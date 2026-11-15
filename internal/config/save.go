package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	ini "gopkg.in/ini.v1"
)

func (c Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		return c.saveTOML(path)
	}
	return c.saveINI(path)
}

func (c Config) saveTOML(path string) error {
	listen := c.Proxy.Listen
	if c.Proxy.Gateway || c.Proxy.HostOnly {
		listen = nil
	}
	t := tomlConfig{}
	t.Proxy.Server = c.Proxy.Server
	t.Proxy.PAC = c.Proxy.PAC
	t.Proxy.PACEncoding = c.Proxy.PACEncoding
	t.Proxy.Listen = listen
	t.Proxy.Port = c.Proxy.Port
	t.Proxy.Gateway = c.Proxy.Gateway
	t.Proxy.HostOnly = c.Proxy.HostOnly
	t.Proxy.Allow = c.Proxy.Allow
	t.Proxy.NoProxy = c.Proxy.NoProxy
	t.Proxy.UserAgent = c.Proxy.UserAgent
	t.Proxy.Username = c.Proxy.Username
	t.Proxy.Auth = c.Proxy.Auth
	t.Proxy.Kerberos = c.Proxy.Kerberos
	t.Client.Username = c.Client.Username
	t.Client.Auth = c.Client.Auth
	t.Client.NoSSPI = c.Client.NoSSPI
	t.Settings.Workers = c.Settings.Workers
	t.Settings.Threads = c.Settings.Threads
	t.Settings.Idle = int(c.Settings.Idle.Seconds())
	t.Settings.SockTimeout = c.Settings.SockTimeout.Seconds()
	t.Settings.ProxyReload = int(c.Settings.ProxyReload.Seconds())
	t.Settings.Foreground = c.Settings.Foreground
	t.Settings.Log = c.Settings.Log
	t.Settings.LogFile = c.Settings.LogFile
	t.Settings.LogLevel = c.Settings.LogLevel.String()

	return writeFileAtomic(path, func(f *os.File) error {
		return toml.NewEncoder(f).Encode(t)
	})
}

func (c Config) saveINI(path string) error {
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
	_, _ = settingsSection.NewKey("socktimeout", strconv.FormatFloat(c.Settings.SockTimeout.Seconds(), 'f', -1, 64))
	_, _ = settingsSection.NewKey("proxyreload", strconv.Itoa(int(c.Settings.ProxyReload.Seconds())))
	_, _ = settingsSection.NewKey("foreground", boolString(c.Settings.Foreground))
	_, _ = settingsSection.NewKey("log", strconv.Itoa(c.Settings.Log))
	_, _ = settingsSection.NewKey("log_file", c.Settings.LogFile)
	_, _ = settingsSection.NewKey("log_level", c.Settings.LogLevel.String())

	return writeFileAtomic(path, func(f *os.File) error {
		_, err := file.WriteTo(f)
		return err
	})
}

// writeFileAtomic leaves path untouched until the write and close succeed.
func writeFileAtomic(path string, write func(*os.File) error) error {
	mode := os.FileMode(0o600)
	info, err := os.Stat(path)
	if err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".px-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := write(f); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
