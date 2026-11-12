// Package fetch — загрузка файлов с повторами, зеркалами и проверкой sha256,
// а также безопасная распаковка tar.gz.
package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultRetries — число попыток на один URL (как в z2r_fetch_url_to_file).
	DefaultRetries = 3
	// DefaultConnectTimeout — таймаут установления соединения.
	DefaultConnectTimeout = 10 * time.Second
	// DefaultRetryDelay — пауза между повторами.
	DefaultRetryDelay = 2 * time.Second
	// DefaultUserAgent — User-Agent по умолчанию.
	DefaultUserAgent = "zator-fetch/1"
)

// ErrChecksum — контрольная сумма скачанного файла не совпала.
var ErrChecksum = errors.New("fetch: несовпадение sha256")

// HTTPError — ответ сервера с неуспешным статусом.
type HTTPError struct {
	URL    string
	Status int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("fetch: %s: HTTP %d", e.URL, e.Status)
}

// Temporary сообщает, имеет ли смысл повторять запрос.
func (e *HTTPError) Temporary() bool {
	return e.Status >= 500 || e.Status == http.StatusRequestTimeout || e.Status == http.StatusTooManyRequests
}

// Client — HTTP-клиент с повторами и списком зеркал.
type Client struct {
	// HTTP — используемый клиент. По умолчанию строится NewClient.
	HTTP *http.Client
	// Retries — число попыток на каждый URL (минимум 1).
	Retries int
	// RetryDelay — пауза между попытками.
	RetryDelay time.Duration
	// UserAgent — значение заголовка User-Agent.
	UserAgent string
	// Mirrors — зеркала; для каждого URL после оригинала пробуются
	// переписанные адреса в порядке следования.
	Mirrors []Mirror
}

// NewClient создаёт клиент с таймаутом соединения 10 с и тремя попытками.
func NewClient(mirrors ...Mirror) *Client {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   DefaultConnectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   DefaultConnectTimeout,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
	return &Client{
		HTTP:       &http.Client{Transport: tr},
		Retries:    DefaultRetries,
		RetryDelay: DefaultRetryDelay,
		UserAgent:  DefaultUserAgent,
		Mirrors:    mirrors,
	}
}

// Candidates возвращает список адресов для попыток: оригинал и зеркала
// (без дубликатов).
func (c *Client) Candidates(url string) []string {
	out := []string{url}
	seen := map[string]bool{url: true}
	for _, m := range c.Mirrors {
		if m == nil {
			continue
		}
		if u, ok := m(url); ok && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// Download скачивает url в dest. Если wantSHA256 не пуст, проверяет
// контрольную сумму (hex, регистр не важен). Файл появляется в dest только
// после успешной загрузки и проверки.
func (c *Client) Download(ctx context.Context, url, dest, wantSHA256 string) error {
	return c.DownloadAny(ctx, c.Candidates(url), dest, wantSHA256)
}

// DownloadAny пробует адреса по очереди (каждый — до Retries раз).
func (c *Client) DownloadAny(ctx context.Context, urls []string, dest, wantSHA256 string) error {
	if len(urls) == 0 {
		return errors.New("fetch: пустой список URL")
	}
	var errs []error
	for _, u := range urls {
		err := c.downloadWithRetries(ctx, u, dest, wantSHA256)
		if err == nil {
			return nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return errors.Join(errs...)
}

func (c *Client) downloadWithRetries(ctx context.Context, url, dest, want string) error {
	tries := c.Retries
	if tries < 1 {
		tries = 1
	}
	var err error
	for attempt := 1; attempt <= tries; attempt++ {
		err = c.downloadOnce(ctx, url, dest, want)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil || !retryable(err) || attempt == tries {
			break
		}
		if !sleep(ctx, c.RetryDelay) {
			break
		}
	}
	return err
}

func retryable(err error) bool {
	if errors.Is(err, ErrChecksum) {
		// Битая доставка (перехват/обрыв) — повтор имеет смысл.
		return true
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Temporary()
	}
	return true
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (c *Client) downloadOnce(ctx context.Context, url, dest, want string) (err error) {
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	ua := c.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	req.Header.Set("User-Agent", ua)

	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("fetch: %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{URL: url, Status: resp.StatusCode}
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("fetch: %s: %w", url, err)
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return fmt.Errorf("fetch: %s: получено %d байт из %d", url, n, resp.ContentLength)
	}
	if want = strings.TrimSpace(want); want != "" {
		got := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(got, want) {
			return fmt.Errorf("%w: %s: ожидалось %s, получено %s", ErrChecksum, url, strings.ToLower(want), got)
		}
	}
	// На Windows Rename не перезаписывает существующий файл.
	_ = os.Remove(dest)
	if err = os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	return nil
}

// Download — вариант без зеркал и с настройками по умолчанию.
func Download(ctx context.Context, url, dest, wantSHA256 string) error {
	return NewClient().Download(ctx, url, dest, wantSHA256)
}

// SHA256File возвращает hex sha256 файла.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
