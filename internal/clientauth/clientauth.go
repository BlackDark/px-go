package clientauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/BlackDark/px-go/internal/config"
	ntlmserver "github.com/bigkraig/go-ntlm/ntlm"
	keyring "github.com/zalando/go-keyring"
)

type State struct {
	Authed         bool
	DigestNonce    string
	DigestIssuedAt time.Time
	NTLMSession    ntlmserver.ServerSession
	SSPIServer     authServer
}

type authServer interface {
	Accept(in []byte) (out []byte, username string, done bool, err error)
	Close() error
}

type Handler struct {
	username string
	password string
	methods  []string
	logger   *slog.Logger
	noSSPI   bool
}

func New(cfg config.Client, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	password := os.Getenv("PX_CLIENT_PASSWORD")
	if password == "" && cfg.Username != "" {
		if secret, err := keyring.Get(config.ClientServiceName, cfg.Username); err == nil {
			password = secret
		}
	}
	return &Handler{
		username: cfg.Username,
		password: password,
		methods:  expandMethods(cfg.Auth, cfg.NoSSPI),
		logger:   logger,
		noSSPI:   cfg.NoSSPI,
	}
}

func (h *Handler) Enabled() bool {
	return len(h.methods) > 0
}

func (h *Handler) Authenticate(r *http.Request, state *State) (bool, http.Header, error) {
	if !h.Enabled() || state.Authed {
		return true, nil, nil
	}
	value := r.Header.Get("Proxy-Authorization")
	if value == "" {
		return false, h.challenge(state, r.RemoteAddr), nil
	}
	scheme, payload, _ := strings.Cut(value, " ")
	scheme = strings.ToUpper(strings.TrimSpace(scheme))
	payload = strings.TrimSpace(payload)
	switch scheme {
	case "BASIC":
		if h.verifyBasic(payload) {
			state.Authed = true
			return true, nil, nil
		}
	case "DIGEST":
		if h.verifyDigest(r, state, value) {
			state.Authed = true
			return true, nil, nil
		}
	case "NTLM":
		ok, header, err := h.verifyNTLM(payload, state)
		if ok {
			state.Authed = true
			return true, nil, nil
		}
		if header != "" {
			resp := http.Header{}
			resp.Add("Proxy-Authenticate", header)
			return false, resp, err
		}
	case "NEGOTIATE":
		ok, header, err := h.verifyNegotiate(payload, state)
		if ok {
			state.Authed = true
			return true, nil, nil
		}
		if header != "" {
			resp := http.Header{}
			resp.Add("Proxy-Authenticate", header)
			return false, resp, err
		}
	}
	return false, h.challenge(state, r.RemoteAddr), nil
}

func (h *Handler) challenge(state *State, remoteAddr string) http.Header {
	headers := http.Header{}
	for _, method := range h.methods {
		switch method {
		case "BASIC":
			headers.Add("Proxy-Authenticate", `Basic realm="PxClient"`)
		case "DIGEST":
			state.DigestNonce = issueNonce(remoteAddr)
			state.DigestIssuedAt = time.Now()
			headers.Add("Proxy-Authenticate", fmt.Sprintf(`Digest realm="PxClient", nonce="%s", algorithm=MD5, qop="auth"`, state.DigestNonce))
		case "NTLM":
			headers.Add("Proxy-Authenticate", "NTLM")
		case "NEGOTIATE":
			headers.Add("Proxy-Authenticate", "Negotiate")
		}
	}
	return headers
}

func (h *Handler) CloseState(state *State) {
	if state == nil {
		return
	}
	if state.SSPIServer != nil {
		_ = state.SSPIServer.Close()
	}
}

func expandMethods(raw string, noSSPI bool) []string {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	switch raw {
	case "", "NONE":
		return nil
	case "ANY":
		if noSSPI {
			return []string{"NTLM", "DIGEST", "BASIC"}
		}
		return []string{"NEGOTIATE", "NTLM", "DIGEST", "BASIC"}
	case "ANYSAFE":
		if noSSPI {
			return []string{"NTLM", "DIGEST"}
		}
		return []string{"NEGOTIATE", "NTLM", "DIGEST"}
	default:
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "NEGOTIATE" && noSSPI {
				continue
			}
			if part != "NONE" && part != "" {
				out = append(out, part)
			}
		}
		return out
	}
}

func issueNonce(remoteAddr string) string {
	sum := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano) + ":" + remoteAddr + ":PxClient"))
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%x", time.Now().Unix(), sum[:])))
}

func splitUser(username string) (domain, user string) {
	if strings.Contains(username, `\`) {
		parts := strings.SplitN(username, `\`, 2)
		return parts[0], parts[1]
	}
	return "", username
}

func usernameFromBasic(raw string) (string, string, bool) {
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", "", false
	}
	user, pass, ok := strings.Cut(string(decoded), ":")
	return user, pass, ok
}

func background() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}
