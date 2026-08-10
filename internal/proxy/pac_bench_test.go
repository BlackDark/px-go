//go:build bench

package proxy_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlackDark/px-go/internal/config"
	"github.com/BlackDark/px-go/internal/proxy"
)

func freePort(b *testing.B) int {
	b.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func startPACProxyBench(b *testing.B, pacBody string, reload time.Duration) (proxyURL *url.URL, backendURL string, cancel context.CancelFunc) {
	b.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	b.Cleanup(backend.Close)

	pacSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, pacBody)
	}))
	b.Cleanup(pacSrv.Close)

	port := freePort(b)
	cfg := config.Default()
	cfg.Proxy.Port = port
	cfg.Proxy.Listen = []string{"127.0.0.1"}
	cfg.Proxy.PAC = pacSrv.URL
	cfg.Proxy.Server = nil
	cfg.Settings.SockTimeout = 5 * time.Second
	cfg.Settings.Idle = 10 * time.Second
	cfg.Settings.ProxyReload = reload
	cfg.Settings.Threads = 128

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := proxy.New(cfg, logger)
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()
	b.Cleanup(func() {
		cancel()
		_ = srv.Shutdown(context.Background())
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	u, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	return u, backend.URL, cancel
}

func benchClient(proxyURL *url.URL) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			MaxIdleConns:        256,
			MaxIdleConnsPerHost: 64,
			IdleConnTimeout:     30 * time.Second,
			DisableKeepAlives:   false,
		},
		Timeout: 10 * time.Second,
	}
}

func BenchmarkPACProxyE2EHit(b *testing.B) {
	pac := `function FindProxyForURL(url, host) { return "DIRECT"; }`
	proxyURL, backend, _ := startPACProxyBench(b, pac, time.Hour)
	client := benchClient(proxyURL)
	target := backend + "/hit"
	if _, err := client.Get(target); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(target)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

func BenchmarkPACProxyE2EHitParallel(b *testing.B) {
	pac := `function FindProxyForURL(url, host) { return "DIRECT"; }`
	proxyURL, backend, _ := startPACProxyBench(b, pac, time.Hour)
	client := benchClient(proxyURL)
	target := backend + "/hit"
	if _, err := client.Get(target); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := client.Get(target)
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}

func BenchmarkPACProxyE2EMissParallel(b *testing.B) {
	pac := `
function FindProxyForURL(url, host) {
  if (shExpMatch(url, "*/special/*")) return "DIRECT";
  return "DIRECT";
}`
	proxyURL, backend, _ := startPACProxyBench(b, pac, time.Hour)
	client := benchClient(proxyURL)
	var i atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := i.Add(1)
			target := backend + "/special/" + strconv.FormatInt(n, 10)
			resp, err := client.Get(target)
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}
