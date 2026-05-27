//go:build !windows

package auth

type tokenClient interface {
	Next(in []byte) ([]byte, bool, error)
	Close() error
}

func newTokenClient(_ string, _ Credentials, _ string) (tokenClient, error) {
	return nil, errNoSSPI
}
