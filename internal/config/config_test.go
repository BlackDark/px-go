package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	oldwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	ini := `[proxy]
server = ini.proxy:8080
port = 8080
listen = 127.0.0.1
[client]
client_auth = BASIC
[settings]
threads = 8
`
	if err := os.WriteFile(filepath.Join(dir, "px.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PX_SERVER", "env.proxy:8888")
	t.Setenv("PX_THREADS", "12")
	cfg, err := Load([]string{"--server=cli.proxy:9999", "--port=9090"})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Proxy.Server[0]; got != "cli.proxy:9999" {
		t.Fatalf("server precedence mismatch: %s", got)
	}
	if cfg.Proxy.Port != 9090 {
		t.Fatalf("port precedence mismatch: %d", cfg.Proxy.Port)
	}
	if cfg.Settings.Threads != 12 {
		t.Fatalf("env precedence mismatch: %d", cfg.Settings.Threads)
	}
	if cfg.Client.Auth != "BASIC" {
		t.Fatalf("ini not applied: %s", cfg.Client.Auth)
	}
}

func TestFileURLToLocalPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		got := FileURLToLocalPath("file:///C:/Users/test/proxy.pac")
		if got != `C:/Users/test/proxy.pac` && got != `C:\Users\test\proxy.pac` {
			t.Fatalf("unexpected path %q", got)
		}
		return
	}
	got := FileURLToLocalPath("file:///etc/proxy.pac")
	if got != "/etc/proxy.pac" {
		t.Fatalf("unexpected path %q", got)
	}
}

func TestGetHostIPsContainsLoopback(t *testing.T) {
	ips := GetHostIPs()
	found := false
	for _, ip := range ips {
		if ip.String() == "127.0.0.1" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected loopback address")
	}
}

func TestResolvePACPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "px.ini")
	pacPath := filepath.Join(dir, "corp.pac")
	if err := os.WriteFile(configPath, []byte("[proxy]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		pac    string
		config string
		want   string
	}{
		{"http url", "http://example.com/wpad.dat", configPath, "http://example.com/wpad.dat"},
		{"file url", "file:///etc/proxy.pac", configPath, "file:///etc/proxy.pac"},
		{"absolute", pacPath, configPath, pacPath},
		{"relative to config", "corp.pac", configPath, pacPath},
		{"empty", "", configPath, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolvePACPath(tc.pac, tc.config); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestPACRelativePathLoad(t *testing.T) {
	dir := t.TempDir()
	pacContent := `function FindProxyForURL(url, host) { return "DIRECT"; }`
	if err := os.WriteFile(filepath.Join(dir, "local.pac"), []byte(pacContent), 0o644); err != nil {
		t.Fatal(err)
	}
	ini := `[proxy]
pac = local.pac
`
	configPath := filepath.Join(dir, "px.ini")
	if err := os.WriteFile(configPath, []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"--config=" + configPath})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "local.pac")
	if cfg.Proxy.PAC != want {
		t.Fatalf("pac path: got %q want %q", cfg.Proxy.PAC, want)
	}
}

func TestLogFileSetting(t *testing.T) {
	dir := t.TempDir()
	ini := `[settings]
log = 1
log_file = /var/log/px-go/test.log
`
	path := filepath.Join(dir, "px.ini")
	if err := os.WriteFile(path, []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"--config=" + path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.LogFile != "/var/log/px-go/test.log" {
		t.Fatalf("log_file: got %q", cfg.Settings.LogFile)
	}
	if got := cfg.resolveLogPath(); got != "/var/log/px-go/test.log" {
		t.Fatalf("resolveLogPath: got %q", got)
	}
}

// "proxy" and "server" both assign Proxy.Server. Map iteration order must not
// decide which wins, otherwise the same argv routes to different upstreams.
func TestApplyValuesProxyServerPrecedenceIsDeterministic(t *testing.T) {
	const want = "B:2"
	for range 100 {
		cfg := Default()
		if err := applyValues(&cfg, map[string]string{"proxy": "A:1", "server": want}); err != nil {
			t.Fatal(err)
		}
		if len(cfg.Proxy.Server) != 1 || cfg.Proxy.Server[0] != want {
			t.Fatalf("Proxy.Server = %v, want [%s]", cfg.Proxy.Server, want)
		}
	}
}

// PX_CONFIG selects the config file when --config is absent, and the path
// recorded on Special.ConfigPath must be the file that was actually loaded.
func TestLoadHonorsPxConfig(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	discoverDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(discoverDir, "px.ini"),
		[]byte("[proxy]\nport = 1111\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(discoverDir); err != nil {
		t.Fatal(err)
	}

	site := filepath.Join(t.TempDir(), "site.ini")
	if err := os.WriteFile(site, []byte("[proxy]\nport = 2222\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PX_CONFIG", site)

	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.Port != 2222 {
		t.Fatalf("PX_CONFIG file not loaded: port = %d, want 2222", cfg.Proxy.Port)
	}
	if cfg.Special.ConfigPath != site {
		t.Fatalf("recorded config path = %q, want %q", cfg.Special.ConfigPath, site)
	}
}

// A PX_CONFIG that does not exist must not break startup; it falls back to
// discovery, which is how the variable behaved while it was ignored.
func TestLoadIgnoresMissingPxConfig(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	discoverDir := t.TempDir()
	want := filepath.Join(discoverDir, "px.ini")
	if err := os.WriteFile(want, []byte("[proxy]\nport = 3333\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(discoverDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PX_CONFIG", filepath.Join(discoverDir, "nope.ini"))

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("missing PX_CONFIG must not fail startup: %v", err)
	}
	if cfg.Proxy.Port != 3333 {
		t.Fatalf("port = %d, want 3333 from discovery", cfg.Proxy.Port)
	}
}
