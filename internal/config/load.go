package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/joho/godotenv"
	ini "gopkg.in/ini.v1"
)

func Load(args []string) (Config, error) {
	cfg := Default()
	cli, err := parseArgs(args)
	if err != nil {
		return cfg, err
	}

	cliPath := valueOrEmpty(cli, "config")
	configPath, err := resolveConfigPath(cliPath)
	if err != nil {
		return cfg, err
	}
	cfg.Special.ConfigPath = configPath
	if configPath != "" {
		var loadErr error
		if strings.EqualFold(filepath.Ext(configPath), ".toml") {
			loadErr = applyTOML(&cfg, configPath)
		} else {
			loadErr = applyINI(&cfg, configPath)
		}
		if loadErr != nil {
			return cfg, loadErr
		}
	}

	loadDotEnv(configPath)
	if err := applyEnv(&cfg); err != nil {
		return cfg, err
	}
	if err := applyValues(&cfg, cli); err != nil {
		return cfg, err
	}
	// Precedence for the recorded config path: CLI beats PX_CONFIG, which beats
	// auto-discovery. resolveConfigPath already folds PX_CONFIG into configPath,
	// so recorded and loaded always agree.
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
	c.Proxy.PAC = resolvePACPath(c.Proxy.PAC, c.Special.ConfigPath)
}

func resolvePACPath(pac, configPath string) string {
	pac = strings.TrimSpace(pac)
	if pac == "" {
		return ""
	}
	lower := strings.ToLower(pac)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "file://") {
		return pac
	}
	if filepath.IsAbs(pac) {
		return pac
	}
	base := scriptDir()
	if configPath != "" {
		base = filepath.Dir(configPath)
	}
	joined := filepath.Join(base, pac)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return joined
	}
	return abs
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
	// PX_CONFIG selects the file when --config is absent. Best-effort: a path that
	// does not exist falls through to discovery rather than failing startup,
	// which is how this variable behaved while it was parsed but ignored.
	if env := strings.TrimSpace(os.Getenv("PX_CONFIG")); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}
	// px.toml takes precedence over px.ini in each candidate directory.
	dirs := []string{mustGetwd(), ConfigDir(), scriptDir()}
	for _, dir := range dirs {
		for _, name := range []string{"px.toml", "px.ini"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
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
		"log_file":        mapSection("settings", "log_file"),
		"log_level":       mapSection("settings", "log_level"),
	}
	if file.Section("proxy").HasKey("allow") {
		values["allow"] = mapSection("proxy", "allow")
	}
	return applyValues(cfg, values)
}

// tomlConfig mirrors the TOML config file structure. Fields use native TOML
// types (arrays, bools, ints) where appropriate; they are converted to the
// canonical flat string map so that applyValues handles all validation.
type tomlConfig struct {
	Proxy struct {
		Server      []string `toml:"server"`
		PAC         string   `toml:"pac"`
		PACEncoding string   `toml:"pac_encoding"`
		Listen      []string `toml:"listen"`
		Port        int      `toml:"port"`
		Gateway     bool     `toml:"gateway"`
		HostOnly    bool     `toml:"hostonly"`
		Allow       string   `toml:"allow"`
		NoProxy     string   `toml:"noproxy"`
		UserAgent   string   `toml:"useragent"`
		Username    string   `toml:"username"`
		Auth        string   `toml:"auth"`
		Kerberos    bool     `toml:"kerberos"`
	} `toml:"proxy"`
	Client struct {
		Username string `toml:"client_username"`
		Auth     string `toml:"client_auth"`
		NoSSPI   bool   `toml:"client_nosspi"`
	} `toml:"client"`
	Settings struct {
		Workers     int     `toml:"workers"`
		Threads     int     `toml:"threads"`
		Idle        int     `toml:"idle"`
		SockTimeout float64 `toml:"socktimeout"`
		ProxyReload int     `toml:"proxyreload"`
		Foreground  bool    `toml:"foreground"`
		Log         int     `toml:"log"`
		LogFile     string  `toml:"log_file"`
		LogLevel    string  `toml:"log_level"`
	} `toml:"settings"`
}

func applyTOML(cfg *Config, path string) error {
	var t tomlConfig
	meta, err := toml.DecodeFile(path, &t)
	if err != nil {
		return err
	}
	p := t.Proxy
	s := t.Settings
	values := map[string]string{
		"server":          strings.Join(p.Server, ","),
		"pac":             p.PAC,
		"pac_encoding":    p.PACEncoding,
		"listen":          strings.Join(p.Listen, ","),
		"gateway":         boolString(p.Gateway),
		"hostonly":        boolString(p.HostOnly),
		"noproxy":         p.NoProxy,
		"useragent":       p.UserAgent,
		"username":        p.Username,
		"auth":            p.Auth,
		"kerberos":        boolString(p.Kerberos),
		"client_username": t.Client.Username,
		"client_auth":     t.Client.Auth,
		"client_nosspi":   boolString(t.Client.NoSSPI),
		"foreground":      boolString(s.Foreground),
		"log_file":        s.LogFile,
		"log_level":       s.LogLevel,
	}
	if meta.IsDefined("proxy", "allow") {
		values["allow"] = p.Allow
	}
	if meta.IsDefined("proxy", "port") {
		values["port"] = strconv.Itoa(p.Port)
	}
	if meta.IsDefined("settings", "workers") {
		values["workers"] = strconv.Itoa(s.Workers)
	}
	if meta.IsDefined("settings", "threads") {
		values["threads"] = strconv.Itoa(s.Threads)
	}
	if meta.IsDefined("settings", "idle") {
		values["idle"] = strconv.Itoa(s.Idle)
	}
	if meta.IsDefined("settings", "socktimeout") {
		values["socktimeout"] = strconv.FormatFloat(s.SockTimeout, 'f', -1, 64)
	}
	if meta.IsDefined("settings", "proxyreload") {
		values["proxyreload"] = strconv.Itoa(s.ProxyReload)
	}
	if meta.IsDefined("settings", "log") {
		values["log"] = strconv.Itoa(s.Log)
	}
	return applyValues(cfg, values)
}

func applyEnv(cfg *Config) error {
	values := map[string]string{}
	for _, key := range []string{
		"server", "pac", "pac_encoding", "listen", "port", "gateway", "hostonly", "allow", "noproxy", "useragent", "username", "auth", "kerberos",
		"client_username", "client_auth", "client_nosspi",
		"workers", "threads", "idle", "socktimeout", "proxyreload", "foreground", "log", "log_file", "log_level",
		"config", "quit", "test", "install", "uninstall", "save", "version", "health_check",
	} {
		if v, ok := os.LookupEnv("PX_" + strings.ToUpper(key)); ok {
			values[key] = v
		}
	}
	return applyValues(cfg, values)
}

func applyValues(cfg *Config, values map[string]string) error {
	// Sorted iteration: "proxy" and "server" both assign Proxy.Server, so Go's
	// randomized map order would otherwise make the same argv yield either value.
	keys := make([]string, 0, len(values))
	for rawKey := range values {
		keys = append(keys, rawKey)
	}
	slices.Sort(keys)
	for _, rawKey := range keys {
		rawValue := values[rawKey]
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
		case "log_file":
			cfg.Settings.LogFile = rawValue
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
