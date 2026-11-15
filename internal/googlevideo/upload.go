package googlevideo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AloofLibra/zator/internal/fsutil"
)

// Параметры отправки наблюдений.
const (
	// DefaultIngestURL — пусто (self-contained); удалённый ingest только явно.
	DefaultIngestURL = ""
	// LegacyIngestURL — прежний приёмник наблюдений (opt-in).
	LegacyIngestURL = "https://alooflibra.fun/z4r/googlevideo-observations"
	// EnvIngestURL — переопределение адреса приёма наблюдений.
	EnvIngestURL = "Z2R_GOOGLEVIDEO_INGEST_URL"
	// DefaultUploadInterval — не чаще одной отправки за интервал (3 часа).
	DefaultUploadInterval = 10800 * time.Second
	// MaxUploadPairs — максимум пар host=ip в одной отправке.
	MaxUploadPairs = 24
	// MaxUploadLegacyIPs — максимум «голых» IP (старый формат очереди).
	MaxUploadLegacyIPs = 64
	// UploadTimeout — таймаут одного POST.
	UploadTimeout = 8 * time.Second

	staleSendingAge = time.Hour
	staleLockAge    = 10 * time.Minute
)

// IngestURLFromEnv возвращает совместимый с Bash endpoint с возможностью
// явного переопределения для self-hosted инсталляций.
func IngestURLFromEnv() string {
	if v := strings.TrimSpace(os.Getenv(EnvIngestURL)); v != "" {
		return v
	}
	return LegacyIngestURL
}

// Статусы Uploader.Upload (GOOGLEVIDEO_UPLOAD_LAST_STATUS).
const (
	UploadDisabled  = "disabled"
	UploadNoConsent = "consent_or_token_missing"
	UploadThrottled = "throttled"
	UploadBusy      = "busy"
	UploadNoPending = "no-pending"
	UploadNoNew     = "no-new"
	UploadUploaded  = "uploaded"
	UploadFailed    = "failed"
)

// ErrUpload — отправка не удалась; пакет возвращён в очередь повторов.
var ErrUpload = errors.New("googlevideo: не удалось отправить наблюдения")

// Consent — согласие на телеметрию и учётные данные (tel_enabled, STATS_TOKEN, tel_uuid).
type Consent struct {
	Enabled bool
	Token   string
	UUID    string
}

// ConsentFunc возвращает актуальное согласие; вызывается перед каждой отправкой.
type ConsentFunc func() Consent

// StaticConsent возвращает ConsentFunc с постоянными значениями.
func StaticConsent(enabled bool, token, uuid string) ConsentFunc {
	return func() Consent { return Consent{Enabled: enabled, Token: token, UUID: uuid} }
}

// PostFunc выполняет POST формы и возвращает HTTP-код. Подменяется в тестах
// и для офлайн-режима.
type PostFunc func(ctx context.Context, endpoint string, form url.Values) (status int, err error)

// StubPost — заглушка приёмника: принимает всё с заданным кодом.
func StubPost(status int) PostFunc {
	return func(context.Context, string, url.Values) (int, error) { return status, nil }
}

// HTTPPost возвращает PostFunc на основе http.Client (nil — клиент с таймаутом 8 с).
func HTTPPost(client *http.Client) PostFunc {
	if client == nil {
		client = &http.Client{Timeout: UploadTimeout}
	}
	return func(ctx context.Context, endpoint string, form url.Values) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return resp.StatusCode, nil
	}
}

// UploaderConfig — настройки Uploader.
type UploaderConfig struct {
	// Collector — источник очереди (обязателен).
	Collector *Collector
	// Central — необязательная проверка «IP уже в центральном списке».
	Central CentralChecker
	// Consent — согласие и токен (обязателен; nil — отправка отключена).
	Consent ConsentFunc
	// IngestURL — приёмник (по умолчанию DefaultIngestURL).
	IngestURL string
	// Interval — минимальный период между отправками (по умолчанию 3 ч).
	Interval time.Duration
	// Post — транспорт (по умолчанию HTTPPost(nil)).
	Post PostFunc
	// StampFile — файл времени последней успешной отправки
	// (по умолчанию <каталог очереди>/googlevideo-upload.ts).
	StampFile string
	// Now — источник времени (по умолчанию time.Now).
	Now func() time.Time
	// Logf — необязательный журнал.
	Logf func(format string, args ...any)
}

