package netcheck

import (
	"strings"
	"testing"
)

func okV(code int) VersionDetail {
	return VersionDetail{Code: code, Proto: "HTTP/2", Time: "0.5", IP: "1.2.3.4", State: VersionState(code, ErrNone)}
}

func failV(state string) VersionDetail {
	return VersionDetail{Proto: "-", Time: "-", IP: "-", State: state}
}

func dlOK(size int) *Download {
	return &Download{Code: 206, Size: size, Time: "1.000", State: DLOK}
}

func TestVerdict(t *testing.T) {
	cut := &Download{Code: 206, Size: 1500, Time: "1", State: DLCut, Err: ErrTimeout}
	zero := &Download{Code: 206, State: DLZero}
	fail := &Download{Err: ErrTimeout, State: DLFail}
	retry := &Download{Code: 206, Size: 65536, Time: "1", State: DLOK, Retry: true}

	cases := []struct {
		name     string
		v12, v13 VersionDetail
		dl       *Download
		verdict  string
		contains string
	}{
		{"all good", okV(200), okV(200), dlOK(65536), VerdictOK, "Сайт доступен"},
		{"both dead dns", failV(StateDNS), failV(StateNone), nil, VerdictFail, "не разрешается системным DNS"},
		{"both dead tls", failV(StateTLS), failV(StateTLS), nil, VerdictFail, "TLS-рукопожатие не проходит"},
		{"both dead timeout", failV(StateTimeout), failV(StateConn), nil, VerdictFail, "таймаут"},
		{"both dead conn", failV(StateConn), failV(StateConn), nil, VerdictFail, "соединение не устанавливается"},
		{"4xx is ok", okV(404), okV(404), nil, VerdictOK, "кодом 404"},
		{"4xx only 13", failV(StateTLS), okV(403), nil, VerdictOK, "кодом 403"},
		{"cut", okV(200), okV(200), cut, VerdictFail, "после 1500 байт"},
		{"zero", okV(200), okV(200), zero, VerdictFail, "данные не приходят"},
		{"dl fail", okV(200), okV(200), fail, VerdictFail, "данные не приходят"},
		{"retry", okV(200), okV(200), retry, VerdictWarn, "Поток иногда срезается"},
		{"only 1.2", okV(200), failV(StateTLS), dlOK(65536), VerdictWarn, "только по TLS 1.2"},
		{"only 1.3 is ok", failV(StateTLS), okV(200), dlOK(65536), VerdictOK, "Сайт доступен"},
	}
	for _, c := range cases {
		v, text := Verdict(c.v12, c.v13, c.dl)
		if v != c.verdict || !strings.Contains(text, c.contains) {
			t.Errorf("%s: got (%q, %q), want %q containing %q", c.name, v, text, c.verdict, c.contains)
		}
	}
}

