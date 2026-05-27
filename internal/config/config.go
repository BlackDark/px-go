package config

import (
	"errors"
	"fmt"
	"io"
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

	"github.com/joho/godotenv"
	ini "gopkg.in/ini.v1"
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
			Threads:     32,
			Idle:        30 * time.Second,
			SockTimeout: 20 * time.Second,
			ProxyReload: 60 * time.Second,
			LogLevel:    slog.LevelInfo,
		},
	}
}

func Load(args []string) (Config, error) {
	cfg := Default()
	cli, err := parseArgs(args)
	if err != nil {
		return cfg, err
	}

	configPath, err := resolveConfigPath(valueOrEmpty(cli, "config"))
	if err != nil {
		return cfg, err
	}
	cfg.Special.ConfigPath = configPath
	if configPath != "" {
		if err := applyINI(&cfg, configPath); err != nil {
			return cfg, err
		}
	}

	loadDotEnv(configPath)
	applyEnv(&cfg)
	if err := applyValues(&cfg, cli); err != nil {
		return cfg, err
	}
	cfg.Special.ConfigPath = configPath
	cfg.normalize()
	return cfg, cfg.validate()
}

func (c *Config) normalize() {
	c.Proxy.Server = normalizeCSV(c.Proxy.Server)
	c.Proxy.Listen = normalizeCSV(c.Proxy.Listen)
	if len(c.Proxy.Listen) == 0 {
		c.Proxy.Listen = []string{"127.0.0.1"}
	}
	c.Proxy.Auth = strings.ToUpper(strings.TrimSpace(c.Proxy.Auth))
	if c.Proxy.Auth == "" {
		c.Proxy.Auth = "ANY"
	}
	c.Client.Auth = strings.ToUpper(strings.TrimSpace(c.Client.Auth))
	if c.Client.Auth == "" {
		c.Client.Auth = "NONE"
	}
	if c.Proxy.Gateway || c.Proxy.HostOnly {
		c.Proxy.Listen = nil
	}
}

func (c Config) validate() error {
	if c.Proxy.Port <= 0 || c.Proxy.Port > 65535 {
		return fmt.Errorf("invalid port %d", c.Proxy.Port)
	}
	if c.Settings.Threads <= 0 {
		return errors.New("threads must be > 0")
	}
	if c.Settings.Workers <= 0 {
		return errors.New("workers must be > 0")
	}
	return nil
}

func parseArgs(args []string) (map[string]string, error) {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			return nil, fmt.Errorf("unsupported argument %q", arg)
		}
		arg = strings.TrimPrefix(arg, "--")
		key, val, hasValue := strings.Cut(arg, "=")
		key = strings.ReplaceAll(strings.TrimSpace(key), "-", "_")
		if !hasValue {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				val = args[i+1]
				i++
			} else {
				val = "true"
			}
		}
		values[strings.ToLower(key)] = strings.TrimSpace(val)
	}
	return values, nil
}

func resolveConfigPath(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return explicit, fmt.Errorf("config %s: %w", explicit, err)
		}
		return explicit, nil
	}
	candidates := []string{
		filepath.Join(mustGetwd(), "px.ini"),
		filepath.Join(ConfigDir(), "px.ini"),
		filepath.Join(scriptDir(), "px.ini"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", nil
}

func loadDotEnv(configPath string) {
	files := []string{filepath.Join(mustGetwd(), ".env")}
	if configPath != "" {
		files = append(files, filepath.Join(filepath.Dir(configPath), ".env"))
	}
	files = append(files, filepath.Join(scriptDir(), ".env"))
	seen := map[string]struct{}{}
	for _, file := range files {
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		_ = godotenv.Load(file)
	}
}

func applyINI(cfg *Config, path string) error {
	file, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, path)
	if err != nil {
		return err
	}
	mapSection := func(section, key string) string {
		return strings.TrimSpace(file.Section(section).Key(key).String())
	}
	values := map[string]string{
		"server":          mapSection("proxy", "server"),
		"pac":             mapSection("proxy", "pac"),
		"pac_encoding":    mapSection("proxy", "pac_encoding"),
		"listen":          mapSection("proxy", "listen"),
		"port":            mapSection("proxy", "port"),
		"gateway":         mapSection("proxy", "gateway"),
		"hostonly":        mapSection("proxy", "hostonly"),
		"allow":           mapSection("proxy", "allow"),
		"noproxy":         mapSection("proxy", "noproxy"),
		"useragent":       mapSection("proxy", "useragent"),
		"username":        mapSection("proxy", "username"),
		"auth":            mapSection("proxy", "auth"),
		"kerberos":        mapSection("proxy", "kerberos"),
		"client_username": mapSection("client", "client_username"),
		"client_auth":     mapSection("client", "client_auth"),
		"client_nosspi":   mapSection("client", "client_nosspi"),
		"workers":         mapSection("settings", "workers"),
		"threads":         mapSection("settings", "threads"),
		"idle":            mapSection("settings", "idle"),
		"socktimeout":     mapSection("settings", "socktimeout"),
		"proxyreload":     mapSection("settings", "proxyreload"),
		"foreground":      mapSection("settings", "foreground"),
		"log":             mapSection("settings", "log"),
		"log_level":       mapSection("settings", "log_level"),
	}
	return applyValues(cfg, values)
}