// Uploader отправляет локальные наблюдения на сервер пакетами.
type Uploader struct {
	cfg UploaderConfig
	mu  sync.Mutex
	seq atomic.Uint64
}

// NewUploader проверяет настройки и создаёт Uploader.
func NewUploader(cfg UploaderConfig) (*Uploader, error) {
	if cfg.Collector == nil {
		return nil, fmt.Errorf("googlevideo: Uploader требует Collector")
	}
	cfg.IngestURL = strings.TrimSpace(cfg.IngestURL)
	if cfg.IngestURL == "" {
		cfg.IngestURL = DefaultIngestURL
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultUploadInterval
	}
	if cfg.Post == nil {
		cfg.Post = HTTPPost(nil)
	}
	if cfg.StampFile == "" {
		cfg.StampFile = filepath.Join(filepath.Dir(cfg.Collector.PendingFile()), UploadStampName)
	}
	return &Uploader{cfg: cfg}, nil
}

func (u *Uploader) now() time.Time {
	if u.cfg.Now != nil {
		return u.cfg.Now()
	}
	return time.Now()
}

func (u *Uploader) logf(format string, args ...any) {
	if u.cfg.Logf != nil {
		u.cfg.Logf(format, args...)
	}
}

// UploadResult — итог Upload.
type UploadResult struct {
	Status string
	// Count — число отправленных элементов (пары + голые IP).
	Count int
	// Pairs и LegacyIPs — состав отправки.
	Pairs     int
	LegacyIPs int
	// HTTPStatus — код ответа приёмника (0 — запрос не выполнялся/транспортная ошибка).
	HTTPStatus int
	// Retry — имя retry-файла, в который вернулся пакет при ошибке или остаток.
	Retry string
}

// LastUpload возвращает время последней успешной отправки (ok=false — не было).
func (u *Uploader) LastUpload() (time.Time, bool) {
	n, ok := numField(readFirstLine(u.cfg.StampFile))
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(n, 0), true
}

func readFirstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := string(b)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func (u *Uploader) lockDir() string {
	return filepath.Join(filepath.Dir(u.cfg.Collector.PendingFile()), UploadLockName)
}

// acquire берёт межпроцессную блокировку (mkdir); устаревшая снимается.
func (u *Uploader) acquire() bool {
	dir := u.lockDir()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return false
	}
	for attempt := 0; attempt < 2; attempt++ {
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return true
		}
		st, serr := os.Stat(dir)
		if serr != nil || u.now().Sub(st.ModTime()) < staleLockAge {
			return false
		}
		_ = os.Remove(dir)
	}
	return false
}

func (u *Uploader) release() { _ = os.Remove(u.lockDir()) }

func (u *Uploader) uniqueName(prefix string) string {
	return prefix + strconv.Itoa(os.Getpid()) + "." + strconv.FormatUint(u.seq.Add(1), 10)
}

