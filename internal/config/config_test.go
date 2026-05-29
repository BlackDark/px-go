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

func TestLoadTOML(t *testing.T) {
	dir := t.TempDir()
	content := `
[proxy]
server = ["toml.proxy:8080"]
port = 9000
listen = ["127.0.0.1", "::1"]
gateway = false
allow = "10.0.0.0/8"
auth = "NTLM"

[client]
client_auth = "BASIC"
client_nosspi = true

[settings]
threads = 64
idle = 120
socktimeout = 15.0
log_level = "DEBUG"
`
	path := filepath.Join(dir, "px.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"--config=" + path})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Proxy.Server) != 1 || cfg.Proxy.Server[0] != "toml.proxy:8080" {
		t.Fatalf("server mismatch: %v", cfg.Proxy.Server)
	}
	if cfg.Proxy.Port != 9000 {
		t.Fatalf("port mismatch: %d", cfg.Proxy.Port)
	}
	if len(cfg.Proxy.Listen) != 2 {
		t.Fatalf("listen mismatch: %v", cfg.Proxy.Listen)
	}
	if cfg.Proxy.Allow != "10.0.0.0/8" {
		t.Fatalf("allow mismatch: %s", cfg.Proxy.Allow)
	}
	if cfg.Proxy.Auth != "NTLM" {
		t.Fatalf("auth mismatch: %s", cfg.Proxy.Auth)
	}
	if cfg.Client.Auth != "BASIC" {
		t.Fatalf("client_auth mismatch: %s", cfg.Client.Auth)
	}
	if !cfg.Client.NoSSPI {
		t.Fatal("client_nosspi should be true")
	}
	if cfg.Settings.Threads != 64 {
		t.Fatalf("threads mismatch: %d", cfg.Settings.Threads)
	}
	if cfg.Settings.Idle.Seconds() != 120 {
		t.Fatalf("idle mismatch: %v", cfg.Settings.Idle)
	}
}

func TestTOMLPrecedenceOverINI(t *testing.T) {
	dir := t.TempDir()
	oldwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	iniContent := `[proxy]
port = 1111
`
	tomlContent := `
[proxy]
port = 2222
`
	if err := os.WriteFile(filepath.Join(dir, "px.ini"), []byte(iniContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "px.toml"), []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy.Port != 2222 {
		t.Fatalf("toml should take precedence: got port %d", cfg.Proxy.Port)
	}
}

func TestSaveTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.toml")
	cfg := Default()
	cfg.Proxy.Server = []string{"save.proxy:8080"}
	cfg.Proxy.Port = 4321
	cfg.Proxy.Auth = "NTLM"
	cfg.Settings.Threads = 32
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	// Reload and verify round-trip.
	loaded, err := Load([]string{"--config=" + path})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Proxy.Port != 4321 {
		t.Fatalf("round-trip port: %d", loaded.Proxy.Port)
	}
	if loaded.Proxy.Auth != "NTLM" {
		t.Fatalf("round-trip auth: %s", loaded.Proxy.Auth)
	}
	if loaded.Settings.Threads != 32 {
		t.Fatalf("round-trip threads: %d", loaded.Settings.Threads)
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
