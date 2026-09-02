package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/BlackDark/px-go/internal/config"
	"github.com/BlackDark/px-go/internal/platform"
)

type stubPlatform struct {
	pac string
}

func (s stubPlatform) LoadProxyInfo(context.Context, string) (platform.ProxyInfo, error) {
	return platform.ProxyInfo{PAC: s.pac}, nil
}
func (stubPlatform) Install(string) error { return nil }
func (stubPlatform) Uninstall() error     { return nil }
func (stubPlatform) AttachConsole() error { return nil }
func (stubPlatform) DetachConsole() error { return nil }

func TestPlatformPACInitRace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.pac")
	content := `function FindProxyForURL(url, host) { return "DIRECT"; }`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Proxy.PAC = ""
	cfg.Proxy.Server = nil
	srv, err := New(cfg, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	srv.platform = stubPlatform{pac: path}

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for range 32 {
		wg.Go(func() {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/", nil)
			if err != nil {
				errCh <- err
				return
			}
			if _, err := srv.resolveRoute(context.Background(), req); err != nil {
				errCh <- err
			}
		})
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if srv.getPAC() == nil {
		t.Fatal("expected platform PAC to be installed")
	}
}
