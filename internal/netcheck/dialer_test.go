package netcheck

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBindPortRangeDialer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	first, last := 62000, 62005
	dial := BindPortRangeDialer(first, last)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, err := dial(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	la, ok := c.LocalAddr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("LocalAddr = %T", c.LocalAddr())
	}
	if la.Port < first || la.Port > last {
		t.Fatalf("port %d not in %d-%d", la.Port, first, last)
	}
}
