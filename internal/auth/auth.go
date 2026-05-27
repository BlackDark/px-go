package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BlackDark/px-go/internal/config"
	"github.com/BlackDark/px-go/internal/kerberos"
	krbclient "github.com/jcmturner/gokrb5/v8/client"
	krbconfig "github.com/jcmturner/gokrb5/v8/config"
	krbspnego "github.com/jcmturner/gokrb5/v8/spnego"
	keyring "github.com/zalando/go-keyring"
)

type Credentials struct {
	Username string
	Password string
}

type Session interface {
	Scheme() string
	Token(ctx context.Context, req *http.Request, challenge string) (header string, done bool, err error)
}

type Factory struct {
	Credentials Credentials
	Kerberos    *kerberos.Manager
	Logger      *slog.Logger
}

func CredentialsFromConfig(proxy config.Proxy) Credentials {
	password := os.Getenv("PX_PASSWORD")
	if password == "" && proxy.Username != "" {
		if secret, err := keyring.Get(config.ServiceName, proxy.Username); err == nil {
			password = secret
		}
	}
	return Credentials{Username: proxy.Username, Password: password}
}

func (f Factory) NewSession(scheme string, targetHost string) (Session, error) {
	scheme = strings.ToUpper(strings.TrimSpace(scheme))
	switch scheme {
	case "BASIC":
		return &BasicSession{Credentials: f.Credentials}, nil
	case "DIGEST":
		return &DigestSession{Credentials: f.Credentials}, nil
	case "NTLM":
		return &NTLMSession{Credentials: f.Credentials}, nil
	case "NEGOTIATE":
		return newNegotiateSession(f.Credentials, f.Kerberos, targetHost)
	default:
		return nil, fmt.Errorf("unsupported auth scheme %s", scheme)
	}
}

func ChooseScheme(preference string, challenges []string) (string, string, bool) {
	offered := parseChallenges(challenges)
	for _, scheme := range preferenceOrder(preference) {
		if challenge, ok := offered[scheme]; ok {
			return scheme, challenge, true
		}
	}
	return "", "", false
}

func parseChallenges(values []string) map[string]string {
	out := map[string]string{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, " ", 2)
		scheme := strings.ToUpper(strings.TrimSpace(parts[0]))
		rest := ""
		if len(parts) > 1 {
			rest = strings.TrimSpace(parts[1])
		}
		out[scheme] = rest
	}
	return out
}

func preferenceOrder(preference string) []string {
	preference = strings.ToUpper(strings.TrimSpace(preference))
	safe := []string{"NEGOTIATE", "NTLM", "DIGEST"}
	all := []string{"NEGOTIATE", "NTLM", "DIGEST", "BASIC"}
	switch {
	case preference == "", preference == "ANY":
		return all
	case preference == "ANYSAFE":
		return safe
	case preference == "NONE":
		return nil
	case strings.HasPrefix(preference, "ONLY"):
		return []string{strings.TrimPrefix(preference, "ONLY")}
	case strings.HasPrefix(preference, "SAFE") && strings.HasPrefix(strings.TrimPrefix(preference, "SAFE"), "NO"):
		exclude := strings.TrimPrefix(strings.TrimPrefix(preference, "SAFE"), "NO")
		return excludeScheme(safe, exclude)
	case strings.HasPrefix(preference, "NO"):
		exclude := strings.TrimPrefix(preference, "NO")
		return excludeScheme(all, exclude)
	default:
		return []string{preference}
	}
}

func excludeScheme(list []string, exclude string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item != exclude {
			out = append(out, item)
		}
	}
	return out
}

func ProxyAuthorization(req *http.Request, header string) *http.Request {
	clone := req.Clone(req.Context())
	clone.Header = clone.Header.Clone()
	clone.Header.Set("Proxy-Authorization", header)
	return clone
}

func RequestURI(req *http.Request) string {
	if req.Method == http.MethodConnect {
		return req.Host
	}
	if req.URL == nil {
		return "/"
	}
	if req.URL.IsAbs() {
		return req.URL.String()
	}
	if uri := req.URL.RequestURI(); uri != "" {
		return uri
	}
	return "/"
}

func parseUser(username string) (user, domain string, domainNeeded bool) {
	if strings.Contains(username, `\`) {
		parts := strings.SplitN(username, `\`, 2)
		return parts[1], parts[0], true
	}
	if strings.Contains(username, "@") {
		return username, "", false
	}
	return username, "", true
}

func decodeChallenge(challenge string) ([]byte, error) {
	if strings.TrimSpace(challenge) == "" {
		return nil, errors.New("empty challenge")
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(challenge))
}

func loadKrb5Config() (*krbconfig.Config, error) {
	candidates := []string{}
	if env := os.Getenv("KRB5_CONFIG"); env != "" {
		candidates = append(candidates, env)
	}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, filepath.Join(os.Getenv("ProgramData"), "MIT", "Kerberos5", "krb5.ini"))
	} else {
		candidates = append(candidates, "/etc/krb5.conf")
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return krbconfig.Load(candidate)
		}
	}
	return nil, errors.New("krb5.conf not found")
}

func buildKrbClient(creds Credentials) (*krbclient.Client, error) {
	user, realm, _ := parseUser(creds.Username)
	if realm == "" {
		return nil, errors.New("kerberos realm is required in username")
	}
	cfg, err := loadKrb5Config()
	if err != nil {
		return nil, err
	}
	client := krbclient.NewWithPassword(user, strings.ToUpper(realm), creds.Password, cfg)
	if err := client.Login(); err != nil {
		return nil, err
	}
	return client, nil
}

type NegotiateSession struct {
	creds    Credentials
	kerberos *kerberos.Manager
	sspi     tokenClient
	usedSSPI bool
	done     bool
}

func newNegotiateSession(creds Credentials, manager *kerberos.Manager, targetHost string) (Session, error) {
	if sspi, err := newTokenClient("Negotiate", creds, targetHost); err == nil {
		return &NegotiateSession{creds: creds, kerberos: manager, sspi: sspi, usedSSPI: true}, nil
	}
	return &NegotiateSession{creds: creds, kerberos: manager}, nil
}

func (s *NegotiateSession) Scheme() string { return "NEGOTIATE" }

func (s *NegotiateSession) Token(ctx context.Context, req *http.Request, challenge string) (string, bool, error) {
	if s.done {
		return "", true, nil
	}
	if s.usedSSPI {
		var input []byte
		var err error
		if strings.TrimSpace(challenge) != "" {
			input, err = decodeChallenge(challenge)
			if err != nil {
				return "", false, err
			}
		}
		out, complete, err := s.sspi.Next(input)
		if err != nil {
			return "", false, err
		}
		s.done = complete
		return "Negotiate " + base64.StdEncoding.EncodeToString(out), complete, nil
	}
	krbClient, err := buildKrbClient(s.creds)
	if err != nil {
		return "", false, err
	}
	clone := req.Clone(ctx)
	clone.Header = clone.Header.Clone()
	if err := krbspnego.SetSPNEGOHeader(krbClient, clone, "HTTP/"+clone.URL.Hostname()); err != nil {
		return "", false, err
	}
	value := clone.Header.Get("Authorization")
	value = strings.TrimPrefix(value, "Authorization")
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "Negotiate ") {
		return "", false, errors.New("failed to create negotiate header")
	}
	return value, true, nil
}
