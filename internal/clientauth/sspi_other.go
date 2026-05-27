//go:build !windows

package clientauth

import "errors"

func newNegotiateServer() (authServer, error) {
	return nil, errors.New("sspi unavailable")
}
