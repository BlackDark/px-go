package clientauth

import (
	"strings"

	"github.com/marbaced/migrate-px/px-go/internal/config"
	keyring "github.com/zalando/go-keyring"
)

func (h *Handler) verifyBasic(raw string) bool {
	user, pass, ok := usernameFromBasic(raw)
	if !ok || user == "" {
		return false
	}
	if h.username != "" && !strings.EqualFold(user, h.username) {
		return false
	}
	if h.username != "" {
		return pass == h.password
	}
	secret, err := keyring.Get(config.ClientServiceName, user)
	if err != nil {
		return false
	}
	return pass == secret
}
