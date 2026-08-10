//go:build bench

package pac_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlackDark/px-go/internal/pac"
)

const benchPAC = `
function FindProxyForURL(url, host) {
  if (shExpMatch(url, "ftp://*")) return "DIRECT";
  if (dnsDomainIs(host, ".direct.example")) return "DIRECT";
  if (shExpMatch(url, "*/special/*")) return "PROXY special.example.com:8080";
  return "PROXY default.example.com:8080";
}
`

func newBenchEvaluator(b *testing.B, reload time.Duration) (*pac.Evaluator, *httptest.Server) {
	b.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, benchPAC)
	}))
	b.Cleanup(srv.Close)
	e := pac.New(srv.URL, "utf-8", reload, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(e.Close)
	// Warm load
	_ = e.FindProxyForURL(context.Background(), "http://warm.example/", "warm.example")
	return e, srv
}

func BenchmarkFindProxyCacheHit(b *testing.B) {
	e, _ := newBenchEvaluator(b, time.Hour)
	ctx := context.Background()
	url := "http://app.example/path"
	host := "app.example"
	_ = e.FindProxyForURL(ctx, url, host) // seed cache
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := e.FindProxyForURL(ctx, url, host); got == "" {
			b.Fatal("empty")
		}
	}
}

func BenchmarkFindProxyCacheHitParallel(b *testing.B) {
	e, _ := newBenchEvaluator(b, time.Hour)
	ctx := context.Background()
	url := "http://app.example/path"
	host := "app.example"
	_ = e.FindProxyForURL(ctx, url, host)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = e.FindProxyForURL(ctx, url, host)
		}
	})
}

func BenchmarkFindProxyCacheMiss(b *testing.B) {
	e, _ := newBenchEvaluator(b, time.Hour)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		u := "http://app.example/q?i=" + strconv.Itoa(i)
		_ = e.FindProxyForURL(ctx, u, "app.example")
	}
}

func BenchmarkFindProxyCacheMissParallel(b *testing.B) {
	e, _ := newBenchEvaluator(b, time.Hour)
	ctx := context.Background()
	var i atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := i.Add(1)
			u := fmt.Sprintf("http://app.example/q?i=%d", n)
			_ = e.FindProxyForURL(ctx, u, "app.example")
		}
	})
}

func BenchmarkFindProxyDuringReload(b *testing.B) {
	// Short reload + slow second PAC response → soft reload must not serialize evals.
	block := make(chan struct{})
	release := make(chan struct{})
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) >= 2 {
			select {
			case block <- struct{}{}:
			default:
			}
			<-release
		}
		_, _ = io.WriteString(w, benchPAC)
	}))
	b.Cleanup(func() {
		close(release)
		srv.Close()
	})
	e := pac.New(srv.URL, "utf-8", 20*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(e.Close)
	_ = e.FindProxyForURL(context.Background(), "http://warm.example/", "warm.example")
	time.Sleep(30 * time.Millisecond)
	// Kick reload in the background so this works on both sync (main) and async (branch) loaders.
	go func() {
		_ = e.FindProxyForURL(context.Background(), "http://warm.example/reload", "warm.example")
	}()
	select {
	case <-block:
	case <-time.After(2 * time.Second):
		b.Fatal("reload never blocked")
	}

	ctx := context.Background()
	var i atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := i.Add(1)
			_ = e.FindProxyForURL(ctx, fmt.Sprintf("http://app.example/r?i=%d", n), "app.example")
		}
	})
	b.StopTimer()
}
