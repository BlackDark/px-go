package proxy_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BlackDark/px-go/internal/config"
	"github.com/BlackDark/px-go/internal/proxy"
)

// testEnv holds a running px proxy and its dependencies.
type testEnv struct {
	backend  *httptest.Server
	px       *proxy.Server
	pxPort   int
	cancel   context.CancelFunc
	proxyURL *url.URL
}

func (e *testEnv) Close() {
	e.cancel()
	e.backend.Close()
}

func (e *testEnv) Client() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(e.proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 10 * time.Second,
	}
}

func newTestEnv(t *testing.T, port int, cfgFn func(*config.Config)) *testEnv {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Method", r.Method)
		w.Header().Set("X-Path", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "method=%s path=%s", r.Method, r.URL.Path)
	}))

	cfg := config.Default()
	cfg.Proxy.Port = port
	cfg.Proxy.Listen = []string{"127.0.0.1"}
	cfg.Settings.SockTimeout = 5 * time.Second
	cfg.Settings.Idle = 10 * time.Second
	if cfgFn != nil {
		cfgFn(&cfg)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := proxy.New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	go func() { _ = srv.Start(ctx) }()
	waitForPort(t, port)

	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	return &testEnv{
		backend:  backend,
		px:       srv,
		pxPort:   port,
		cancel:   cancel,
		proxyURL: proxyURL,
	}
}

func waitForPort(t *testing.T, port int) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("port %d did not become ready", port)
}

func TestIntegration_DirectHTTP(t *testing.T) {
	env := newTestEnv(t, 19100, nil)
	defer env.Close()

	client := env.Client()
	resp, err := client.Get(env.backend.URL + "/hello")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "path=/hello") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestIntegration_DirectHTTPS(t *testing.T) {
	// HTTPS backend
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "secure path=%s", r.URL.Path)
	}))
	defer backend.Close()

	cfg := config.Default()
	cfg.Proxy.Port = 19101
	cfg.Proxy.Listen = []string{"127.0.0.1"}
	cfg.Settings.SockTimeout = 5 * time.Second
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := proxy.New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	go func() { _ = srv.Start(ctx) }()
	waitForPort(t, 19101)

	proxyURL, _ := url.Parse("http://127.0.0.1:19101")
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 10 * time.Second,
	}
	resp, err := client.Get(backend.URL + "/secure")
	if err != nil {
		t.Fatalf("HTTPS GET failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "path=/secure") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestIntegration_HTTPMethods(t *testing.T) {
	env := newTestEnv(t, 19102, nil)
	defer env.Close()

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
	client := env.Client()
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req, _ := http.NewRequest(method, env.backend.URL+"/method-test", nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s failed: %v", method, err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}
			if !strings.Contains(string(body), "method="+method) {
				t.Fatalf("expected method=%s in body, got: %s", method, body)
			}
		})
	}
}

