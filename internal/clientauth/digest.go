package clientauth

import (
	"net/http"
	"strings"
	"time"

	"github.com/BlackDark/px-go/internal/auth"
)

func (h *Handler) verifyDigest(r *http.Request, state *State, header string) bool {
	if state.DigestNonce == "" || time.Since(state.DigestIssuedAt) > 5*time.Minute {
		return false
	}
	params := parseAuthHeader(strings.TrimSpace(strings.TrimPrefix(header, "Digest")))
	if params["nonce"] != state.DigestNonce {
		return false
	}
	user := params["username"]
	if h.username != "" && !strings.EqualFold(user, h.username) {
		return false
	}
	password := h.password
	if password == "" {
		return false
	}
	challenge := `realm="PxClient", nonce="` + state.DigestNonce + `", algorithm=MD5, qop="auth"`
	session := auth.DigestSession{Credentials: auth.Credentials{Username: user, Password: password}}
	expected, _, err := session.Token(background(), r, challenge)
	if err != nil {
		return false
	}
	expectedParams := parseAuthHeader(strings.TrimSpace(strings.TrimPrefix(expected, "Digest")))
	return expectedParams["response"] == params["response"]
}

func parseAuthHeader(header string) map[string]string {
	parts := strings.Split(header, ",")
	out := map[string]string{}
	for _, part := range parts {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return out
}
