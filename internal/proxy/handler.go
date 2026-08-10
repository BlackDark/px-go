package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/BlackDark/px-go/internal/auth"
	"github.com/BlackDark/px-go/internal/network"
	"github.com/BlackDark/px-go/internal/pac"
)

type route struct {
	Direct  bool
	Proxies []string
}

// connBodyCloser wraps a response body so that closing it also closes the
// underlying TCP connection. This prevents connection leaks when the proxy
// returns a response read from a raw connection.
type connBodyCloser struct {
	io.ReadCloser
	conn net.Conn
}

func (c *connBodyCloser) Close() error {
	err := c.ReadCloser.Close()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	return err
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Settings.SockTimeout*2)
	defer cancel()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()
	route, err := s.resolveRoute(ctx, r)
	if err != nil {
		s.logger.Debug("route resolution failed", "host", r.Host, "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if route.Direct {
		s.logger.Debug("HTTP direct", "method", r.Method, "host", r.Host)
		resp, err := s.roundTripDirect(ctx, r, body)
		if err != nil {
			s.logger.Debug("HTTP direct failed", "host", r.Host, "err", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		copyResponse(w, resp)
		return
	}
	s.logger.Debug("HTTP via proxy", "method", r.Method, "host", r.Host, "proxies", route.Proxies)
	var lastErr error
	for _, upstream := range route.Proxies {
		resp, err := s.roundTripProxy(ctx, r, body, upstream)
		if err != nil {
			lastErr = err
			s.logger.Debug("HTTP proxy attempt failed", "proxy", upstream, "err", err)
			continue
		}
		defer func() { _ = resp.Body.Close() }()
		copyResponse(w, resp)
		return
	}
	if lastErr == nil {
		lastErr = errors.New("no proxy route available")
	}
	http.Error(w, lastErr.Error(), http.StatusBadGateway)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	// The semaphore is released before entering the long-lived relay phase.
	// Use a flag to ensure exactly one release regardless of exit path.
	semReleased := false
	releaseSem := func() {
		if !semReleased {
			semReleased = true
			<-s.sem
		}
	}
	defer releaseSem()

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Settings.SockTimeout*2)
	defer cancel()
	route, err := s.resolveRoute(ctx, r)
	if err != nil {
		s.logger.Debug("CONNECT route resolution failed", "host", r.Host, "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var upstream net.Conn
	if route.Direct {
		s.logger.Debug("CONNECT direct", "host", targetHostPort(r))
		upstream, err = net.DialTimeout("tcp", targetHostPort(r), s.cfg.Settings.SockTimeout)
	} else {
		s.logger.Debug("CONNECT via proxy", "host", targetHostPort(r), "proxies", route.Proxies)
		for _, proxyAddr := range route.Proxies {
			upstream, err = s.connectViaProxy(ctx, r, proxyAddr)
			if err == nil {
				break
			}
			s.logger.Debug("CONNECT proxy attempt failed", "proxy", proxyAddr, "err", err)
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, clientRW, err := hj.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	_, _ = clientRW.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
	_ = clientRW.Flush()
	// Release semaphore before relay — tunnel setup is done, the relay is just
	// bidirectional I/O copying and must not hold a concurrency slot.
	releaseSem()
	Relay(clientConn, upstream, s.cfg.Settings.Idle)
}

func (s *Server) resolveRoute(ctx context.Context, r *http.Request) (route, error) {
	host := r.URL.Hostname()
	if host == "" {
		host = strings.TrimSuffix(r.Host, ":443")
	}
	if s.noproxy != nil && s.noproxy.MatchHost(ctx, host, network.DefaultResolver) {
		s.logger.Debug("route: noproxy match", "host", host)
		return route{Direct: true}, nil
	}
	if p := s.getPAC(); p != nil {
		result := p.FindProxyForURL(ctx, absoluteURL(r), host)
		parsed := parsePACResult(result)
		s.logger.Debug("route: PAC result", "host", host, "raw", result, "proxies", parsed)
		if len(parsed) == 0 {
			return route{Direct: true}, nil
		}
		return route{Proxies: parsed}, nil
	}
	if len(s.cfg.Proxy.Server) > 0 {
		s.logger.Debug("route: configured servers", "host", host, "servers", s.cfg.Proxy.Server)
		return route{Proxies: s.cfg.Proxy.Server}, nil
	}
	info, err := s.platform.LoadProxyInfo(ctx, absoluteURL(r))
	if err == nil {
		s.logger.Debug("route: platform proxy info", "pac", info.PAC, "servers", info.Servers)
		if info.PAC != "" {
			p := s.ensurePlatformPAC(info.PAC)
			result := p.FindProxyForURL(ctx, absoluteURL(r), host)
			parsed := parsePACResult(result)
			s.logger.Debug("route: PAC (from platform) result", "host", host, "raw", result, "proxies", parsed)
			if len(parsed) == 0 {
				return route{Direct: true}, nil
			}
			return route{Proxies: parsed}, nil
		}
		if len(info.Servers) > 0 {
			s.logger.Debug("route: platform servers", "host", host, "servers", info.Servers)
			return route{Proxies: info.Servers}, nil
		}
	} else {
		s.logger.Debug("route: platform proxy discovery failed", "err", err)
	}
	s.logger.Debug("route: direct (no upstream)", "host", host)
	return route{Direct: true}, nil
}

func (s *Server) getPAC() *pac.Evaluator {
	s.pacMu.Lock()
	defer s.pacMu.Unlock()
	return s.pac
}

func (s *Server) ensurePlatformPAC(source string) *pac.Evaluator {
	s.pacMu.Lock()
	defer s.pacMu.Unlock()
	// First discovered PAC URL wins for the process lifetime; later platform
	// updates with a different PAC path are ignored until restart.
	if s.pac == nil {
		s.logger.Info("discovered PAC from platform", "pac", source)
		s.pac = pac.New(source, s.cfg.Proxy.PACEncoding, s.cfg.Settings.ProxyReload, s.logger)
	}
	return s.pac
}

func (s *Server) roundTripDirect(ctx context.Context, r *http.Request, body []byte) (*http.Response, error) {
	req := cloneForTransport(r, body)
	if s.cfg.Proxy.UserAgent != "" {
		req.Header.Set("User-Agent", s.cfg.Proxy.UserAgent)
	}
	return s.directTransport.RoundTrip(req.WithContext(ctx))
}

func (s *Server) roundTripProxy(ctx context.Context, r *http.Request, body []byte, proxyAddr string) (*http.Response, error) {
	conn, err := net.DialTimeout("tcp", normalizeProxyAddr(proxyAddr), s.cfg.Settings.SockTimeout)
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	var session auth.Session
	defer func() {
		if session != nil {
			_ = session.Close()
		}
	}()
	var authHeader string
	for attempt := 0; attempt < 4; attempt++ {
		req := cloneForWrite(r, body, authHeader, strings.EqualFold(s.cfg.Proxy.Auth, "NONE"))
		if s.cfg.Proxy.UserAgent != "" {
			req.Header.Set("User-Agent", s.cfg.Proxy.UserAgent)
		}
		if err := req.WriteProxy(conn); err != nil {
			_ = conn.Close()
			return nil, err
		}
		resp, err := http.ReadResponse(reader, req)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		s.logger.Debug("upstream HTTP proxy response", "status", resp.StatusCode, "attempt", attempt, "proxy", proxyAddr)
		if resp.StatusCode != http.StatusProxyAuthRequired || strings.EqualFold(s.cfg.Proxy.Auth, "NONE") {
			resp.Body = &connBodyCloser{ReadCloser: resp.Body, conn: conn}
			return resp, nil
		}
		challenges := resp.Header.Values("Proxy-Authenticate")
		if session == nil {
			scheme, challenge, ok := auth.ChooseScheme(s.cfg.Proxy.Auth, challenges)
			if !ok {
				resp.Body = &connBodyCloser{ReadCloser: resp.Body, conn: conn}
				return resp, nil
			}
			s.logger.Debug("upstream HTTP auth", "scheme", scheme, "proxy", proxyAddr, "target", normalizeProxyAddr(proxyAddr))
			session, err = s.upstream.NewSession(scheme, proxyAddr)
			if err != nil {
				_ = conn.Close()
				return nil, err
			}
			authHeader, _, err = session.Token(ctx, r, challenge)
			if err != nil {
				_ = conn.Close()
				return nil, err
			}
		} else {
			authHeader, _, err = session.Token(ctx, r, challengeForScheme(challenges, session.Scheme()))
			if err != nil {
				_ = conn.Close()
				return nil, err
			}
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	_ = conn.Close()
	return nil, errors.New("proxy authentication failed")
}

func (s *Server) connectViaProxy(ctx context.Context, r *http.Request, proxyAddr string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", normalizeProxyAddr(proxyAddr), s.cfg.Settings.SockTimeout)
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	var session auth.Session
	defer func() {
		if session != nil {
			_ = session.Close()
		}
	}()
	var authHeader string
	for attempt := 0; attempt < 4; attempt++ {
		if err := writeConnectRequest(conn, r, authHeader, strings.EqualFold(s.cfg.Proxy.Auth, "NONE")); err != nil {
			_ = conn.Close()
			return nil, err
		}
		resp, err := http.ReadResponse(reader, r)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		s.logger.Debug("CONNECT proxy response", "status", resp.StatusCode, "attempt", attempt, "proxy", proxyAddr)
		if resp.StatusCode == http.StatusOK {
			return conn, nil
		}
		if resp.StatusCode != http.StatusProxyAuthRequired || strings.EqualFold(s.cfg.Proxy.Auth, "NONE") {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			_ = conn.Close()
			return nil, fmt.Errorf("proxy connect failed: %s", resp.Status)
		}
		challenges := resp.Header.Values("Proxy-Authenticate")
		if session == nil {
			scheme, challenge, ok := auth.ChooseScheme(s.cfg.Proxy.Auth, challenges)
			if !ok {
				return nil, errors.New("no compatible upstream auth scheme")
			}
			s.logger.Debug("upstream CONNECT auth", "scheme", scheme, "proxy", proxyAddr, "target", normalizeProxyAddr(proxyAddr))
			session, err = s.upstream.NewSession(scheme, proxyAddr)
			if err != nil {
				return nil, err
			}
			authHeader, _, err = session.Token(ctx, r, challenge)
			if err != nil {
				return nil, err
			}
		} else {
			authHeader, _, err = session.Token(ctx, r, challengeForScheme(challenges, session.Scheme()))
			if err != nil {
				return nil, err
			}
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	_ = conn.Close()
	return nil, errors.New("proxy connect authentication failed")
}

func challengeForScheme(challenges []string, scheme string) string {
	for _, value := range challenges {
		parts := strings.SplitN(strings.TrimSpace(value), " ", 2)
		if len(parts) > 0 && strings.EqualFold(parts[0], scheme) {
			if len(parts) == 2 {
				return parts[1]
			}
			return ""
		}
	}
	return ""
}

func absoluteURL(r *http.Request) string {
	if r.URL != nil && r.URL.IsAbs() {
		return r.URL.String()
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: r.Host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}).String()
}

func parsePACResult(raw string) []string {
	// PAC results use semicolons as separators (e.g. "PROXY host:port; DIRECT")
	// but some implementations may use commas
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ',' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || strings.EqualFold(part, "DIRECT") {
			continue
		}
		// Strip PAC type prefixes: "PROXY host:port", "HTTP host:port", etc.
		if idx := strings.IndexByte(part, ' '); idx >= 0 {
			part = strings.TrimSpace(part[idx+1:])
		}
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizeProxyAddr(raw string) string {
	// Strip PAC-style prefixes (e.g. "PROXY host:port")
	if idx := strings.IndexByte(raw, ' '); idx >= 0 {
		prefix := strings.ToUpper(raw[:idx])
		if prefix == "PROXY" || prefix == "HTTP" || prefix == "HTTPS" || prefix == "SOCKS" || prefix == "SOCKS5" {
			raw = strings.TrimSpace(raw[idx+1:])
		}
	}
	if strings.Contains(raw, "://") {
		if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
			return parsed.Host
		}
	}
	return raw
}

func cloneForTransport(r *http.Request, body []byte) *http.Request {
	clone := r.Clone(r.Context())
	clone.RequestURI = ""
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.Header = clone.Header.Clone()
	removeHopRequestHeaders(clone.Header)
	if clone.URL != nil && !clone.URL.IsAbs() {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		clone.URL = &url.URL{Scheme: scheme, Host: r.Host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
	}
	return clone
}

func cloneForWrite(r *http.Request, body []byte, authHeader string, preserveClientProxyAuth bool) *http.Request {
	clone := r.Clone(r.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.Header = clone.Header.Clone()
	removeHopRequestHeaders(clone.Header)
	if preserveClientProxyAuth && r.Header.Get("Proxy-Authorization") != "" && authHeader == "" {
		clone.Header.Set("Proxy-Authorization", r.Header.Get("Proxy-Authorization"))
	}
	if authHeader != "" {
		clone.Header.Set("Proxy-Authorization", authHeader)
	}
	if clone.URL != nil && clone.URL.Scheme == "" {
		clone.URL = &url.URL{Scheme: "http", Host: r.Host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
	}
	return clone
}

func writeConnectRequest(conn net.Conn, r *http.Request, authHeader string, preserveClientProxyAuth bool) error {
	builder := &strings.Builder{}
	target := targetHostPort(r)
	fmt.Fprintf(builder, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	for key, values := range r.Header {
		if hopHeader(key) {
			continue
		}
		if strings.EqualFold(key, "Proxy-Authorization") && (!preserveClientProxyAuth || authHeader != "") {
			continue
		}
		for _, value := range values {
			fmt.Fprintf(builder, "%s: %s\r\n", key, value)
		}
	}
	if authHeader != "" {
		fmt.Fprintf(builder, "Proxy-Authorization: %s\r\n", authHeader)
	}
	builder.WriteString("\r\n")
	_, err := io.WriteString(conn, builder.String())
	return err
}

func targetHostPort(r *http.Request) string {
	if r.Host != "" {
		return r.Host
	}
	if r.URL != nil {
		return r.URL.Host
	}
	return ""
}

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	removeHopResponseHeaders(resp.Header)
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func removeHopRequestHeaders(header http.Header) {
	for _, key := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailers", "Transfer-Encoding", "Upgrade"} {
		header.Del(key)
	}
}

func removeHopResponseHeaders(header http.Header) {
	for _, key := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authorization", "Te", "Trailers", "Transfer-Encoding", "Upgrade"} {
		header.Del(key)
	}
}

func hopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailers", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}