// Upload отправляет накопленную очередь одним пакетом (_googlevideo_upload_local_batch):
//
//   - без согласия или токена — ничего не делает (UploadNoConsent);
//   - не чаще одного раза за Interval, если не force (UploadThrottled);
//   - берёт сначала retry-файл, затем pending.tsv (переименованием в .sending.*);
//   - в пакет идут до 24 пар host=ip (допустимое имя, публичный IP, не старше
//     7 суток, не отправленные за 30 суток) и до 64 «голых» IP старого формата,
//     которых нет в центральном списке;
//   - при успехе (2xx) пишется время отправки, пары заносятся в reported.tsv,
//     неотправленный остаток возвращается в retry-файл; при ошибке весь пакет
//     возвращается в retry-файл, а результат — UploadFailed и ErrUpload.
func (u *Uploader) Upload(ctx context.Context, force bool) (UploadResult, error) {
	if u.cfg.Consent == nil || strings.TrimSpace(u.cfg.IngestURL) == "" {
		return UploadResult{Status: UploadDisabled}, nil
	}
	consent := u.cfg.Consent()
	if !consent.Enabled || consent.Token == "" {
		u.logf("upload skipped reason=consent_or_token_missing consent=%t", consent.Enabled)
		return UploadResult{Status: UploadNoConsent}, nil
	}
	if !u.mu.TryLock() {
		return UploadResult{Status: UploadBusy}, nil
	}
	defer u.mu.Unlock()
	if !u.acquire() {
		return UploadResult{Status: UploadBusy}, nil
	}
	defer u.release()

	now := u.now().Unix()
	if !force {
		if last, ok := u.LastUpload(); ok && now-last.Unix() < int64(u.cfg.Interval/time.Second) {
			u.logf("upload deferred reason=interval now=%d last=%d", now, last.Unix())
			return UploadResult{Status: UploadThrottled}, nil
		}
	}

	col := u.cfg.Collector
	col.mu.Lock()
	snapshot, err := u.claim(now)
	col.mu.Unlock()
	if err != nil {
		u.logf("upload queue_error reason=%v", err)
		return UploadResult{Status: UploadFailed}, fmt.Errorf("%w: %v", ErrUpload, err)
	}
	if snapshot == "" {
		u.logf("upload skipped reason=no_pending")
		return UploadResult{Status: UploadNoPending}, nil
	}

	rows := ReadPendingFile(snapshot)
	batch := u.buildBatch(rows, now)
	if len(batch.pairs) == 0 && len(batch.ips) == 0 {
		_ = os.Remove(snapshot)
		u.logf("upload discarded rows=%d reason=no_valid_unlisted_ips", len(rows))
		return UploadResult{Status: UploadNoNew}, nil
	}

	form := url.Values{"token": {consent.Token}, "uuid": {consent.UUID}}
	if len(batch.ips) > 0 {
		form.Set("ips", strings.Join(batch.ips, ","))
	}
	if len(batch.pairs) > 0 {
		parts := make([]string, len(batch.pairs))
		for i, p := range batch.pairs {
			parts[i] = p.host + "=" + p.ip
		}
		form.Set("observations", strings.Join(parts, ","))
	}
	res := UploadResult{Pairs: len(batch.pairs), LegacyIPs: len(batch.ips), Count: len(batch.pairs) + len(batch.ips)}
	u.logf("upload sending legacy_ips=%d host_ip_pairs=%d", res.LegacyIPs, res.Pairs)

	rctx, cancel := context.WithTimeout(ctx, UploadTimeout)
	status, perr := u.cfg.Post(rctx, u.cfg.IngestURL, form)
	cancel()
	res.HTTPStatus = status
	if perr != nil || status < 200 || status > 299 {
		res.Status, res.Count = UploadFailed, 0
		res.Retry = u.restoreRetry(snapshot, now)
		u.logf("upload result=failed http=%d retry=%s err=%v", status, res.Retry, perr)
		if perr != nil {
			return res, fmt.Errorf("%w: %v", ErrUpload, perr)
		}
		return res, fmt.Errorf("%w: HTTP %d", ErrUpload, status)
	}

	if err := u.commit(snapshot, rows, batch, now, &res); err != nil {
		res.Status = UploadFailed
		return res, fmt.Errorf("%w: %v", ErrUpload, err)
	}
	res.Status = UploadUploaded
	u.logf("upload result=accepted http=%d legacy_ips=%d host_ip_pairs=%d", status, res.LegacyIPs, res.Pairs)
	return res, nil
}

type pair struct{ host, ip string }

type batch struct {
	pairs []pair
	ips   []string
}

func (u *Uploader) buildBatch(rows []PendingRow, now int64) batch {
	var b batch
	maxAge := int64(ObservationMaxAge / time.Second)
	reported := map[[2]string]bool{}
	cutoff := now - int64(ReportedRetention/time.Second)
	for _, r := range u.cfg.Collector.Reported() {
		if r.ReportedAt >= cutoff {
			reported[[2]string{r.Host, r.IP}] = true
		}
	}
	seenPair := map[pair]bool{}
	legacy := map[string]bool{}
	for _, r := range rows {
		if r.Observed+maxAge <= now {
			continue
		}
		if r.Host != "" {
			host, ok := NormalizeHostname(r.Host)
			if !ok || !PublicIPv4(r.IP) || reported[[2]string{host, r.IP}] {
				continue
			}
			p := pair{host, r.IP}
			if seenPair[p] || len(b.pairs) >= MaxUploadPairs {
				continue
			}
			seenPair[p] = true
			b.pairs = append(b.pairs, p)
			continue
		}
		if PublicIPv4(r.IP) && !u.central(r.IP) {
			legacy[r.IP] = true
		}
	}
	for ip := range legacy {
		b.ips = append(b.ips, ip)
	}
	sort.Strings(b.ips)
	if len(b.ips) > MaxUploadLegacyIPs {
		b.ips = b.ips[:MaxUploadLegacyIPs]
	}
	return b
}

