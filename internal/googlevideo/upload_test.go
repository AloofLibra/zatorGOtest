package googlevideo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder — PostFunc, запоминающий формы и отвечающий заданным кодом/ошибкой.
type recorder struct {
	mu     sync.Mutex
	forms  []url.Values
	urls   []string
	status int
	err    error
	block  chan struct{}
	enter  chan struct{}
}

func newRecorder() *recorder { return &recorder{status: 200} }

func (r *recorder) post(ctx context.Context, endpoint string, form url.Values) (int, error) {
	r.mu.Lock()
	r.forms = append(r.forms, form)
	r.urls = append(r.urls, endpoint)
	status, err, block, enter := r.status, r.err, r.block, r.enter
	r.mu.Unlock()
	if enter != nil {
		close(enter)
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return status, err
}

func (r *recorder) set(status int, err error) {
	r.mu.Lock()
	r.status, r.err = status, err
	r.mu.Unlock()
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.forms)
}

func (r *recorder) last() url.Values {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.forms[len(r.forms)-1]
}

type uploadEnv struct {
	t   *testing.T
	col *Collector
	clk *clock
	up  *Uploader
	rec *recorder
}

func newUploadEnv(t *testing.T, central CentralChecker, consent ConsentFunc) *uploadEnv {
	t.Helper()
	col, clk := newCollector(t, central)
	rec := newRecorder()
	if consent == nil {
		consent = StaticConsent(true, "tok", "uuid-1")
	}
	up, err := NewUploader(UploaderConfig{
		Collector: col, Central: central, Consent: consent, Post: rec.post, Now: clk.Now,
		IngestURL: "https://ingest.test/upload",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &uploadEnv{t: t, col: col, clk: clk, up: up, rec: rec}
}

func (e *uploadEnv) retryFiles() []string { return e.col.retryFiles() }

func (e *uploadEnv) upload(force bool) UploadResult {
	e.t.Helper()
	res, err := e.up.Upload(context.Background(), force)
	if err != nil {
		e.t.Fatalf("Upload: %v (%+v)", err, res)
	}
	return res
}

func TestNewUploader_Validation(t *testing.T) {
	if _, err := NewUploader(UploaderConfig{}); err == nil {
		t.Error("Collector is required")
	}
	col, _ := newCollector(t, nil)
	up, err := NewUploader(UploaderConfig{Collector: col})
	if err != nil {
		t.Fatal(err)
	}
	if up.cfg.IngestURL != "" || up.cfg.Interval != DefaultUploadInterval || up.cfg.Post == nil ||
		up.cfg.StampFile != filepath.Join(filepath.Dir(col.PendingFile()), UploadStampName) {
		t.Errorf("defaults: %+v", up.cfg)
	}
	if DefaultIngestURL != "" {
		t.Error("DefaultIngestURL must be empty (self-contained)")
	}
	if DefaultUploadInterval != 3*time.Hour || MaxUploadPairs != 24 || MaxUploadLegacyIPs != 64 {
		t.Error("limits must match the reference implementation")
	}
	// Без Consent / без IngestURL отправка отключена.
	if res, err := up.Upload(context.Background(), true); err != nil || res.Status != UploadDisabled {
		t.Errorf("no consent/url: %v %+v", err, res)
	}
}

func TestUpload_ConsentGate(t *testing.T) {
	for name, consent := range map[string]ConsentFunc{
		"disabled":    StaticConsent(false, "tok", "u"),
		"empty token": StaticConsent(true, "", "u"),
	} {
		t.Run(name, func(t *testing.T) {
			e := newUploadEnv(t, nil, consent)
			mustObserve(t, e.col, hostA, "9.9.9.9", 600)
			res := e.upload(true)
			if res.Status != UploadNoConsent || e.rec.count() != 0 {
				t.Errorf("res=%+v posts=%d", res, e.rec.count())
			}
			// Очередь не тронута, время отправки не записано.
			if len(e.col.Pending()) != 1 {
				t.Error("queue must stay intact")
			}
			if _, ok := e.up.LastUpload(); ok {
				t.Error("stamp must not be written")
			}
		})
	}
}

func TestUpload_ConsentIsReadOnEveryCall(t *testing.T) {
	enabled := false
	e := newUploadEnv(t, nil, func() Consent { return Consent{Enabled: enabled, Token: "t", UUID: "u"} })
	mustObserve(t, e.col, hostA, "9.9.9.9", 600)
	if res := e.upload(true); res.Status != UploadNoConsent {
		t.Fatalf("status = %s", res.Status)
	}
	enabled = true
	if res := e.upload(true); res.Status != UploadUploaded {
		t.Errorf("after enabling: %s", res.Status)
	}
}

func TestUpload_SuccessFlow(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	mustObserve(t, e.col, hostA, "9.9.9.9", 600)
	mustObserve(t, e.col, hostB, "8.8.4.4", 600)
	mustObserve(t, e.col, hostB, "9.9.9.9", 600)

	res := e.upload(false)
	if res.Status != UploadUploaded || res.Pairs != 3 || res.LegacyIPs != 0 || res.Count != 3 || res.HTTPStatus != 200 || res.Retry != "" {
		t.Fatalf("result = %+v", res)
	}
	form := e.rec.last()
	if form.Get("token") != "tok" || form.Get("uuid") != "uuid-1" || form.Has("ips") {
		t.Errorf("form = %v", form)
	}
	want := hostA + "=9.9.9.9," + hostB + "=8.8.4.4," + hostB + "=9.9.9.9"
	if form.Get("observations") != want {
		t.Errorf("observations = %q, want %q", form.Get("observations"), want)
	}
	if e.rec.urls[0] != "https://ingest.test/upload" {
		t.Errorf("url = %s", e.rec.urls[0])
	}

	// Очередь опустела, снапшот удалён, время записано.
	if len(e.col.Pending()) != 0 || len(e.retryFiles()) != 0 || len(e.col.sendingFiles()) != 0 {
		t.Error("queue must be drained")
	}
	if _, err := os.Stat(e.col.PendingFile()); !os.IsNotExist(err) {
		t.Error("pending.tsv must be gone")
	}
	if at, ok := e.up.LastUpload(); !ok || at.Unix() != e.clk.Now().Unix() {
		t.Errorf("stamp: %v %v", at, ok)
	}
	rep := e.col.Reported()
	if len(rep) != 3 || rep[0] != (ReportedRow{Host: hostA, IP: "9.9.9.9", ReportedAt: e.clk.Now().Unix()}) {
		t.Errorf("reported = %+v", rep)
	}
	// Блокировка снята.
	if _, err := os.Stat(e.up.lockDir()); !os.IsNotExist(err) {
		t.Error("upload lock must be released")
	}
	// Повторное наблюдение уже отправленной пары не попадает в очередь.
	if res := mustObserve(t, e.col, hostA, "9.9.9.9", 600); !res.AlreadyReported || res.Queued {
		t.Errorf("after upload: %+v", res)
	}
	// Пустая очередь.
	e.clk.Advance(4 * time.Hour)
	if res := e.upload(false); res.Status != UploadNoPending || e.rec.count() != 1 {
		t.Errorf("empty queue: %+v posts=%d", res, e.rec.count())
	}
}

func TestUpload_ThrottleAndForce(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	mustObserve(t, e.col, hostA, "9.9.9.9", 600)
	e.upload(false)

	mustObserve(t, e.col, hostB, "8.8.4.4", 600)
	e.clk.Advance(time.Hour)
	if res := e.upload(false); res.Status != UploadThrottled || e.rec.count() != 1 {
		t.Fatalf("within interval: %+v", res)
	}
	if len(e.col.Pending()) != 1 {
		t.Error("throttled upload must keep the queue")
	}
	// force обходит ограничение.
	if res := e.upload(true); res.Status != UploadUploaded || e.rec.count() != 2 {
		t.Fatalf("force: %+v", res)
	}
	// По истечении интервала — снова обычная отправка.
	mustObserve(t, e.col, hostC, "1.1.1.1", 600)
	e.clk.Advance(3*time.Hour + time.Second)
	if res := e.upload(false); res.Status != UploadUploaded {
		t.Fatalf("after interval: %+v", res)
	}
	// Своя длительность интервала.
	e.up.cfg.Interval = time.Minute
	mustObserve(t, e.col, "rr1---sn-d.googlevideo.com", "1.0.0.1", 600)
	e.clk.Advance(2 * time.Minute)
	if res := e.upload(false); res.Status != UploadUploaded {
		t.Errorf("1m interval: %+v", res)
	}
}

func TestUpload_FailureKeepsQueueForRetry(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	mustObserve(t, e.col, hostA, "9.9.9.9", 600)
	mustObserve(t, e.col, hostB, "8.8.4.4", 600)

	e.rec.set(500, nil)
	res, err := e.up.Upload(context.Background(), false)
	if !errors.Is(err, ErrUpload) || res.Status != UploadFailed || res.HTTPStatus != 500 || res.Count != 0 {
		t.Fatalf("HTTP 500: err=%v res=%+v", err, res)
	}
	retry := e.retryFiles()
	if len(retry) != 1 || res.Retry != filepath.Base(retry[0]) || !strings.HasPrefix(res.Retry, "pending.retry.") {
		t.Fatalf("retry files = %v (res.Retry=%s)", retry, res.Retry)
	}
	if rows := ReadPendingFile(retry[0]); len(rows) != 2 {
		t.Errorf("retry rows = %+v", rows)
	}
	if _, ok := e.up.LastUpload(); ok {
		t.Error("failed upload must not advance the timer")
	}
	if len(e.col.Reported()) != 0 {
		t.Error("nothing is reported after failure")
	}
	if len(e.col.sendingFiles()) != 0 {
		t.Error("no stray .sending files")
	}

	// Транспортная ошибка — то же самое.
	e.rec.set(0, errors.New("connection refused"))
	if res, err := e.up.Upload(context.Background(), false); !errors.Is(err, ErrUpload) || !strings.Contains(err.Error(), "connection refused") || res.Status != UploadFailed {
		t.Fatalf("transport error: %v %+v", err, res)
	}
	if len(e.retryFiles()) != 1 {
		t.Errorf("retry files = %v", e.retryFiles())
	}

	// Новые наблюдения копятся в pending.tsv, retry-файл уходит первым.
	mustObserve(t, e.col, hostC, "1.1.1.1", 600)
	e.rec.set(200, nil)
	res = e.upload(false)
	if res.Status != UploadUploaded || res.Pairs != 2 {
		t.Fatalf("retry upload: %+v", res)
	}
	if len(e.retryFiles()) != 0 {
		t.Error("retry file consumed")
	}
	if p := e.col.Pending(); len(p) != 1 || p[0].Host != hostC {
		t.Errorf("new observation must stay queued: %+v", p)
	}
	// Следующая отправка уносит остаток (force: таймер только что обновился).
	if res := e.upload(true); res.Pairs != 1 {
		t.Errorf("second batch: %+v", res)
	}
}

func TestUpload_PairLimitLeavesRemainder(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	now := e.clk.Now().Unix()
	var b strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "11.0.0.%d\t%d\trr1---sn-h%d.googlevideo.com\n", i+1, now-10, i)
	}
	writeText(t, e.col.PendingFile(), b.String())

	res := e.upload(false)
	if res.Pairs != MaxUploadPairs || res.Retry == "" {
		t.Fatalf("result = %+v", res)
	}
	if n := len(strings.Split(e.rec.last().Get("observations"), ",")); n != 24 {
		t.Errorf("pairs in form = %d", n)
	}
	left := e.col.Pending()
	if len(left) != 6 || left[0].Host != "rr1---sn-h24.googlevideo.com" {
		t.Errorf("leftover = %+v", left)
	}
	if len(e.col.Reported()) != 24 {
		t.Errorf("reported = %d", len(e.col.Reported()))
	}
	res = e.upload(true)
	if res.Pairs != 6 || res.Retry != "" || len(e.col.Pending()) != 0 {
		t.Errorf("remainder: %+v", res)
	}
}