func TestIntegration_Noproxy(t *testing.T) {
	env := newTestEnv(t, 19103, func(cfg *config.Config) {
		cfg.Proxy.NoProxy = "127.0.0.1"
	})
	defer env.Close()

	client := env.Client()
	resp, err := client.Get(env.backend.URL + "/direct")
	if err != nil {
		t.Fatalf("noproxy request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "path=/direct") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestIntegration_HealthEndpoint(t *testing.T) {
	env := newTestEnv(t, 19104, nil)
	defer env.Close()

	// Health endpoint is accessed directly, not via proxy
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", env.pxPort))
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if string(body) != "ok" {
		t.Fatalf("expected 'ok', got %q", body)
	}
}

func TestIntegration_ClientAllow(t *testing.T) {
	env := newTestEnv(t, 19105, func(cfg *config.Config) {
		// Only allow 192.168.0.* and enable gateway so local fallback is disabled
		cfg.Proxy.Allow = "192.168.0.*"
		cfg.Proxy.Gateway = true
	})
	defer env.Close()

	client := env.Client()
	resp, err := client.Get(env.backend.URL + "/blocked")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestIntegration_UpstreamProxy(t *testing.T) {
	// Backend HTTP server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "via-upstream path=%s", r.URL.Path)
	}))
	defer backend.Close()

	// Upstream proxy (a second px instance acting as upstream, no auth)
	upstreamCfg := config.Default()
	upstreamCfg.Proxy.Port = 19110
	upstreamCfg.Proxy.Listen = []string{"127.0.0.1"}
	upstreamCfg.Settings.SockTimeout = 5 * time.Second
	upstreamLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	upstreamSrv, err := proxy.New(upstreamCfg, upstreamLogger)
	if err != nil {
		t.Fatal(err)
	}
	upstreamCtx := t.Context()
	go func() { _ = upstreamSrv.Start(upstreamCtx) }()
	waitForPort(t, 19110)

	// Main px proxy pointing to upstream
	cfg := config.Default()
	cfg.Proxy.Port = 19111
	cfg.Proxy.Listen = []string{"127.0.0.1"}
	cfg.Proxy.Server = []string{"127.0.0.1:19110"}
	cfg.Proxy.Auth = "NONE"
	cfg.Settings.SockTimeout = 5 * time.Second
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := proxy.New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	go func() { _ = srv.Start(ctx) }()
	waitForPort(t, 19111)

	// Test HTTP through chain
	proxyURL, _ := url.Parse("http://127.0.0.1:19111")
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}
	resp, err := client.Get(backend.URL + "/chained")
	if err != nil {
		t.Fatalf("chained GET failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "path=/chained") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestIntegration_UpstreamProxyCONNECT(t *testing.T) {
	// HTTPS backend
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "tunnel path=%s", r.URL.Path)
	}))
	defer backend.Close()

	// Upstream proxy
	upstreamCfg := config.Default()
	upstreamCfg.Proxy.Port = 19112
	upstreamCfg.Proxy.Listen = []string{"127.0.0.1"}
	upstreamCfg.Settings.SockTimeout = 5 * time.Second
	upstreamLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	upstreamSrv, err := proxy.New(upstreamCfg, upstreamLogger)
	if err != nil {
		t.Fatal(err)
	}
	upstreamCtx := t.Context()
	go func() { _ = upstreamSrv.Start(upstreamCtx) }()
	waitForPort(t, 19112)

	// Main px with upstream
	cfg := config.Default()
	cfg.Proxy.Port = 19113
	cfg.Proxy.Listen = []string{"127.0.0.1"}
	cfg.Proxy.Server = []string{"127.0.0.1:19112"}
	cfg.Proxy.Auth = "NONE"
	cfg.Settings.SockTimeout = 5 * time.Second
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := proxy.New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	go func() { _ = srv.Start(ctx) }()
	waitForPort(t, 19113)

	proxyURL, _ := url.Parse("http://127.0.0.1:19113")
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 10 * time.Second,
	}
	resp, err := client.Get(backend.URL + "/tunnel")
	if err != nil {
		t.Fatalf("CONNECT tunnel failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "path=/tunnel") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestIntegration_UpstreamBasicAuth(t *testing.T) {
	// Backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "authed-ok")
	}))
	defer backend.Close()

	// Upstream proxy requiring Basic auth
	upstreamCfg := config.Default()
	upstreamCfg.Proxy.Port = 19114
	upstreamCfg.Proxy.Listen = []string{"127.0.0.1"}
	upstreamCfg.Client.Auth = "BASIC"
	upstreamCfg.Client.Username = "testuser"
	upstreamCfg.Settings.SockTimeout = 5 * time.Second
	upstreamLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Set password via env for the upstream
	t.Setenv("PX_CLIENT_PASSWORD", "testpass")
	upstreamSrv, err := proxy.New(upstreamCfg, upstreamLogger)
	if err != nil {
		t.Fatal(err)
	}
	upstreamCtx := t.Context()
	go func() { _ = upstreamSrv.Start(upstreamCtx) }()
	waitForPort(t, 19114)

	// Main px configured to auth against upstream with Basic
	cfg := config.Default()
	cfg.Proxy.Port = 19115
	cfg.Proxy.Listen = []string{"127.0.0.1"}
	cfg.Proxy.Server = []string{"127.0.0.1:19114"}
	cfg.Proxy.Auth = "BASIC"
	cfg.Proxy.Username = "testuser"
	cfg.Settings.SockTimeout = 5 * time.Second
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Password via env
	t.Setenv("PX_PASSWORD", "testpass")
	srv, err := proxy.New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	go func() { _ = srv.Start(ctx) }()
	waitForPort(t, 19115)

	proxyURL, _ := url.Parse("http://127.0.0.1:19115")
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}
	resp, err := client.Get(backend.URL + "/auth-test")
	if err != nil {
		t.Fatalf("auth proxy request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d; body: %s", resp.StatusCode, body)
	}
	if string(body) != "authed-ok" {
		t.Fatalf("expected 'authed-ok', got %q", body)
	}
}

func TestIntegration_LargeBody(t *testing.T) {
	// 1MB payload
	payload := strings.Repeat("x", 1024*1024)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "size=%d", len(body))
	}))
	defer backend.Close()

	cfg := config.Default()
	cfg.Proxy.Port = 19106
	cfg.Proxy.Listen = []string{"127.0.0.1"}
	cfg.Settings.SockTimeout = 5 * time.Second
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := proxy.New(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	go func() { _ = srv.Start(ctx) }()
	waitForPort(t, 19106)

	proxyURL, _ := url.Parse("http://127.0.0.1:19106")
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   30 * time.Second,
	}
	resp, err := client.Post(backend.URL+"/upload", "application/octet-stream", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST large body failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	expected := fmt.Sprintf("size=%d", len(payload))
	if !strings.Contains(string(body), expected) {
		t.Fatalf("expected %q in body, got: %s", expected, body)
	}
}

func TestIntegration_Shutdown(t *testing.T) {
	env := newTestEnv(t, 19107, nil)

	// Verify running
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", env.pxPort))
	if err != nil {
		t.Fatalf("health before quit: %v", err)
	}
	_ = resp.Body.Close()

	// Send quit
	resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/PxQuit", env.pxPort))
	if err != nil {
		t.Fatalf("quit request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 from quit, got %d", resp.StatusCode)
	}

	// Wait for shutdown
	time.Sleep(1 * time.Second)

	// Verify not running
	_, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", env.pxPort))
	if err == nil {
		t.Fatal("expected connection refused after quit")
	}
	env.Close()
}
