package platform

import "context"

type ProxyInfo struct {
	Servers []string
	PAC     string
	Bypass  string
}

type Interface interface {
	LoadProxyInfo(context.Context, string) (ProxyInfo, error)
	Install(string) error
	Uninstall() error
}

func Current() Interface {
	return current()
}
