package pac

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

const simplePAC = `
function FindProxyForURL(url, host) {
  if (host == "direct.example.com") return "DIRECT";
  if (host == "proxy.example.com") return "PROXY proxy1.com:8080";
  if (host == "multi.example.com") return "PROXY proxy1.com:8080; PROXY proxy2.com:3128; DIRECT";
  if (host == "socks.example.com") return "SOCKS5 socks.com:1080";
  return "DIRECT";
}`

func waitNotReloading(t *testing.T, e *Evaluator) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		reloading := e.reloading
		e.mu.Unlock()
		if !reloading {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("still reloading")
}

func TestPACLoadAndEvaluate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.pac")
	if err := os.WriteFile(path, []byte(simplePAC), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(path, "utf-8", time.Minute, slog.Default())
	if got := e.FindProxyForURL(context.Background(), "http://direct.example.com", "direct.example.com"); got != "DIRECT" {
		t.Fatalf("unexpected direct result %q", got)
	}
	if got := e.FindProxyForURL(context.Background(), "http://proxy.example.com", "proxy.example.com"); got != "proxy1.com:8080" {
		t.Fatalf("unexpected proxy result %q", got)
	}
	if got := e.FindProxyForURL(context.Background(), "http://multi.example.com", "multi.example.com"); got != "proxy1.com:8080,proxy2.com:3128,DIRECT" {
		t.Fatalf("unexpected multi result %q", got)
	}
	if got := e.FindProxyForURL(context.Background(), "http://socks.example.com", "socks.example.com"); got != "socks5://socks.com:1080" {
		t.Fatalf("unexpected socks result %q", got)
	}
}

