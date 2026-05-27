package auth

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

type DigestSession struct {
	Credentials Credentials
	nc          int
}

func (s *DigestSession) Scheme() string { return "DIGEST" }

func (s *DigestSession) Token(_ context.Context, req *http.Request, challenge string) (string, bool, error) {
	params := parseDigestChallenge(challenge)
	s.nc++
	uri := RequestURI(req)
	cnonce := randomHex(8)
	nc := fmt.Sprintf("%08x", s.nc)
	realm := params["realm"]
	nonce := params["nonce"]
	qop := params["qop"]
	algorithm := params["algorithm"]
	if algorithm == "" {
		algorithm = "MD5"
	}
	ha1 := md5Hex(s.Credentials.Username + ":" + realm + ":" + s.Credentials.Password)
	ha2 := md5Hex(req.Method + ":" + uri)
	response := md5Hex(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":auth:" + ha2)
	if qop == "" {
		response = md5Hex(ha1 + ":" + nonce + ":" + ha2)
	}
	parts := []string{
		fmt.Sprintf(`username="%s"`, s.Credentials.Username),
		fmt.Sprintf(`realm="%s"`, realm),
		fmt.Sprintf(`nonce="%s"`, nonce),
		fmt.Sprintf(`uri="%s"`, uri),
		fmt.Sprintf(`response="%s"`, response),
		fmt.Sprintf(`algorithm=%s`, algorithm),
	}
	if opaque := params["opaque"]; opaque != "" {
		parts = append(parts, fmt.Sprintf(`opaque="%s"`, opaque))
	}
	if qop != "" {
		parts = append(parts,
			fmt.Sprintf("qop=%s", firstCSV(qop)),
			fmt.Sprintf("nc=%s", nc),
			fmt.Sprintf(`cnonce="%s"`, cnonce),
		)
	}
	return "Digest " + strings.Join(parts, ", "), true, nil
}

func parseDigestChallenge(challenge string) map[string]string {
	challenge = strings.TrimSpace(strings.TrimPrefix(challenge, "Digest"))
	parts := strings.Split(challenge, ",")
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

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomHex(size int) string {
	buf := make([]byte, size)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func firstCSV(value string) string {
	parts := strings.Split(value, ",")
	return strings.TrimSpace(parts[0])
}
