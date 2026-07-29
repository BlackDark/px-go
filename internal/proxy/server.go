package proxy

import (
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/BlackDark/px-go/internal/auth"
	"github.com/BlackDark/px-go/internal/clientauth"
	"github.com/BlackDark/px-go/internal/config"
	"github.com/BlackDark/px-go/internal/kerberos"
	"github.com/BlackDark/px-go/internal/network"
	"github.com/BlackDark/px-go/internal/pac"
	"github.com/BlackDark/px-go/internal/platform"
)

type connContextKey struct{}

type Server struct {
	cfg             config.Config
	logger          *slog.Logger
	platform        platform.Interface
	clientAuth      *clientauth.Handler
	allow           *network.Matcher
	noproxy         *network.Matcher
	pac             *pac.Evaluator
	pacMu           sync.Mutex
	kerberos        *kerberos.Manager
	upstream        auth.Factory
	directTransport *http.Transport
	sem             chan struct{}
	locals          map[string]struct{}

	servers  []*http.Server
	connAuth sync.Map
	stopOnce sync.Once
	stopCh   chan struct{}
}

func New(cfg config.Config, logger *slog.Logger) (*Server, error) {
	allow, err := network.Parse(cfg.Proxy.Allow)
	if err != nil {
		return nil, err
	}
	noproxy, err := network.Parse(cfg.Proxy.NoProxy)
	if err != nil {
		return nil, err
	}
	locals := map[string]struct{}{}
	for _, ip := range config.GetHostIPs() {
		locals[ip.String()] = struct{}{}
	}
	creds := auth.CredentialsFromConfig(cfg.Proxy)
	var krb *kerberos.Manager
	if cfg.Proxy.Kerberos && creds.Username != "" && creds.Password != "" {
		manager, err := kerberos.New(creds.Username, creds.Password, logger)
		if err == nil {
			krb = manager
		}
	}
	s := &Server{
		cfg:        cfg,
		logger:     logger,
		platform:   platform.Current(),
		clientAuth: clientauth.New(cfg.Client, logger),
		allow:      allow,
		noproxy:    noproxy,
		kerberos:   krb,
		upstream:   auth.Factory{Credentials: creds, Kerberos: krb, Logger: logger},
		directTransport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: cfg.Settings.SockTimeout}).DialContext,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          256,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       cfg.Settings.Idle,
			TLSHandshakeTimeout:   cfg.Settings.SockTimeout,
			ResponseHeaderTimeout: cfg.Settings.SockTimeout,
		},
		sem:    make(chan struct{}, cfg.Settings.Threads),
		locals: locals,
		stopCh: make(chan struct{}),
	}
	if cfg.Proxy.PAC != "" {
		s.pac = pac.New(cfg.Proxy.PAC, cfg.Proxy.PACEncoding, cfg.Settings.ProxyReload, logger)
	}
	return s, nil
}

func (s *Server) Start(ctx context.Context) error {
	if s.kerberos != nil {
		s.kerberos.Start(ctx)
	}
	errCh := make(chan error, len(s.cfg.ListenAddresses()))
	for _, addr := range s.cfg.ListenAddresses() {
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		httpServer := &http.Server{
			Addr:              addr,
			Handler:           s,
			ReadHeaderTimeout: 30 * time.Second,
			IdleTimeout:       s.cfg.Settings.Idle,
			ConnContext: func(ctx context.Context, c net.Conn) context.Context {
				return context.WithValue(ctx, connContextKey{}, c)
			},
			ConnState: func(c net.Conn, st http.ConnState) {
				if st == http.StateClosed || st == http.StateHijacked {
					if state, ok := s.connAuth.LoadAndDelete(c); ok {
						s.clientAuth.CloseState(state.(*clientauth.State))
					}
				}
			},
			ErrorLog: log.New(io.Discard, "", 0),
		}
		s.servers = append(s.servers, httpServer)
		go func(srv *http.Server, ln net.Listener) {
			s.logger.Info("listening", "addr", ln.Addr().String())
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}(httpServer, listener)
	}
	select {
	case <-ctx.Done():
		return s.Shutdown(context.Background())
	case err := <-errCh:
		return err
	case <-s.stopCh:
		return nil
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	var shutdownErr error
	s.stopOnce.Do(func() {
		close(s.stopCh)
		var wg sync.WaitGroup
		for _, srv := range s.servers {
			wg.Add(1)
			go func(server *http.Server) {
				defer wg.Done()
				if err := server.Shutdown(ctx); err != nil && shutdownErr == nil {
					shutdownErr = err
				}
			}(srv)
		}
		wg.Wait()
		if s.kerberos != nil {
			_ = s.kerberos.Close()
		}
		if p := s.getPAC(); p != nil {
			p.Close()
		}
	})
	return shutdownErr
}

func (s *Server) stateFor(r *http.Request) *clientauth.State {
	conn, _ := r.Context().Value(connContextKey{}).(net.Conn)
	if conn == nil {
		return &clientauth.State{}
	}
	if state, ok := s.connAuth.Load(conn); ok {
		return state.(*clientauth.State)
	}
	state := &clientauth.State{}
	s.connAuth.Store(conn, state)
	return state
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.isClientAllowed(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/health" {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
		return
	}
	if r.URL.Path == "/PxQuit" {
		s.handleQuit(w, r)
		return
	}
	if s.clientAuth.Enabled() {
		ok, headers, err := s.clientAuth.Authenticate(r, s.stateFor(r))
		if err != nil {
			s.logger.Warn("client auth failed", "err", err)
		}
		if !ok {
			for k, values := range headers {
				for _, value := range values {
					w.Header().Add(k, value)
				}
			}
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
	}
	select {
	case s.sem <- struct{}{}:
	default:
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodConnect {
		// handleConnect releases the semaphore internally before relay
		s.handleConnect(w, r)
		return
	}
	defer func() { <-s.sem }()
	s.handleHTTP(w, r)
}

func (s *Server) isClientAllowed(remoteAddr string) bool {
	ip := parseRemoteIP(remoteAddr)
	if !ip.IsValid() {
		return false
	}
	if s.cfg.Proxy.HostOnly && !s.cfg.Proxy.Gateway {
		_, ok := s.locals[ip.String()]
		return ok
	}
	if s.allow == nil || s.allow.MatchIP(ip) {
		return true
	}
	if !s.cfg.Proxy.Gateway {
		_, ok := s.locals[ip.String()]
		return ok
	}
	return false
}

func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	ip := parseRemoteIP(r.RemoteAddr)
	if !ip.IsLoopback() {
		if _, ok := s.locals[ip.String()]; !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "shutting down")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()
}

func parseRemoteIP(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		addr, _ := netip.ParseAddr(remoteAddr)
		return addr.Unmap()
	}
	addr, _ := netip.ParseAddr(host)
	return addr.Unmap()
}
