package clientauth

import (
	"encoding/base64"
	"errors"
	"strings"

	ntlmserver "github.com/bigkraig/go-ntlm/ntlm"
)

func (h *Handler) verifyNTLM(payload string, state *State) (bool, string, error) {
	if h.username == "" || h.password == "" {
		return false, "", errors.New("client_username and password are required for NTLM")
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return false, "", err
	}
	if state.NTLMSession == nil {
		sess, err := ntlmserver.CreateServerSession(ntlmserver.Version2, ntlmserver.ConnectionOrientedMode)
		if err != nil {
			return false, "", err
		}
		domain, user := splitUser(h.username)
		sess.SetUserInfo(user, h.password, domain)
		if err := sess.ProcessNegotiateMessage(&ntlmserver.NegotiateMessage{Bytes: data}); err != nil {
			return false, "", err
		}
		challenge, err := sess.GenerateChallengeMessage()
		if err != nil {
			return false, "", err
		}
		state.NTLMSession = sess
		return false, "NTLM " + base64.StdEncoding.EncodeToString(challenge.Bytes()), nil
	}
	msg, err := ntlmserver.ParseAuthenticateMessage(data, 2)
	if err != nil {
		return false, "", err
	}
	if err := state.NTLMSession.ProcessAuthenticateMessage(msg); err != nil {
		return false, "", err
	}
	return true, "", nil
}

func (h *Handler) verifyNegotiate(payload string, state *State) (bool, string, error) {
	if strings.HasPrefix(payload, "TlRMTVNTUA") {
		return h.verifyNTLM(payload, state)
	}
	if h.noSSPI {
		return false, "", errors.New("negotiate requires SSPI on this platform")
	}
	if state.SSPIServer == nil {
		server, err := newNegotiateServer()
		if err != nil {
			return false, "", err
		}
		state.SSPIServer = server
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return false, "", err
	}
	out, _, done, err := state.SSPIServer.Accept(data)
	if err != nil {
		return false, "", err
	}
	if done {
		return true, "", nil
	}
	return false, "Negotiate " + base64.StdEncoding.EncodeToString(out), nil
}
