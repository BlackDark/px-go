package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"

	ntlmssp "github.com/Azure/go-ntlmssp"
)

type NTLMSession struct {
	Credentials Credentials
	stage       int
}

func (s *NTLMSession) Scheme() string { return "NTLM" }

func (s *NTLMSession) Token(_ context.Context, _ *http.Request, challenge string) (string, bool, error) {
	switch s.stage {
	case 0:
		msg, err := ntlmssp.NewNegotiateMessage("", workstation())
		if err != nil {
			return "", false, err
		}
		s.stage = 1
		return "NTLM " + base64.StdEncoding.EncodeToString(msg), false, nil
	case 1:
		data, err := decodeChallenge(challenge)
		if err != nil {
			return "", false, err
		}
		msg, err := ntlmssp.NewAuthenticateMessage(data, s.Credentials.Username, s.Credentials.Password, &ntlmssp.AuthenticateMessageOptions{
			WorkstationName: workstation(),
		})
		if err != nil {
			return "", false, err
		}
		s.stage = 2
		return "NTLM " + base64.StdEncoding.EncodeToString(msg), true, nil
	default:
		return "", true, nil
	}
}

func workstation() string {
	if host, err := os.Hostname(); err == nil {
		return host
	}
	return "PX"
}

var errNoSSPI = errors.New("sspi unavailable")