func TestUpload_FiltersAgeReportedAndInvalid(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	now := e.clk.Now().Unix()
	week := int64(7 * 24 * 3600)
	writeText(t, e.col.PendingFile(), strings.Join([]string{
		fmt.Sprintf("9.9.9.1\t%d\t%s", now-10, hostA),        // годится
		fmt.Sprintf("9.9.9.2\t%d\t%s", now-week-5, hostB),    // слишком старое
		fmt.Sprintf("9.9.9.3\t%d\tevil.example.com", now-10), // чужой хост
		fmt.Sprintf("10.0.0.9\t%d\t%s", now-10, hostC),       // непубличный IP
		fmt.Sprintf("9.9.9.4\t%d\t%s", now-10, hostA),        // уже отправлено
		fmt.Sprintf("9.9.9.1\t%d\t%s", now-5, hostA),         // дубль пары в пакете
	}, "\n")+"\n")
	writeText(t, e.col.ReportedFile(), fmt.Sprintf("%s\t9.9.9.4\t%d\n%s\t9.9.9.77\t%d\n", hostA, now-100, hostB, now-int64(40*24*3600)))

	res := e.upload(false)
	if res.Pairs != 1 || e.rec.last().Get("observations") != hostA+"=9.9.9.1" {
		t.Fatalf("result=%+v form=%v", res, e.rec.last())
	}
	// Отправленная пара (обе строки), просроченная — убраны; прочие остаются до истечения срока.
	var left []string
	for _, p := range e.col.Pending() {
		left = append(left, p.IP)
	}
	if strings.Join(left, ",") != "9.9.9.3,10.0.0.9,9.9.9.4" {
		t.Errorf("leftover = %v", left)
	}
	// Записи reported старше 30 суток вычищены, свежие и новая — на месте.
	rep := e.col.Reported()
	if len(rep) != 2 || rep[0].IP != "9.9.9.4" || rep[1].IP != "9.9.9.1" {
		t.Errorf("reported = %+v", rep)
	}
}

