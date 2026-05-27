//go:build !windows

package platform

import "context"

type unixPlatform struct{}

func current() Interface { return unixPlatform{} }

func (unixPlatform) LoadProxyInfo(context.Context, string) (ProxyInfo, error) {
	return ProxyInfo{}, nil
}
func (unixPlatform) Install(string) error { return nil }
func (unixPlatform) Uninstall() error     { return nil }
func (unixPlatform) AttachConsole() error { return nil }
func (unixPlatform) DetachConsole() error { return nil }
