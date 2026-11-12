package sweep

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/AloofLibra/zator/internal/netcheck"
)

// ProgressRow — строка прогресса воркера (progress.<name>.tsv):
// epoch, цель, стратегия, токен, состояния TLS 1.2/1.3, байты и время докачки, кратко.
type ProgressRow struct {
	Epoch    int64
	Target   string // номер профиля или домен
	Strategy int
	Token    string
	TLS12    string
	TLS13    string
	DLBytes  string
	DLTime   string
	Short    string
}

// TSV форматирует строку (без перевода строки).
func (r ProgressRow) TSV() string {
	return strings.Join([]string{
		strconv.FormatInt(r.Epoch, 10), cleanField(r.Target), strconv.Itoa(r.Strategy), r.Token,
		cleanField(r.TLS12), cleanField(r.TLS13), cleanField(r.DLBytes), cleanField(r.DLTime), cleanField(r.Short),
	}, "\t")
}

func (e *Env) progressRow(target string, tr TryResult) ProgressRow {
	return ProgressRow{
		Epoch:    e.now().Unix(),
		Target:   target,
		Strategy: tr.Strategy,
		Token:    tr.Verdict,
		TLS12:    tr.Target.TLS12Detail.State,
		TLS13:    tr.Target.TLS13Detail.State,
		DLBytes:  tr.DLBytes,
		DLTime:   tr.DLTime,
		Short:    tr.Short,
	}
}

// ProfileSweepConfig — параметры автопрогона одного профиля.
type ProfileSweepConfig struct {
	// Name — имя воркера для прогресса (yt|gv|ds), необязательно.
	Name string
	// Profile, Protos — профиль и его протоколы (nil — по таблице профилей).
	Profile int
	Protos  []string
	// Label и URL — цель проверки.
	Label, URL string
	// Max — число стратегий (перебор 1..Max).
	Max int
	// TLSPref — требуемые версии TLS: any|12|13|both.
	TLSPref netcheck.TLSPref
	// Pause — интервал между проверками (включает Settle).
	Pause time.Duration
	// GreenPause — пауза после зелёной стратегии с обеими TLS (0 — не менять).
	GreenPause time.Duration
	// MarkUnknown — результаты ok/warn помечаются unknown.
	MarkUnknown bool

	// OnTry вызывается перед каждой пробой (текущая стратегия для WebUI).
	OnTry func(strategy int)
	// OnRow вызывается после каждой пробы (запись progress.<name>.tsv).
	OnRow func(row ProgressRow)
	// OnPacing вызывается перед паузой между пробами.
	OnPacing func(strategy int, pause time.Duration)
}

// BestKind — вид выбранной лучшей стратегии.
const (
	BestKindBest = "best" // зелёная
	BestKindWarn = "warn" // жёлтая (автоматически не применяется)
)

// ProfileSweepResult — итог автопрогона профиля (best.<name>).
type ProfileSweepResult struct {
	Best      int // 0 — нет
	BestKind  string
	BestShort string
	Greens    []int // токен ok
	Fulls     []int // ok и обе TLS 2xx/3xx
	Warns     []int
	NOK       int
	NWarn     int
	NFail     int
	NUnknown  int
	// Interrupted — проход неполный (отмена/обрыв): результат применять нельзя.
	Interrupted bool
	Rows        []ProgressRow
}

type speedStat struct {
	strategy int
	time     float64
	size     int
	short    string
}

// fastest — стратегия с максимальной скоростью size/time (первая при равенстве).
func fastest(stats []speedStat) (speedStat, bool) {
	var (
		best speedStat
		max  = -1.0
		ok   bool
	)
	for _, s := range stats {
		if s.time > 0 && float64(s.size)/s.time > max {
			max = float64(s.size) / s.time
			best, ok = s, true
		}
	}
	return best, ok
}

// AutoSweepProfile перебирает стратегии 1..Max одного профиля: для каждой
// ставит пробный лок, проверяет цель, пишет строку прогресса и делает паузу.
// Лучшая — самая быстрая зелёная; без докачки — первая зелёная; без зелёных —
// жёлтая (BestKindWarn). Локи не восстанавливаются и не применяются —
// это забота вызывающего (см. Coordinator). Отмена контекста — не ошибка:
// результат помечается Interrupted. Ошибка записи лока прерывает прогон.
func (e *Env) AutoSweepProfile(ctx context.Context, cfg ProfileSweepConfig) (ProfileSweepResult, error) {
	var res ProfileSweepResult
	if err := e.validate(); err != nil {
		return res, err
	}
	if cfg.Max < 1 {
		return res, fmt.Errorf("%w: %d", ErrNoStrategies, cfg.Max)
	}
	target := strconv.Itoa(cfg.Profile)
	var okStats, warnStats []speedStat
	var runErr error

	s := 1
	for ; s <= cfg.Max; s++ {
		if ctx.Err() != nil {
			break
		}
		if cfg.OnTry != nil {
			cfg.OnTry(s)
		}
		tr, err := e.Try(ctx, TryRequest{
			Profile: cfg.Profile, Protos: cfg.Protos, Strategy: s,
			Label: cfg.Label, URL: cfg.URL, TLSPref: cfg.TLSPref, MarkUnknown: cfg.MarkUnknown,
		})
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				runErr = err
			}
			break
		}

		row := e.progressRow(target, tr)
		res.Rows = append(res.Rows, row)
		if cfg.OnRow != nil {
			cfg.OnRow(row)
		}

		dlStat := speedStat{strategy: s, size: dlSize(tr), time: dlSeconds(tr), short: tr.Short}
		hasStat := tr.Target.Download != nil && dlStat.time > 0
		switch tr.Verdict {
		case TokenOK:
			res.NOK++
			res.Greens = append(res.Greens, s)
			if tr.Full {
				res.Fulls = append(res.Fulls, s)
			}
			if hasStat {
				okStats = append(okStats, dlStat)
			}
		case TokenWarn:
			res.NWarn++
			res.Warns = append(res.Warns, s)
			if hasStat {
				warnStats = append(warnStats, dlStat)
			}
		case TokenUnknown:
			res.NUnknown++
		default:
			res.NFail++
		}

		if s < cfg.Max {
			pause := cfg.Pause - e.Settle // Settle входит в паузу
			if pause < 0 {
				pause = 0
			}
			if tr.Verdict == TokenOK && tr.Full && cfg.GreenPause > 0 {
				pause = cfg.GreenPause
			}
			if cfg.OnPacing != nil {
				cfg.OnPacing(s, pause)
			}
			if err := e.sleep(ctx, pause); err != nil {
				break
			}
		}
	}
	// Проход полный, только если перебраны все стратегии.
	res.Interrupted = s <= cfg.Max

	switch {
	case len(okStats) > 0:
		w, _ := fastest(okStats)
		res.Best, res.BestKind, res.BestShort = w.strategy, BestKindBest, w.short
	case len(res.Greens) > 0:
		res.Best, res.BestKind, res.BestShort = res.Greens[0], BestKindBest, "сервер ответил (без докачки)"
	case len(res.Warns) > 0:
		res.Best, res.BestKind, res.BestShort = res.Warns[0], BestKindWarn, "жёлтая (единственная без красных)"
		if w, ok := fastest(warnStats); ok {
			res.Best, res.BestShort = w.strategy, w.short
		}
	}
	return res, runErr
}

func dlSize(tr TryResult) int {
	if tr.Target.Download == nil {
		return 0
	}
	return tr.Target.Download.Size
}

func dlSeconds(tr TryResult) float64 {
	if tr.Target.Download == nil {
		return 0
	}
	return tr.Target.Download.Seconds()
}