func TestUpload_NoNewDiscardsQueue(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	now := e.clk.Now().Unix()
	writeText(t, e.col.PendingFile(), fmt.Sprintf("9.9.9.2\t%d\t%s\n10.0.0.1\t%d\t%s\n", now-8*24*3600, hostA, now, hostB))
	res := e.upload(false)
	if res.Status != UploadNoNew || e.rec.count() != 0 {
		t.Fatalf("result = %+v", res)
	}
	if len(e.col.Pending()) != 0 || len(e.col.sendingFiles()) != 0 {
		t.Error("snapshot must be discarded")
	}
	if _, ok := e.up.LastUpload(); ok {
		t.Error("the timer must not advance without a request")
	}
}

func TestUpload_LegacyIPRows(t *testing.T) {
	e := newUploadEnv(t, fakeCentral{"9.9.9.2": true}, nil)
	now := e.clk.Now().Unix()
	writeText(t, e.col.PendingFile(), strings.Join([]string{
		fmt.Sprintf("9.9.9.3\t%d", now-10),
		fmt.Sprintf("9.9.9.1\t%d", now-10),
		fmt.Sprintf("9.9.9.1\t%d", now-9),  // дубль
		fmt.Sprintf("9.9.9.2\t%d", now-10), // в центральном списке
		fmt.Sprintf("10.1.1.1\t%d", now-10),
		fmt.Sprintf("9.9.9.7\t%d", now-8*24*3600),
		fmt.Sprintf("9.9.9.5\t%d\t%s", now-10, hostA),
	}, "\n")+"\n")
	res := e.upload(false)
	if res.LegacyIPs != 2 || res.Pairs != 1 || res.Count != 3 {
		t.Fatalf("result = %+v", res)
	}
	form := e.rec.last()
	if form.Get("ips") != "9.9.9.1,9.9.9.3" || form.Get("observations") != hostA+"=9.9.9.5" {
		t.Errorf("form = %v", form)
	}
	// Центральный и просроченный IP из очереди убраны, непубличный остаётся (до истечения срока).
	var left []string
	for _, p := range e.col.Pending() {
		left = append(left, p.IP)
	}
	if strings.Join(left, ",") != "10.1.1.1" {
		t.Errorf("leftover = %v", left)
	}
	// Голые IP не попадают в reported.tsv (в нём только пары).
	if rep := e.col.Reported(); len(rep) != 1 || rep[0].IP != "9.9.9.5" {
		t.Errorf("reported = %+v", rep)
	}
}