func applyEnv(cfg *Config) {
	values := map[string]string{}
	for _, key := range []string{
		"server", "pac", "pac_encoding", "listen", "port", "gateway", "hostonly", "allow", "noproxy", "useragent", "username", "auth", "kerberos",
		"client_username", "client_auth", "client_nosspi",
		"workers", "threads", "idle", "socktimeout", "proxyreload", "foreground", "log", "log_level",
		"config", "quit", "test", "install", "uninstall", "save", "version", "health_check",
	} {
		if v, ok := os.LookupEnv("PX_" + strings.ToUpper(key)); ok {
			values[key] = v
		}
	}
	_ = applyValues(cfg, values)
}

func applyValues(cfg *Config, values map[string]string) error {
	for rawKey, rawValue := range values {
		key := strings.ToLower(rawKey)
		if rawValue == "" && key != "listen" && key != "server" && key != "noproxy" && key != "allow" && key != "useragent" && key != "username" && key != "client_username" && key != "auth" && key != "client_auth" && key != "pac" {
			continue
		}
		switch key {
		case "proxy":
			cfg.Proxy.Server = splitCSV(rawValue)
		case "server":
			cfg.Proxy.Server = splitCSV(rawValue)
		case "pac":
			cfg.Proxy.PAC = rawValue
		case "pac_encoding":
			cfg.Proxy.PACEncoding = rawValue
		case "listen":
			cfg.Proxy.Listen = splitCSV(rawValue)
		case "port":
			v, err := strconv.Atoi(rawValue)
			if err != nil {
				return fmt.Errorf("invalid port %q: %w", rawValue, err)
			}
			cfg.Proxy.Port = v
		case "gateway":
			cfg.Proxy.Gateway = parseBool(rawValue)
		case "hostonly":
			cfg.Proxy.HostOnly = parseBool(rawValue)
		case "allow":
			cfg.Proxy.Allow = rawValue
		case "noproxy":
			cfg.Proxy.NoProxy = rawValue
		case "useragent":
			cfg.Proxy.UserAgent = rawValue
		case "username":
			cfg.Proxy.Username = rawValue
		case "auth":
			cfg.Proxy.Auth = rawValue
		case "kerberos":
			cfg.Proxy.Kerberos = parseBool(rawValue)
		case "client_username":
			cfg.Client.Username = rawValue
		case "client_auth":
			cfg.Client.Auth = rawValue
		case "client_nosspi":
			cfg.Client.NoSSPI = parseBool(rawValue)
		case "workers":
			v, err := strconv.Atoi(rawValue)
			if err != nil {
				return fmt.Errorf("invalid workers %q: %w", rawValue, err)
			}
			cfg.Settings.Workers = v
		case "threads":
			v, err := strconv.Atoi(rawValue)
			if err != nil {
				return fmt.Errorf("invalid threads %q: %w", rawValue, err)
			}
			cfg.Settings.Threads = v
		case "idle":
			v, err := strconv.Atoi(rawValue)
			if err != nil {
				return fmt.Errorf("invalid idle %q: %w", rawValue, err)
			}
			cfg.Settings.Idle = time.Duration(v) * time.Second
		case "socktimeout":
			v, err := strconv.ParseFloat(rawValue, 64)
			if err != nil {
				return fmt.Errorf("invalid socktimeout %q: %w", rawValue, err)
			}
			cfg.Settings.SockTimeout = time.Duration(v * float64(time.Second))
		case "proxyreload":
			v, err := strconv.Atoi(rawValue)
			if err != nil {
				return fmt.Errorf("invalid proxyreload %q: %w", rawValue, err)
			}
			cfg.Settings.ProxyReload = time.Duration(v) * time.Second
		case "foreground":
			cfg.Settings.Foreground = parseBool(rawValue)
		case "log":
			v, err := strconv.Atoi(rawValue)
			if err != nil {
				return fmt.Errorf("invalid log %q: %w", rawValue, err)
			}
			cfg.Settings.Log = v
		case "log_level":
			level, err := parseLevel(rawValue)
			if err != nil {
				return err
			}
			cfg.Settings.LogLevel = level
		case "config":
			cfg.Special.ConfigPath = rawValue
		case "quit":
			cfg.Special.Quit = parseBool(rawValue)
		case "test":
			if parseBool(rawValue) {
				cfg.Special.TestURL = "all"
			} else {
				cfg.Special.TestURL = rawValue
			}
		case "install":
			cfg.Special.Install = parseBool(rawValue)
		case "uninstall":
			cfg.Special.Uninstall = parseBool(rawValue)
		case "save":
			cfg.Special.Save = parseBool(rawValue)
		case "version":
			cfg.Special.Version = parseBool(rawValue)
		case "health_check":
			cfg.Special.HealthCheck = parseBool(rawValue)
		case "password":
			cfg.Special.Password = parseBool(rawValue)
		case "client_password":
			cfg.Special.ClientPassword = parseBool(rawValue)
		case "restart":
			cfg.Special.Restart = parseBool(rawValue)
		case "verbose":
			if parseBool(rawValue) {
				cfg.Settings.Log = 4
				cfg.Settings.Foreground = true
				cfg.Settings.LogLevel = slog.LevelDebug
			}
		case "quiet", "silent":
			cfg.Settings.Quiet = parseBool(rawValue)
		default:
			// Ignore unknown keys to remain compatible with px.ini extras.
		}
	}
	return nil
}