func (u *Uploader) central(ip string) bool {
	return u.cfg.Central != nil && u.cfg.Central.Contains(ip)
}

// claim забирает пакет: устаревшие .sending.* возвращаются в retry, затем
// первый непустой retry-файл, иначе pending.tsv. "" — очередь пуста.
func (u *Uploader) claim(now int64) (string, error) {
	col := u.cfg.Collector
	dir := filepath.Dir(col.PendingFile())
	for _, f := range col.sendingFiles() {
		st, err := os.Stat(f)
		if err == nil && u.now().Sub(st.ModTime()) > staleSendingAge {
			_ = os.Rename(f, filepath.Join(dir, u.uniqueName(fmt.Sprintf("pending.retry.%d.", now))))
		}
	}
	for _, retry := range col.retryFiles() {
		if st, err := os.Stat(retry); err != nil || st.Size() == 0 {
			continue
		}
		snap := col.PendingFile() + ".sending." + u.uniqueName("")
		if err := os.Rename(retry, snap); err != nil {
			return "", fmt.Errorf("claim_retry: %w", err)
		}
		u.logf("upload claimed source=%s", filepath.Base(retry))
		return snap, nil
	}
	if st, err := os.Stat(col.PendingFile()); err == nil && st.Size() > 0 {
		snap := col.PendingFile() + ".sending." + u.uniqueName("")
		if err := os.Rename(col.PendingFile(), snap); err != nil {
			return "", fmt.Errorf("claim_pending: %w", err)
		}
		u.logf("upload claimed source=%s", filepath.Base(col.PendingFile()))
		return snap, nil
	}
	return "", nil
}

// restoreRetry возвращает пакет в retry-файл; если переименовать не удалось,
// пакет удаляется (как в оригинале), чтобы не копить «зависшие» .sending.*.
func (u *Uploader) restoreRetry(snapshot string, now int64) string {
	dir := filepath.Dir(u.cfg.Collector.PendingFile())
	name := u.uniqueName(fmt.Sprintf("pending.retry.%d.", now))
	if err := os.Rename(snapshot, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(snapshot)
		return ""
	}
	return name
}

// commit фиксирует успешную отправку: время, reported.tsv и остаток очереди.
func (u *Uploader) commit(snapshot string, rows []PendingRow, b batch, now int64, res *UploadResult) error {
	col := u.cfg.Collector
	if err := os.MkdirAll(filepath.Dir(u.cfg.StampFile), 0o755); err != nil {
		u.restoreRetry(snapshot, now)
		return err
	}
	if err := fsutil.AtomicWriteFile(u.cfg.StampFile, []byte(strconv.FormatInt(now, 10)+"\n"), 0o644); err != nil {
		u.restoreRetry(snapshot, now)
		return err
	}

	col.mu.Lock()
	defer col.mu.Unlock()
	cutoff := now - int64(ReportedRetention/time.Second)
	var rep []string
	for _, r := range ReadReportedFile(col.ReportedFile()) {
		if r.ReportedAt >= cutoff {
			rep = append(rep, r.TSV())
		}
	}
	for _, p := range b.pairs {
		rep = append(rep, ReportedRow{Host: p.host, IP: p.ip, ReportedAt: now}.TSV())
	}
	if err := writeLines(col.ReportedFile(), tail(rep, MaxStateRows)); err != nil {
		u.restoreRetry(snapshot, now)
		return err
	}

	sentPair := map[pair]bool{}
	for _, p := range b.pairs {
		sentPair[pair{p.host, p.ip}] = true
	}
	sentIP := map[string]bool{}
	for _, ip := range b.ips {
		sentIP[ip] = true
	}
	maxAge := int64(ObservationMaxAge / time.Second)
	var left []string
	for _, r := range rows {
		switch {
		case r.Observed+maxAge <= now:
		case r.Host != "" && sentPair[pair{r.Host, r.IP}]:
		case r.Host == "" && (sentIP[r.IP] || u.central(r.IP)):
		default:
			left = append(left, r.TSV())
		}
	}
	if len(left) > 0 {
		name := u.uniqueName(fmt.Sprintf("pending.retry.%d.", now))
		if err := writeLines(filepath.Join(filepath.Dir(col.PendingFile()), name), left); err != nil {
			u.restoreRetry(snapshot, now)
			return err
		}
		res.Retry = name
	}
	_ = os.Remove(snapshot)
	return nil
}