func TestUpload_LegacyIPLimit(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	now := e.clk.Now().Unix()
	var b strings.Builder
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "11.0.0.%d\t%d\n", i+1, now-1)
	}
	writeText(t, e.col.PendingFile(), b.String())
	res := e.upload(false)
	if res.LegacyIPs != MaxUploadLegacyIPs {
		t.Errorf("legacy ips = %d", res.LegacyIPs)
	}
	if n := len(strings.Split(e.rec.last().Get("ips"), ",")); n != 64 {
		t.Errorf("ips in form = %d", n)
	}
	if left := e.col.Pending(); len(left) != 16 {
		t.Errorf("leftover = %d", len(left))
	}
}

func TestUpload_BusyLocks(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	mustObserve(t, e.col, hostA, "9.9.9.9", 600)

	// Свежая блокировка другого процесса.
	if err := os.MkdirAll(e.up.lockDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	// Время в тестах — фиктивные часы, поэтому mtime блокировки выставляем по ним.
	if err := os.Chtimes(e.up.lockDir(), e.clk.Now(), e.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if res := e.upload(true); res.Status != UploadBusy || e.rec.count() != 0 {
		t.Fatalf("fresh lock: %+v", res)
	}
	if _, err := os.Stat(e.up.lockDir()); err != nil {
		t.Error("foreign lock must not be removed")
	}
	// Устаревшая блокировка снимается.
	old := e.clk.Now().Add(-time.Hour)
	if err := os.Chtimes(e.up.lockDir(), old, old); err != nil {
		t.Fatal(err)
	}
	if res := e.upload(true); res.Status != UploadUploaded {
		t.Fatalf("stale lock: %+v", res)
	}
}

func TestUpload_ConcurrentCallReportsBusy(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	mustObserve(t, e.col, hostA, "9.9.9.9", 600)
	e.rec.block = make(chan struct{})
	e.rec.enter = make(chan struct{})

	done := make(chan UploadResult, 1)
	go func() {
		res, _ := e.up.Upload(context.Background(), true)
		done <- res
	}()
	<-e.rec.enter
	if res, err := e.up.Upload(context.Background(), true); err != nil || res.Status != UploadBusy {
		t.Errorf("concurrent: %v %+v", err, res)
	}
	close(e.rec.block)
	if res := <-done; res.Status != UploadUploaded {
		t.Errorf("first upload: %+v", res)
	}
}

func TestUpload_ContextCancelReturnsBatchToQueue(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	mustObserve(t, e.col, hostA, "9.9.9.9", 600)
	e.rec.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for e.rec.count() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	res, err := e.up.Upload(ctx, true)
	if !errors.Is(err, ErrUpload) || res.Status != UploadFailed {
		t.Fatalf("cancel: %v %+v", err, res)
	}
	if len(e.retryFiles()) != 1 || len(e.col.Pending()) != 1 {
		t.Error("batch must return to the retry queue")
	}
}

func TestUpload_RecoversStaleSendingSnapshot(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	now := e.clk.Now()
	stale := e.col.PendingFile() + ".sending.999.1"
	writeText(t, stale, fmt.Sprintf("9.9.9.9\t%d\t%s\n", now.Unix()-10, hostA))
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	// Свежий .sending другого воркера не трогаем.
	fresh := e.col.PendingFile() + ".sending.888.1"
	writeText(t, fresh, fmt.Sprintf("8.8.4.4\t%d\t%s\n", now.Unix()-10, hostB))
	if err := os.Chtimes(fresh, now, now); err != nil {
		t.Fatal(err)
	}

	res := e.upload(false)
	if res.Status != UploadUploaded || e.rec.last().Get("observations") != hostA+"=9.9.9.9" {
		t.Fatalf("result=%+v form=%v", res, e.rec.last())
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh .sending file must stay")
	}
}

func TestHTTPPost_RealTransport(t *testing.T) {
	var got struct {
		method, ctype string
		form          url.Values
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.ctype = r.Method, r.Header.Get("Content-Type")
		_ = r.ParseForm()
		got.form = r.PostForm
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	col, clk := newCollector(t, nil)
	mustObserve(t, col, hostA, "9.9.9.9", 600)
	up, err := NewUploader(UploaderConfig{
		Collector: col, IngestURL: srv.URL, Now: clk.Now,
		Consent: StaticConsent(true, "se cret&token", "u u"),
		Post:    HTTPPost(srv.Client()),
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := up.Upload(context.Background(), false)
	if err != nil || res.Status != UploadUploaded || res.HTTPStatus != 202 {
		t.Fatalf("%v %+v", err, res)
	}
	if got.method != http.MethodPost || !strings.HasPrefix(got.ctype, "application/x-www-form-urlencoded") {
		t.Errorf("request: %+v", got)
	}
	if got.form.Get("token") != "se cret&token" || got.form.Get("uuid") != "u u" || got.form.Get("observations") != hostA+"=9.9.9.9" {
		t.Errorf("form = %v", got.form)
	}
}

func TestHTTPPost_Errors(t *testing.T) {
	post := HTTPPost(nil)
	if _, err := post(context.Background(), "http://127.0.0.1:1/", url.Values{}); err == nil {
		t.Error("connection refused expected")
	}
	if _, err := post(context.Background(), "://bad", url.Values{}); err == nil {
		t.Error("bad URL expected")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer srv.Close()
	if st, err := post(context.Background(), srv.URL, url.Values{}); err != nil || st != 500 {
		t.Errorf("status passthrough: %d %v", st, err)
	}
}

func TestStubPost(t *testing.T) {
	col, clk := newCollector(t, nil)
	mustObserve(t, col, hostA, "9.9.9.9", 600)
	up, _ := NewUploader(UploaderConfig{
		Collector: col, Now: clk.Now, Consent: StaticConsent(true, "t", "u"), Post: StubPost(200),
		IngestURL: "https://ingest.test/upload",
	})
	if res, err := up.Upload(context.Background(), false); err != nil || res.Status != UploadUploaded {
		t.Errorf("stub accept: %v %+v", err, res)
	}
	mustObserve(t, col, hostB, "8.8.4.4", 600)
	up.cfg.Post = StubPost(403)
	if res, err := up.Upload(context.Background(), true); !errors.Is(err, ErrUpload) || res.HTTPStatus != 403 {
		t.Errorf("stub reject: %v %+v", err, res)
	}
}

func TestUpload_StampFileFormat(t *testing.T) {
	e := newUploadEnv(t, nil, nil)
	mustObserve(t, e.col, hostA, "9.9.9.9", 600)
	e.upload(false)
	b, err := os.ReadFile(e.up.cfg.StampFile)
	if err != nil || string(b) != strconv.FormatInt(e.clk.Now().Unix(), 10)+"\n" {
		t.Errorf("stamp = %q (%v)", b, err)
	}
	// Повреждённый штамп = отправки не было.
	writeText(t, e.up.cfg.StampFile, "garbage\n")
	if _, ok := e.up.LastUpload(); ok {
		t.Error("garbage stamp must be ignored")
	}
	mustObserve(t, e.col, hostB, "8.8.4.4", 600)
	if res := e.upload(false); res.Status != UploadUploaded {
		t.Errorf("garbage stamp must not throttle: %+v", res)
	}
}
