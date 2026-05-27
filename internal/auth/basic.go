package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
)

type BasicSession struct {
	Credentials Credentials
}

func (s *BasicSession) Scheme() string { return "BASIC" }

func (s *BasicSession) Token(_ context.Context, _ *http.Request, _ string) (string, bool, error) {
	if s.Credentials.Username == "" {
		return "", false, errors.New("username required for basic auth")
	}
	raw := s.Credentials.Username + ":" + s.Credentials.Password
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw)), true, nil
}

func (s *BasicSession) Close() error { return nil }