func TestPACInvalidFallsBackToDirect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.pac")
	if err := os.WriteFile(path, []byte("this is not javascript"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(path, "utf-8", time.Minute, slog.Default())
	if got := e.FindProxyForURL(context.Background(), "http://example.com", "example.com"); got != "DIRECT" {
		t.Fatalf("unexpected result %q", got)
	}
}

func TestPACEvalNotBlockedByReloadFetch(t *testing.T) {
	var requests atomic.Int32
	blockSecond := make(chan struct{}, 1)
	releaseSecond := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n >= 2 {
			select {
			case blockSecond <- struct{}{}:
			default:
			}
			<-releaseSecond
		}
		_, _ = w.Write([]byte(simplePAC))
	}))
	defer srv.Close()

	e := New(srv.URL, "utf-8", 30*time.Millisecond, slog.Default())
	t.Cleanup(func() {
		select {
		case <-releaseSecond:
		default:
			close(releaseSecond)
		}
		waitNotReloading(t, e)
	})
	if got := e.FindProxyForURL(context.Background(), "http://direct.example.com", "direct.example.com"); got != "DIRECT" {
		t.Fatalf("initial load: got %q", got)
	}

	time.Sleep(40 * time.Millisecond)

	// Cache hit kicks async soft reload; must not block this caller.
	_ = e.FindProxyForURL(context.Background(), "http://direct.example.com", "direct.example.com")

	select {
	case <-blockSecond:
	case <-time.After(2 * time.Second):
		t.Fatal("reload fetch never started")
	}

	done := make(chan string, 1)
	go func() {
		done <- e.FindProxyForURL(context.Background(), "http://proxy.example.com", "proxy.example.com")
	}()

	select {
	case got := <-done:
		if got != "proxy1.com:8080" {
			t.Fatalf("concurrent eval: got %q", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("eval blocked by in-flight reload fetch")
	}

	close(releaseSecond)
	waitNotReloading(t, e)
}

func TestPACNoDeadlockOnPoolSwapWhileSaturated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.pac")
	if err := os.WriteFile(path, []byte(simplePAC), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(path, "utf-8", time.Minute, slog.Default())
	e.poolSize = 1
	if got := e.FindProxyForURL(context.Background(), "http://direct.example.com/", "direct.example.com"); got != "DIRECT" {
		t.Fatalf("warmup: %q", got)
	}

	hold := make(chan struct{})
	held := make(chan struct{})
	e.onSlotHeld = func() {
		close(held)
		<-hold
	}

	holderDone := make(chan string, 1)
	go func() {
		holderDone <- e.FindProxyForURL(context.Background(), "http://proxy.example.com/hold", "proxy.example.com")
	}()
	select {
	case <-held:
	case <-time.After(2 * time.Second):
		t.Fatal("holder never checked out slot")
	}

	waiterSeeing := make(chan struct{})
	e.onBeforeCheckout = func(p *runtimePool) {
		close(waiterSeeing)
	}
	waiterDone := make(chan string, 1)
	go func() {
		waiterDone <- e.FindProxyForURL(context.Background(), "http://proxy.example.com/wait", "proxy.example.com")
	}()
	select {
	case <-waiterSeeing:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter never reached checkout")
	}
	// Let waiter enter <-p.slots before we swap the active pool.
	for range 100 {
		runtime.Gosched()
	}

	newPool, err := e.buildPool(simplePAC)
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.cache.invalidate()
	newPool.gen = e.cache.generation()
	e.pool.Store(newPool)
	e.mu.Unlock()

	e.onBeforeCheckout = nil
	e.onSlotHeld = nil
	close(hold)

	select {
	case got := <-holderDone:
		if got != "proxy1.com:8080" {
			t.Fatalf("holder: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("holder stuck")
	}
	select {
	case got := <-waiterDone:
		if got != "proxy1.com:8080" {
			t.Fatalf("waiter: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter deadlocked on discarded pool slot")
	}
}

func TestPACCacheNotPoisonedByStaleEval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.pac")
	v1 := `function FindProxyForURL(url, host) { return "PROXY old.example.com:1"; }`
	v2 := `function FindProxyForURL(url, host) { return "PROXY new.example.com:2"; }`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(path, "utf-8", time.Minute, slog.Default())
	e.poolSize = 1

	hold := make(chan struct{})
	held := make(chan struct{})
	e.onSlotHeld = func() {
		close(held)
		<-hold
	}

	staleDone := make(chan string, 1)
	go func() {
		staleDone <- e.FindProxyForURL(context.Background(), "http://x.com/q", "x.com")
	}()
	select {
	case <-held:
	case <-time.After(2 * time.Second):
		t.Fatal("stale eval never started")
	}

	if err := os.WriteFile(path, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.loadAndSwap(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.onSlotHeld = nil
	close(hold)

	stale := <-staleDone
	if stale != "old.example.com:1" {
		t.Fatalf("stale eval: %q", stale)
	}
	got := e.FindProxyForURL(context.Background(), "http://x.com/q", "x.com")
	if got != "new.example.com:2" {
		t.Fatalf("cache poisoned with stale result: got %q", got)
	}
}

func TestPACLoadIgnoresCanceledRequestContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(simplePAC))
	}))
	defer srv.Close()

	e := New(srv.URL, "utf-8", time.Minute, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := e.FindProxyForURL(ctx, "http://direct.example.com/", "direct.example.com")
	if got != "DIRECT" {
		t.Fatalf("canceled ctx should still load PAC: got %q", got)
	}
}

func TestPACFailedReloadDoesNotHammer(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			_, _ = w.Write([]byte(simplePAC))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	e := New(srv.URL, "utf-8", time.Hour, slog.Default())
	if got := e.FindProxyForURL(context.Background(), "http://direct.example.com/", "direct.example.com"); got != "DIRECT" {
		t.Fatalf("initial: %q", got)
	}

	e.mu.Lock()
	e.lastLoad = time.Now().Add(-2 * time.Hour)
	e.mu.Unlock()

	_ = e.FindProxyForURL(context.Background(), "http://direct.example.com/r1", "direct.example.com")
	waitNotReloading(t, e)
	afterFail := requests.Load()
	_ = e.FindProxyForURL(context.Background(), "http://direct.example.com/r2", "direct.example.com")
	if requests.Load() != afterFail {
		t.Fatalf("failed reload hammered source: before=%d after=%d", afterFail, requests.Load())
	}
	if got := e.FindProxyForURL(context.Background(), "http://proxy.example.com/x", "proxy.example.com"); got != "proxy1.com:8080" {
		t.Fatalf("old pool should remain after failed reload: %q", got)
	}
}

func TestPACHTTPNonOKRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`function FindProxyForURL(url, host) { return "PROXY evil:1"; }`))
	}))
	defer srv.Close()

	e := New(srv.URL, "utf-8", time.Minute, slog.Default())
	if got := e.FindProxyForURL(context.Background(), "http://x.com/", "x.com"); got != "DIRECT" {
		t.Fatalf("non-OK PAC fetch should stay DIRECT: got %q", got)
	}
	if e.pool.Load() != nil {
		t.Fatal("non-OK fetch must not install a pool")
	}
}

func TestPACCloseDropsInFlightSwap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.pac")
	if err := os.WriteFile(path, []byte(simplePAC), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(path, "utf-8", time.Minute, slog.Default())
	if got := e.FindProxyForURL(context.Background(), "http://direct.example.com/", "direct.example.com"); got != "DIRECT" {
		t.Fatalf("warmup: %q", got)
	}
	e.Close()
	if err := e.loadAndSwap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.pool.Load() != nil {
		t.Fatal("Close must prevent loadAndSwap from resurrecting pool")
	}
	if got := e.FindProxyForURL(context.Background(), "http://proxy.example.com/", "proxy.example.com"); got != "DIRECT" {
		t.Fatalf("after Close expected DIRECT, got %q", got)
	}
}
