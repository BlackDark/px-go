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
