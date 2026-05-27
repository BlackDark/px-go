package proxy_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/marbaced/migrate-px/px-go/internal/config"
	"github.com/marbaced/migrate-px/px-go/internal/proxy"
)

func TestDirectHTTPProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = fmt.Fprintf(w, "Hello from backend! Path: %s", r.URL.Path)
	}))
	defer backend.Close()

	cfg := config.Default()
	cfg.Proxy.Port = 18399
	cfg.Settings.SockTimeout = 5 * time.Second
	logger := slog.Default()
	srv, err := proxy.New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	time.Sleep(500 * time.Millisecond)

	proxyURL, _ := url.Parse("http://127.0.0.1:18399")
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}
	resp, err := client.Get(backend.URL + "/test")
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	expected := "Hello from backend! Path: /test"
	if string(body) != expected {
		t.Fatalf("expected %q, got %q", expected, string(body))
	}
	t.Logf("Success: %s", string(body))
}
