package pac

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResultCacheURLHostKey(t *testing.T) {
	c := newResultCache(time.Minute, 16)
	c.put(cacheKey("http://x.com/", "x.com"), "PROXY a:1")
	c.put(cacheKey("ftp://x.com/", "x.com"), "DIRECT")

	got, ok := c.get(cacheKey("http://x.com/", "x.com"))
	if !ok || got != "PROXY a:1" {
		t.Fatalf("http key: got %q ok=%v", got, ok)
	}
	got, ok = c.get(cacheKey("ftp://x.com/", "x.com"))
	if !ok || got != "DIRECT" {
		t.Fatalf("ftp key: got %q ok=%v", got, ok)
	}
}

func TestResultCacheTTLExpiry(t *testing.T) {
	c := newResultCache(20*time.Millisecond, 16)
	c.put("k", "DIRECT")
	if _, ok := c.get("k"); !ok {
		t.Fatal("expected hit")
	}
	time.Sleep(30 * time.Millisecond)
	if _, ok := c.get("k"); ok {
		t.Fatal("expected miss after TTL")
	}
}

func TestResultCacheInvalidate(t *testing.T) {
	c := newResultCache(time.Minute, 16)
	c.put("k", "DIRECT")
	c.invalidate()
	if _, ok := c.get("k"); ok {
		t.Fatal("expected miss after invalidate")
	}
}

func TestResultCacheLRUEviction(t *testing.T) {
	c := newResultCache(time.Minute, 2)
	c.put("a", "1")
	c.put("b", "2")
	c.put("c", "3") // evicts a
	if _, ok := c.get("a"); ok {
		t.Fatal("expected a evicted")
	}
	if got, ok := c.get("b"); !ok || got != "2" {
		t.Fatalf("b: got %q ok=%v", got, ok)
	}
	if got, ok := c.get("c"); !ok || got != "3" {
		t.Fatalf("c: got %q ok=%v", got, ok)
	}
}

func TestPACCacheDistinguishesURLScheme(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.pac")
	content := `
function FindProxyForURL(url, host) {
  if (shExpMatch(url, "ftp://*")) return "DIRECT";
  return "PROXY proxy.example.com:8080";
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(path, "utf-8", time.Minute, nil)
	httpGot := e.FindProxyForURL(t.Context(), "http://x.com/a", "x.com")
	ftpGot := e.FindProxyForURL(t.Context(), "ftp://x.com/a", "x.com")
	if httpGot != "proxy.example.com:8080" {
		t.Fatalf("http: got %q", httpGot)
	}
	if ftpGot != "DIRECT" {
		t.Fatalf("ftp: got %q", ftpGot)
	}
	if e.FindProxyForURL(t.Context(), "http://x.com/a", "x.com") != httpGot {
		t.Fatal("http cache mismatch")
	}
	if e.FindProxyForURL(t.Context(), "ftp://x.com/a", "x.com") != ftpGot {
		t.Fatal("ftp cache mismatch")
	}
}
