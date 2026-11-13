package netcheck

import "fmt"

const manualHint = "Проверьте доступность вручную. Возможно ошибка теста."

// versionParts возвращает «факт» и «подсказку» для версии TLS
// (z2r_tls_version_parts). Подсказка пуста при успехе.
func versionParts(ver TLSVersion, d VersionDetail, opts Options) (fact, hint string) {
	label := ver.Label()
	why := "важно в основном для всего современного"
	if ver == TLS12 {
		why = "важно для ТВ и т.п."
	}
	hint = manualHint
	switch d.State {
	case StateOK, StateHTTP:
		fact = fmt.Sprintf("Есть ответ по TLS %s (%s): %s %d за %s с", label, why, d.Proto, d.Code, d.Time)
		hint = ""
	case StateAborted:
		fact = "Проверка остановлена: сайт уже ответил по другой версии TLS"
		hint = ""
	case StateDNS:
		fact = fmt.Sprintf("Нет ответа по TLS %s (%s) Домен не разрешается (DNS).", label, why)
	case StateTimeout:
		fact = fmt.Sprintf("Нет ответа по TLS %s (%s) Таймаут %dсек.", label, why, int(opts.MaxTime.Seconds()))
	case StateTLS:
		fact = fmt.Sprintf("Нет ответа по TLS %s (%s) Ошибка TLS-рукопожатия.", label, why)
	case StateConn:
		fact = fmt.Sprintf("Нет ответа по TLS %s (%s) Соединение не устанавливается.", label, why)
	case StateUnsupported:
		fact = fmt.Sprintf("Нет ответа по TLS %s (%s) Локальный клиент не поддерживает эту версию TLS.", label, why)
	default:
		fact = fmt.Sprintf("Нет ответа по TLS %s (%s) Ошибка запроса.", label, why)
	}
	return fact, hint
}

func joinFactHint(fact, hint string) string {
	if hint == "" {
		return fact
	}
	return fact + " " + hint
}

func describeVersion(ver TLSVersion, d VersionDetail, opts Options) string {
	return joinFactHint(versionParts(ver, d, opts))
}

// describeDownload — z2r_tls_download_parts/_text.
func describeDownload(d Download, opts Options) string {
	hint := manualHint
	var fact string
	switch d.State {
	case DLOK:
		fact = fmt.Sprintf("Данные: получено %d байт за %s с (код %d)", d.Size, d.Time, d.Code)
		if d.Retry {
			fact += " — со второй попытки, первая оборвалась"
		}
		hint = ""
	case DLCut:
		fact = fmt.Sprintf("Данные оборвались: получено %d байт и поток остановился.", d.Size)
	case DLZero:
		if d.Code != 0 {
			fact = fmt.Sprintf("Данные: код %d, 0 байт — сервер не отдал тело ответа.", d.Code)
		} else {
			fact = "Данные: 0 байт — TLS работает, но тело ответа не приходит."
		}
	default:
		if d.Err == ErrTimeout {
			fact = fmt.Sprintf("Данные: таймаут — тело ответа не начало приходить за %d сек.", int(opts.DownloadMaxTime.Seconds()))
		} else {
			fact = fmt.Sprintf("Данные: скачать не удалось (%s).", errLabel(d.Err))
		}
	}
	return joinFactHint(fact, hint)
}

func errLabel(k ErrKind) string {
	if k == ErrNone {
		return "ошибка"
	}
	return "ошибка: " + string(k)
}

func failReason(s12, s13 string) string {
	switch {
	case s12 == StateDNS || s13 == StateDNS:
		return "домен не разрешается системным DNS; браузер может работать через свой DoH"
	case s12 == StateTLS || s13 == StateTLS:
		return "TLS-рукопожатие не проходит"
	case s12 == StateTimeout || s13 == StateTimeout:
		return "нет ответа от сервера (таймаут)"
	}
	return "соединение не устанавливается"
}