func TestShortResult(t *testing.T) {
	cut := &Download{Code: 206, Size: 1500, Time: "1", State: DLCut, Err: ErrTimeout}
	zero := &Download{Code: 206, State: DLZero}
	fail := &Download{State: DLFail, Err: ErrTimeout}
	retry := &Download{Code: 206, Size: 65536, Time: "1.0", State: DLOK, Retry: true}

	cases := []struct {
		name     string
		v12, v13 VersionDetail
		dl       *Download
		pref     TLSPref
		verdict  string
		contains string
	}{
		{"dns", failV(StateDNS), failV(StateNone), nil, PrefAny, "fail", "DNS"},
		{"tls", failV(StateTLS), failV(StateNone), nil, PrefAny, "fail", "TLS-рукопожатие"},
		{"timeout", failV(StateTimeout), failV(StateNone), nil, PrefAny, "fail", "таймаут"},
		{"none", failV(StateConn), failV(StateConn), nil, PrefAny, "fail", "нет ответа"},
		{"cut", okV(200), okV(200), cut, PrefAny, "fail", "срез после 1500"},
		{"zero", okV(200), okV(200), zero, PrefAny, "fail", "тело не приходит"},
		{"dlfail", okV(200), okV(200), fail, PrefAny, "fail", "докачка не началась"},
		{"retry", okV(200), okV(200), retry, PrefAny, "warn", "повтор прошёл"},
		{"any ok", okV(200), okV(200), dlOK(65536), PrefAny, "ok", "данные 65536 байт за 1.000 с"},
		{"both ok", okV(200), okV(200), dlOK(65536), PrefBoth, "ok", "данные 65536"},
		{"both only12", okV(200), failV(StateTLS), dlOK(65536), PrefBoth, "warn", "работает только TLS 1.2"},
		{"both only13", failV(StateTLS), okV(200), dlOK(65536), PrefBoth, "warn", "работает только TLS 1.3"},
		{"pref12 ok", okV(200), failV(StateTLS), dlOK(65536), Pref12, "ok", "данные"},
		{"pref12 only13", failV(StateTLS), okV(200), dlOK(65536), Pref12, "warn", "только TLS 1.3"},
		{"pref13 ok", failV(StateTLS), okV(200), dlOK(65536), Pref13, "ok", "данные"},
		{"pref13 only12", okV(200), failV(StateTLS), dlOK(65536), Pref13, "warn", "только TLS 1.2"},
		{"any only12", okV(200), failV(StateTLS), dlOK(65536), PrefAny, "ok", "данные"},
		{"4xx any", okV(404), okV(404), nil, PrefAny, "ok", "кодом 404"},
		{"4xx both", okV(404), okV(404), nil, PrefBoth, "ok", "обе версии"},
		{"4xx both only12", okV(404), failV(StateTLS), nil, PrefBoth, "warn", "только TLS 1.2, код 404"},
		{"4xx pref13 only12", okV(404), failV(StateTLS), nil, Pref13, "warn", "только TLS 1.2, код 404"},
		{"4xx pref12 only13", failV(StateTLS), okV(404), nil, Pref12, "warn", "только TLS 1.3, код 404"},
		{"no dl only 1.2", okV(200), failV(StateTLS), nil, PrefAny, "warn", "только TLS 1.2"},
		{"no dl both", okV(200), okV(200), nil, PrefAny, "ok", "данные идут"},
	}
	for _, c := range cases {
		v, s := ShortResult(c.v12, c.v13, c.dl, c.pref)
		if v != c.verdict || !strings.Contains(s, c.contains) {
			t.Errorf("%s: got (%q, %q), want %q containing %q", c.name, v, s, c.verdict, c.contains)
		}
	}
}

func TestVersionTextsMatchReference(t *testing.T) {
	opts := Options{MaxTime: 5e9}.withDefaults()
	d := okV(200)
	if got := describeVersion(TLS12, d, opts); got != "Есть ответ по TLS 1.2 (важно для ТВ и т.п.): HTTP/2 200 за 0.5 с" {
		t.Errorf("tls1.2 text = %q", got)
	}
	if got := describeVersion(TLS13, d, opts); got != "Есть ответ по TLS 1.3 (важно в основном для всего современного): HTTP/2 200 за 0.5 с" {
		t.Errorf("tls1.3 text = %q", got)
	}
	got := describeVersion(TLS12, failV(StateTLS), opts)
	want := "Нет ответа по TLS 1.2 (важно для ТВ и т.п.) Ошибка TLS-рукопожатия. Проверьте доступность вручную. Возможно ошибка теста."
	if got != want {
		t.Errorf("fail text = %q, want %q", got, want)
	}
	if got := describeVersion(TLS13, failV(StateTimeout), opts); !strings.Contains(got, "Таймаут 5сек.") {
		t.Errorf("timeout text = %q", got)
	}
	if got := describeVersion(TLS13, failV(StateNone), opts); !strings.Contains(got, "Ошибка запроса") {
		t.Errorf("none text = %q", got)
	}
}

func TestDownloadTexts(t *testing.T) {
	opts := Options{}.withDefaults()
	cases := []struct {
		d        Download
		contains string
	}{
		{Download{Code: 206, Size: 65536, Time: "1.2", State: DLOK}, "Данные: получено 65536 байт за 1.2 с (код 206)"},
		{Download{Code: 206, Size: 65536, Time: "1.2", State: DLOK, Retry: true}, "со второй попытки"},
		{Download{Code: 206, State: DLZero}, "код 206, 0 байт"},
		{Download{State: DLZero}, "TLS работает, но тело ответа не приходит"},
		{Download{State: DLFail, Err: ErrTimeout}, "таймаут — тело ответа не начало приходить за 10 сек"},
		{Download{State: DLFail, Err: ErrConn}, "скачать не удалось"},
	}
	for _, c := range cases {
		if got := describeDownload(c.d, opts); !strings.Contains(got, c.contains) {
			t.Errorf("describeDownload(%+v) = %q, want to contain %q", c.d, got, c.contains)
		}
	}
	if got := describeDownload(Download{State: DLOK, Size: 1, Time: "1", Code: 200}, opts); strings.Contains(got, manualHint) {
		t.Errorf("при успехе подсказки быть не должно: %q", got)
	}
}
