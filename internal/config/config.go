package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	ServiceName       = "Px"
	ClientServiceName = "PxClient"
)

type Proxy struct {
	Server      []string
	PAC         string
	PACEncoding string
	Listen      []string
	Port        int
	Gateway     bool
	HostOnly    bool
	Allow       string
	NoProxy     string
	UserAgent   string
	Username    string
	Auth        string
	Kerberos    bool
}

type Client struct {
	Username string
	Auth     string
	NoSSPI   bool
}

type Settings struct {
	Workers     int
	Threads     int
	Idle        time.Duration
	SockTimeout time.Duration
	ProxyReload time.Duration
	Foreground  bool
	Quiet       bool
	Log         int
	LogFile     string
	LogLevel    slog.Level
}

type Special struct {
	ConfigPath     string
	Quit           bool
	Restart        bool
	TestURL        string
	Install        bool
	Uninstall      bool
	Save           bool
	Version        bool
	HealthCheck    bool
	Password       bool
	ClientPassword bool
}

type Config struct {
	Proxy    Proxy
	Client   Client
	Settings Settings
	Special  Special
}

func Default() Config {
	return Config{
		Proxy: Proxy{
			Listen:      []string{"127.0.0.1"},
			Port:        3128,
			Allow:       "*.*.*.*",
			PACEncoding: "utf-8",
			Auth:        "ANY",
		},
		Client: Client{
			Auth: "NONE",
		},
		Settings: Settings{
			Workers:     1,
			Threads:     128,
			Idle:        300 * time.Second,
			SockTimeout: 20 * time.Second,
			ProxyReload: 60 * time.Second,
			LogLevel:    slog.LevelInfo,
		},
	}
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseLevel(raw string) (slog.Level, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "DEBUG":
		return slog.LevelDebug, nil
	case "INFO", "":
		return slog.LevelInfo, nil
	case "WARN", "WARNING":
		return slog.LevelWarn, nil
	case "ERROR":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level %q", raw)
	}
}

func normalizeCSV(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func valueOrEmpty(values map[string]string, key string) string {
	if v, ok := values[key]; ok {
		return v
	}
	return ""
}

func scriptDir() string {
	exe, err := os.Executable()
	if err != nil {
		return mustGetwd()
	}
	return filepath.Dir(exe)
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func ConfigDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(base, "px")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "px")
	default:
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, "px")
	}
}

func FileURLToLocalPath(fileURL string) string {
	parsed, err := url.Parse(fileURL)
	if err != nil {
		return fileURL
	}
	path := parsed.Path
	if runtime.GOOS == "windows" {
		if parsed.Host != "" && len(parsed.Host) == 2 && parsed.Host[1] == ':' {
			return parsed.Host + path
		}
		if strings.HasPrefix(path, "/") && len(path) > 2 && path[2] == ':' {
			return path[1:]
		}
	}
	return path
}

func GetHostIPs() []netip.Addr {
	out := []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	seen := map[string]struct{}{"127.0.0.1": {}}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if (iface.Flags & net.FlagUp) == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr().Unmap()
			if !ip.Is4() {
				continue
			}
			if _, ok := seen[ip.String()]; ok {
				continue
			}
			seen[ip.String()] = struct{}{}
			out = append(out, ip)
		}
	}
	return out
}

func (c Config) ListenAddresses() []string {
	if c.Proxy.Gateway || c.Proxy.HostOnly {
		return []string{fmt.Sprintf(":%d", c.Proxy.Port)}
	}
	addrs := make([]string, 0, len(c.Proxy.Listen))
	for _, listen := range c.Proxy.Listen {
		addrs = append(addrs, net.JoinHostPort(listen, strconv.Itoa(c.Proxy.Port)))
	}
	return addrs
}

func (c Config) HealthURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/health", c.Proxy.Port)
}

func HealthCheck(ctxTimeout time.Duration, port int) error {
	client := &http.Client{Timeout: ctxTimeout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %s", resp.Status)
	}
	return nil
}

func boolString(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