// Verdict — z2r_tls_target_verdict: итоговый вердикт и текст по цели.
// dl == nil означает, что докачка пропущена.
func Verdict(v12, v13 VersionDetail, dl *Download) (verdict, text string) {
	t12, t13 := v12.Answered(), v13.Answered()
	if !t12 && !t13 {
		return VerdictFail, fmt.Sprintf("Нет ответа: %s. Цель недоступна с роутера (это не доказывает сбой лока). %s",
			failReason(v12.State, v13.State), manualHint)
	}
	if !v12.CodeOK() && !v13.CodeOK() {
		best := v13.Code
		if t12 {
			best = v12.Code
		}
		return VerdictOK, fmt.Sprintf("Доступ есть: TLS работает, сервер ответил кодом %d.", best)
	}
	if dl != nil {
		switch DownloadState(dl.Err, dl.Code, dl.Size) {
		case DLCut:
			return VerdictFail, fmt.Sprintf("TLS работает, но поток данных срезается после %d байт - похоже на блокировку «16кб». %s", dl.Size, manualHint)
		case DLZero, DLFail:
			return VerdictFail, "TLS работает, но данные не приходят — страница не скачивается. " + manualHint
		}
		if dl.Retry {
			return VerdictWarn, "Поток иногда срезается — повторное соединение прошло, страница может открываться не с первого раза. Проверьте доступность вручную."
		}
	}
	if !t13 {
		return VerdictWarn, "Сайт отвечает только по TLS 1.2 — TLS 1.3 недоступен, современные браузеры могут не открыться. Проверьте вручную."
	}
	return VerdictOK, "Сайт доступен: TLS работает, данные идут."
}

// ShortResult — z2r_tls_short_result: компактный результат одной стратегии
// для таблицы автопрогона. pref задаёт требуемые версии TLS: при 12/13/both
// зелёным считается только прохождение выбранных версий.
func ShortResult(v12, v13 VersionDetail, dl *Download, pref TLSPref) (verdict, short string) {
	t12, t13 := v12.Answered(), v13.Answered()
	q12, q13 := v12.CodeOK(), v13.CodeOK()

	if !t12 && !t13 {
		switch {
		case v12.State == StateDNS || v13.State == StateDNS:
			return VerdictFail, "нет ответа (DNS)"
		case v12.State == StateTLS || v13.State == StateTLS:
			return VerdictFail, "TLS-рукопожатие не проходит"
		case v12.State == StateTimeout || v13.State == StateTimeout:
			return VerdictFail, "нет ответа (таймаут)"
		}
		return VerdictFail, "нет ответа"
	}

	if !q12 && !q13 {
		best := v13.Code
		if t12 {
			best = v12.Code
		}
		return prefResult(pref, t12, t13, fmt.Sprintf("сервер ответил кодом %d", best), best, "")
	}

	if dl != nil {
		switch DownloadState(dl.Err, dl.Code, dl.Size) {
		case DLCut:
			return VerdictFail, fmt.Sprintf("срез после %d байт", dl.Size)
		case DLZero:
			return VerdictFail, "тело не приходит"
		case DLFail:
			return VerdictFail, "докачка не началась"
		case DLOK:
			if dl.Retry {
				return VerdictWarn, "срез нестабильный, повтор прошёл"
			}
			data := fmt.Sprintf("данные %d байт за %s с", dl.Size, dl.Time)
			return prefResult(pref, q12, q13, data, 0, data)
		}
	}
	if !t13 {
		return VerdictWarn, "только TLS 1.2"
	}
	return VerdictOK, "данные идут"
}

// prefResult формирует результат с учётом TLSPref. Для «ответ без данных»
// (code != 0) текст «<base> (обе версии)» добавляется при pref=both.
func prefResult(pref TLSPref, has12, has13 bool, base string, code int, data string) (string, string) {
	codeMode := data == ""
	onlyText := func(v string) string {
		if codeMode {
			return fmt.Sprintf("работает только TLS %s, код %d", v, code)
		}
		return fmt.Sprintf("работает только TLS %s, %s", v, data)
	}
	switch pref {
	case PrefBoth:
		switch {
		case has12 && has13:
			if codeMode {
				return VerdictOK, base + " (обе версии)"
			}
			return VerdictOK, base
		case has12:
			return VerdictWarn, onlyText("1.2")
		default:
			return VerdictWarn, onlyText("1.3")
		}
	case Pref12:
		if has12 {
			return VerdictOK, base
		}
		return VerdictWarn, onlyText("1.3")
	case Pref13:
		if has13 {
			return VerdictOK, base
		}
		return VerdictWarn, onlyText("1.2")
	}
	return VerdictOK, base
}
