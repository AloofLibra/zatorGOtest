package sweep

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/config"
	"github.com/AloofLibra/zator/internal/netcheck"
	"github.com/AloofLibra/zator/internal/orchestra"
)

// Env — окружение проб: куда писать пробные локи и чем проверять цель.
type Env struct {
	// Locks — хранилище фиксаций (обычно *orchestra.Store).
	Locks Locker
	// Checker — проверка цели (обычно *netcheck.Checker).
	Checker TargetChecker
	// Scope — область локов ("" = default).
	Scope string
	// Settle — пауза после записи лока: Lua кеширует фиксации ~2 с, иначе
	// проба проверила бы прежнюю стратегию.
	Settle time.Duration
	// Sleep — прерываемый сон (по умолчанию на таймере).
	Sleep SleepFunc
	// Now — источник времени (по умолчанию time.Now).
	Now func() time.Time
}

func (e *Env) scope() string {
	if e.Scope == "" {
		return orchestra.ScopeDefault
	}
	return e.Scope
}

func (e *Env) sleep(ctx context.Context, d time.Duration) error {
	if e.Sleep != nil {
		return e.Sleep(ctx, d)
	}
	return sleepCtx(ctx, d)
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Env) validate() error {
	if e.Locks == nil {
		return fmt.Errorf("%w: не задан Locks", ErrConfig)
	}
	if e.Checker == nil {
		return fmt.Errorf("%w: не задан Checker", ErrConfig)
	}
	return nil
}

// TryRequest — одна проба стратегии.
type TryRequest struct {
	// Profile — номер профиля (лок profile/proto). Игнорируется при Domain.
	Profile int
	// Domain — если задан, лок ставится на домен (только tls, как у custom_rkn).
	Domain string
	// Protos — протоколы лока; по умолчанию протоколы профиля (для домена — tls).
	Protos []string
	// Strategy — номер стратегии (>= 1).
	Strategy int
	// Label и URL — цель проверки.
	Label, URL string
	// TLSPref — требование к версиям TLS (по умолчанию any).
	TLSPref netcheck.TLSPref
	// MarkUnknown — вместо ok/warn/fail вернуть unknown (TLS/CDN-тест
	// googlevideo без медиапроверки); fail остаётся fail.
	MarkUnknown bool
}

// TryResult — итог пробы.
type TryResult struct {
	Strategy int
	// Verdict — ok|warn|fail|unknown.
	Verdict string
	// Short — краткое пояснение (z2r_tls_short_result).
	Short string
	// Target — полный результат проверки цели.
	Target netcheck.TargetResult
	// Q12/Q13 — версия ответила 2xx/3xx.
	Q12, Q13 bool
	// DLBytes/DLTime — результат докачки ("-" если этап пропущен).
	DLBytes, DLTime string
	// Speed — скорость докачки, байт/с (0, если неизвестна).
	Speed float64
	// Full — обе версии TLS ответили 2xx/3xx.
	Full bool
}

// Try — «пробный лок + проверка»: ставит фиксацию стратегии, ждёт Settle и
// проверяет цель. Ошибка — только при неудачной записи лока или отмене.
func (e *Env) Try(ctx context.Context, req TryRequest) (TryResult, error) {
	if err := e.validate(); err != nil {
		return TryResult{}, err
	}
	if req.Strategy < 1 {
		return TryResult{}, fmt.Errorf("%w: стратегия %d", orchestra.ErrInvalid, req.Strategy)
	}
	if err := ctx.Err(); err != nil {
		return TryResult{}, err
	}

	profile, protos := strconv.Itoa(req.Profile), req.Protos
	if req.Domain != "" {
		profile = req.Domain
		if protos == nil {
			protos = []string{"tls"}
		}
	} else if protos == nil {
		protos = config.ProfileProtos(req.Profile)
	}
	if len(protos) == 0 {
		return TryResult{}, fmt.Errorf("%w: для профиля %s нет протоколов", ErrConfig, profile)
	}
	for _, p := range protos {
		if err := e.Locks.SetLock(e.scope(), profile, p, orchestra.Strategy(req.Strategy)); err != nil {
			return TryResult{}, fmt.Errorf("sweep: лок %s/%s=%d: %w", profile, p, req.Strategy, err)
		}
	}
	if err := e.sleep(ctx, e.Settle); err != nil {
		return TryResult{}, err
	}

	tr := e.Checker.CheckTarget(ctx, req.Label, req.URL)
	if err := ctx.Err(); err != nil {
		return TryResult{}, err
	}
	return buildTry(req, tr), nil
}

func buildTry(req TryRequest, tr netcheck.TargetResult) TryResult {
	pref := req.TLSPref
	if pref == "" {
		pref = netcheck.PrefAny
	}
	verdict, short := netcheck.ShortResult(tr.TLS12Detail, tr.TLS13Detail, tr.Download, pref)
	if req.MarkUnknown && verdict != TokenFail {
		verdict = TokenUnknown
		short = "Плюшевый TLS/CDN-тест — медиаданные не проверены; " + short
	}
	switch verdict {
	case TokenOK, TokenWarn, TokenUnknown:
	default:
		verdict = TokenFail
	}
	res := TryResult{
		Strategy: req.Strategy,
		Verdict:  verdict,
		Short:    short,
		Target:   tr,
		Q12:      tr.TLS12Detail.CodeOK(),
		DLBytes:  "-",
		DLTime:   "-",
	}
	res.Q13 = tr.TLS13Detail.CodeOK()
	res.Full = res.Q12 && res.Q13
	if tr.Download != nil {
		res.DLBytes = strconv.Itoa(tr.Download.Size)
		res.DLTime = tr.Download.Time
		res.Speed = tr.Download.Speed()
	}
	return res
}

// cleanField убирает управляющие символы из текста для TSV.
func cleanField(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return s
}