func (c Config) Save(path string) error {
	file := ini.Empty()
	proxySection, _ := file.NewSection("proxy")
	clientSection, _ := file.NewSection("client")
	settingsSection, _ := file.NewSection("settings")

	_, _ = proxySection.NewKey("server", strings.Join(c.Proxy.Server, ","))
	_, _ = proxySection.NewKey("pac", c.Proxy.PAC)
	_, _ = proxySection.NewKey("pac_encoding", c.Proxy.PACEncoding)
	listen := strings.Join(c.Proxy.Listen, ",")
	if c.Proxy.Gateway || c.Proxy.HostOnly {
		listen = ""
	}
	_, _ = proxySection.NewKey("listen", listen)
	_, _ = proxySection.NewKey("port", strconv.Itoa(c.Proxy.Port))
	_, _ = proxySection.NewKey("gateway", boolString(c.Proxy.Gateway))
	_, _ = proxySection.NewKey("hostonly", boolString(c.Proxy.HostOnly))
	_, _ = proxySection.NewKey("allow", c.Proxy.Allow)
	_, _ = proxySection.NewKey("noproxy", c.Proxy.NoProxy)
	_, _ = proxySection.NewKey("useragent", c.Proxy.UserAgent)
	_, _ = proxySection.NewKey("username", c.Proxy.Username)
	_, _ = proxySection.NewKey("auth", c.Proxy.Auth)
	_, _ = proxySection.NewKey("kerberos", boolString(c.Proxy.Kerberos))

	_, _ = clientSection.NewKey("client_username", c.Client.Username)
	_, _ = clientSection.NewKey("client_auth", c.Client.Auth)
	_, _ = clientSection.NewKey("client_nosspi", boolString(c.Client.NoSSPI))

	_, _ = settingsSection.NewKey("workers", strconv.Itoa(c.Settings.Workers))
	_, _ = settingsSection.NewKey("threads", strconv.Itoa(c.Settings.Threads))
	_, _ = settingsSection.NewKey("idle", strconv.Itoa(int(c.Settings.Idle.Seconds())))
	_, _ = settingsSection.NewKey("socktimeout", strconv.FormatFloat(c.Settings.SockTimeout.Seconds(), 'f', 1, 64))
	_, _ = settingsSection.NewKey("proxyreload", strconv.Itoa(int(c.Settings.ProxyReload.Seconds())))
	_, _ = settingsSection.NewKey("foreground", boolString(c.Settings.Foreground))
	_, _ = settingsSection.NewKey("log", strconv.Itoa(c.Settings.Log))
	_, _ = settingsSection.NewKey("log_level", c.Settings.LogLevel.String())

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return file.SaveTo(path)
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

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func NewLogger(cfg Config) (*slog.Logger, io.Closer, error) {
	output := io.Writer(io.Discard)
	closer := io.Closer(nopCloser{})
	if cfg.Settings.Log != 0 {
		switch cfg.Settings.Log {
		case 1:
			path := filepath.Join(scriptDir(), "debug.log")
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return nil, nil, err
			}
			output = file
			closer = file
		case 2:
			path := filepath.Join(mustGetwd(), "debug.log")
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return nil, nil, err
			}
			output = file
			closer = file
		case 3:
			path := filepath.Join(mustGetwd(), fmt.Sprintf("debug-%d-%d.log", cfg.Proxy.Port, time.Now().UnixNano()))
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return nil, nil, err
			}
			output = file
			closer = file
		case 4:
			output = os.Stdout
		default:
			output = io.Discard
		}
	}
	handler := slog.NewTextHandler(output, &slog.HandlerOptions{Level: cfg.Settings.LogLevel})
	return slog.New(handler), closer, nil
}

func boolString(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
