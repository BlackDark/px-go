//go:build windows

package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type windowsPlatform struct{}

func current() Interface { return windowsPlatform{} }

func (windowsPlatform) LoadProxyInfo(context.Context, string) (ProxyInfo, error) {
	cfg, err := winHTTPGetIEProxyConfigForCurrentUser()
	if err != nil {
		return ProxyInfo{}, err
	}
	info := ProxyInfo{}
	if cfg.autoConfigURL != "" {
		info.PAC = cfg.autoConfigURL
	}
	if cfg.proxy != "" {
		info.Servers = parseProxyString(cfg.proxy)
	}
	if cfg.proxyBypass != "" {
		info.Bypass = cfg.proxyBypass
	}
	return info, nil
}

func (windowsPlatform) Install(configPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	command := `"` + exe + `" --config="` + configPath + `"`
	windowless := strings.TrimSuffix(exe, filepath.Ext(exe)) + "w" + filepath.Ext(exe)
	if _, err := os.Stat(windowless); err == nil {
		command = `"` + windowless + `" --config="` + configPath + `"`
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue("Px", command)
}

func (windowsPlatform) Uninstall() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.DeleteValue("Px")
}

func (windowsPlatform) AttachConsole() error {
	ret, _, err := procAttachConsole.Call(^uintptr(0))
	if ret == 0 {
		return err
	}
	return nil
}

func (windowsPlatform) DetachConsole() error {
	ret, _, err := procFreeConsole.Call()
	if ret == 0 {
		return err
	}
	return nil
}

type ieProxyConfig struct {
	autoDetect    uint32
	autoConfigURL string
	proxy         string
	proxyBypass   string
}

type winhttpCurrentUserIEProxyConfig struct {
	fAutoDetect       uint32
	lpszAutoConfigUrl *uint16
	lpszProxy         *uint16
	lpszProxyBypass   *uint16
}

var (
	winhttpDLL                                = windows.NewLazySystemDLL("winhttp.dll")
	kernel32                                  = windows.NewLazySystemDLL("kernel32.dll")
	procWinHttpGetIEProxyConfigForCurrentUser = winhttpDLL.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
	procGlobalFree                            = kernel32.NewProc("GlobalFree")
	procAttachConsole                         = kernel32.NewProc("AttachConsole")
	procFreeConsole                           = kernel32.NewProc("FreeConsole")
)

func winHTTPGetIEProxyConfigForCurrentUser() (ieProxyConfig, error) {
	var raw winhttpCurrentUserIEProxyConfig
	ret, _, err := procWinHttpGetIEProxyConfigForCurrentUser.Call(uintptr(unsafe.Pointer(&raw)))
	if ret == 0 {
		return ieProxyConfig{}, err
	}
	cfg := ieProxyConfig{
		autoDetect:    raw.fAutoDetect,
		autoConfigURL: ptrToString(raw.lpszAutoConfigUrl),
		proxy:         ptrToString(raw.lpszProxy),
		proxyBypass:   ptrToString(raw.lpszProxyBypass),
	}
	freeIfNeeded(raw.lpszAutoConfigUrl)
	freeIfNeeded(raw.lpszProxy)
	freeIfNeeded(raw.lpszProxyBypass)
	return cfg, nil
}

func parseProxyString(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ' ' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.Contains(part, "=") {
			_, value, _ := strings.Cut(part, "=")
			part = value
		}
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func ptrToString(ptr *uint16) string {
	if ptr == nil {
		return ""
	}
	return windows.UTF16PtrToString(ptr)
}

func freeIfNeeded(ptr *uint16) {
	if ptr != nil {
		_, _, _ = procGlobalFree.Call(uintptr(unsafe.Pointer(ptr)))
	}
}
