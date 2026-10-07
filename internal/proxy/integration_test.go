package proxy_test

import (
	"bufio"
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
	"sync"
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

// startAuthChain starts two chained px instances: an upstream px that demands
// Basic auth from its clients, and a front px that authenticates against it.
// Returns the front proxy URL.
func startAuthChain(t *testing.T, upstreamPort, frontPort int) *url.URL {
	t.Helper()
	upstreamCfg := config.Default()
	upstreamCfg.Proxy.Port = upstreamPort
	upstreamCfg.Proxy.Listen = []string{"127.0.0.1"}
	upstreamCfg.Client.Auth = "BASIC"
	upstreamCfg.Client.Username = "testuser"
	upstreamCfg.Settings.SockTimeout = 5 * time.Second
	t.Setenv("PX_CLIENT_PASSWORD", "testpass")
	upstreamSrv, err := proxy.New(upstreamCfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	// Start blocks and reports bind failures, so a taken port must fail the test
	// instead of leaving waitForPort probing a foreign listener.
	upstreamErr := make(chan error, 1)
	go func() { upstreamErr <- upstreamSrv.Start(t.Context()) }()
	waitForPort(t, upstreamPort)
	failOnBindError(t, upstreamErr, upstreamPort)

	cfg := config.Default()
	cfg.Proxy.Port = frontPort
	cfg.Proxy.Listen = []string{"127.0.0.1"}
	cfg.Proxy.Server = []string{fmt.Sprintf("127.0.0.1:%d", upstreamPort)}
	cfg.Proxy.Auth = "BASIC"
	cfg.Proxy.Username = "testuser"
	cfg.Settings.SockTimeout = 5 * time.Second
	t.Setenv("PX_PASSWORD", "testpass")
	srv, err := proxy.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	frontErr := make(chan error, 1)
	go func() { frontErr <- srv.Start(t.Context()) }()
	waitForPort(t, frontPort)
	failOnBindError(t, frontErr, frontPort)

	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", frontPort))
	return proxyURL
}

func failOnBindError(t *testing.T, startErr <-chan error, port int) {
	t.Helper()
	select {
	case err := <-startErr:
		t.Fatalf("px failed to start on port %d: %v", port, err)
	default:
	}
}

func TestIntegration_UpstreamBasicAuth(t *testing.T) {
	// Backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "authed-ok")
	}))
	defer backend.Close()

	proxyURL := startAuthChain(t, 19114, 19115)
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

// Clients that default to Connection: close (BusyBox wget, HTTP/1.0 tools) must
// still get the real upstream response; the close is hop-by-hop and must not
// leak into the upstream connection that px authenticates and retries on.
func TestIntegration_ClientConnectionClose(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "close-ok")
	}))
	defer backend.Close()
	startAuthChain(t, 19116, 19117)

	cases := []struct {
		name string
		req  string
	}{
		{"keep-alive default", "GET http://%s/ HTTP/1.1\r\nHost: %s\r\n\r\n"},
		{"connection close", "GET http://%s/ HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n"},
		{"http/1.0 default close", "GET http://%s/ HTTP/1.0\r\nHost: %s\r\n\r\n"},
		{"http/1.0 keep-alive", "GET http://%s/ HTTP/1.0\r\nHost: %s\r\nConnection: keep-alive\r\n\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := strings.TrimPrefix(backend.URL, "http://")
			conn, err := net.DialTimeout("tcp", "127.0.0.1:19117", 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(conn, fmt.Sprintf(tc.req, target, target)); err != nil {
				t.Fatal(err)
			}
			raw, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			defer func() { _ = raw.Body.Close() }()
			if raw.StatusCode != http.StatusOK {
				t.Fatalf("expected 200 OK, got: %s", raw.Status)
			}
			body, err := io.ReadAll(raw.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if !strings.Contains(string(body), "close-ok") {
				t.Fatalf("expected upstream body, got: %s", body)
			}
		})
	}
}

// Direct route has no auth retry to break, so the observable effect of leaking
// the client's close upstream is a new origin connection per request. Two
// sequential Connection: close requests must still share one origin connection.
func TestIntegration_DirectConnectionCloseReusesOriginConn(t *testing.T) {
	var mu sync.Mutex
	var origins []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		origins = append(origins, r.RemoteAddr)
		mu.Unlock()
		_, _ = io.WriteString(w, "reuse-ok")
	}))
	defer backend.Close()
	env := newTestEnv(t, 19118, nil)
	defer env.Close()

	target := strings.TrimPrefix(backend.URL, "http://")
	for range 2 {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", env.pxPort), 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		_, werr := io.WriteString(conn,
			fmt.Sprintf("GET http://%s/ HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target, target))
		raw, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if werr != nil {
			t.Fatal(werr)
		}
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		if raw.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK, got: %s", raw.Status)
		}
		_ = raw.Body.Close()
		_ = conn.Close()
	}

	mu.Lock()
	defer mu.Unlock()
	if len(origins) != 2 {
		t.Fatalf("expected 2 origin requests, got %d", len(origins))
	}
	if origins[0] != origins[1] {
		t.Fatalf("client close leaked upstream: origin conns %v", origins)
	}
}

// An upstream proxy is allowed to close the socket with its 407 challenge.
// px must redial instead of replaying the authenticated request into a dead
// connection, which surfaces as 502 unexpected EOF.
func TestIntegration_UpstreamClosesAfterChallenge(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				br := bufio.NewReader(c)
				authorized := false
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if strings.HasPrefix(strings.ToLower(line), "proxy-authorization:") {
						authorized = true
					}
					if strings.TrimSpace(line) != "" {
						continue
					}
					if authorized {
						_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 9\r\n\r\nredial-ok")
						return
					}
					_, _ = io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\n"+
						"Proxy-Authenticate: Basic realm=\"CORP\"\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
					return
				}
			}(c)
		}
	}()

	cfg := config.Default()
	cfg.Proxy.Port = 19119
	cfg.Proxy.Listen = []string{"127.0.0.1"}
	cfg.Proxy.Server = []string{ln.Addr().String()}
	cfg.Proxy.Auth = "BASIC"
	cfg.Proxy.Username = "testuser"
	cfg.Settings.SockTimeout = 5 * time.Second
	t.Setenv("PX_PASSWORD", "testpass")
	srv, err := proxy.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	startErr := make(chan error, 1)
	go func() { startErr <- srv.Start(t.Context()) }()
	waitForPort(t, 19119)
	failOnBindError(t, startErr, 19119)

	proxyURL, _ := url.Parse("http://127.0.0.1:19119")
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}
	resp, err := client.Get("http://example.invalid/resource")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %s: %s", resp.Status, body)
	}
	if string(body) != "redial-ok" {
		t.Fatalf("unexpected body %q", body)
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
