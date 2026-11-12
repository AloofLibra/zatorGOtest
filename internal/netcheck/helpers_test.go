package netcheck

import (
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// body100k — тело, заведомо больше лимита докачки.
var body100k = func() []byte {
	b := make([]byte, 100*1024)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}()

// serveBody отдаёт body100k с поддержкой Range (206).
func serveBody(w http.ResponseWriter, r *http.Request) {
	http.ServeContent(w, r, "f.bin", time.Time{}, bytesReader(body100k))
}

type tlsOpts struct {
	maxVersion uint16
	http2      bool
}

// newTLSServer поднимает HTTPS-сервер с самоподписанным сертификатом.
func newTLSServer(t *testing.T, h http.Handler, o tlsOpts) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	if o.maxVersion != 0 {
		srv.TLS = &tls.Config{MaxVersion: o.maxVersion}
	}
	srv.EnableHTTP2 = o.http2
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// testOpts — быстрые опции для тестов.
func testOpts() Options {
	return Options{
		InsecureSkipVerify: true,
		ConnectTimeout:     time.Second,
		MaxTime:            2 * time.Second,
		DownloadMaxTime:    3 * time.Second,
		DownloadStall:      300 * time.Millisecond,
		WaitBoth:           true,
	}
}
