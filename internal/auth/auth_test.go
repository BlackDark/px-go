package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestChooseScheme(t *testing.T) {
	scheme, _, ok := ChooseScheme("ANYSAFE", []string{"Basic realm=px", "NTLM"})
	if !ok || scheme != "NTLM" {
		t.Fatalf("unexpected scheme %q ok=%v", scheme, ok)
	}
}

func TestBasicSession(t *testing.T) {
	s := &BasicSession{Credentials: Credentials{Username: "user", Password: "pass"}}
	header, done, err := s.Token(context.Background(), httptestReq(t), "")
	if err != nil || !done || !strings.HasPrefix(header, "Basic ") {
		t.Fatalf("unexpected result header=%q done=%v err=%v", header, done, err)
	}
}

func TestDigestSession(t *testing.T) {
	s := &DigestSession{Credentials: Credentials{Username: "user", Password: "pass"}}
	header, done, err := s.Token(context.Background(), httptestReq(t), `realm="Px", nonce="abc", algorithm=MD5, qop="auth"`)
	if err != nil || !done || !strings.HasPrefix(header, "Digest ") {
		t.Fatalf("unexpected result header=%q done=%v err=%v", header, done, err)
	}
	if !strings.Contains(header, `username="user"`) {
		t.Fatalf("missing username in %q", header)
	}
}

func TestNTLMSessionInitialMessage(t *testing.T) {
	s := &NTLMSession{Credentials: Credentials{Username: `DOMAIN\user`, Password: "pass"}}
	header, done, err := s.Token(context.Background(), httptestReq(t), "")
	if err != nil || done || !strings.HasPrefix(header, "NTLM ") {
		t.Fatalf("unexpected result header=%q done=%v err=%v", header, done, err)
	}
}

func httptestReq(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://example.com/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
