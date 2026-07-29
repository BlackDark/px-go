package pac

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	})
	if got := e.FindProxyForURL(context.Background(), "http://direct.example.com", "direct.example.com"); got != "DIRECT" {
		t.Fatalf("initial load: got %q", got)
	}

	time.Sleep(40 * time.Millisecond)

	reloadDone := make(chan struct{})
	go func() {
		_ = e.FindProxyForURL(context.Background(), "http://direct.example.com", "direct.example.com")
		close(reloadDone)
	}()

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
	select {
	case <-reloadDone:
	case <-time.After(2 * time.Second):
		t.Fatal("reload never finished")
	}
}
