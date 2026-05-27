package pac

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/BlackDark/px-go/internal/config"
	"github.com/dop251/goja"
)

type Evaluator struct {
	source         string
	encoding       string
	reloadInterval time.Duration
	client         *http.Client
	logger         *slog.Logger

	mu       sync.Mutex
	lastLoad time.Time
	runtime  *goja.Runtime
	callable goja.Callable
}

func New(source, encoding string, reloadInterval time.Duration, logger *slog.Logger) *Evaluator {
	if encoding == "" {
		encoding = "utf-8"
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Evaluator{
		source:         source,
		encoding:       encoding,
		reloadInterval: reloadInterval,
		client:         &http.Client{Timeout: 15 * time.Second},
		logger:         logger,
	}
}

func (e *Evaluator) FindProxyForURL(ctx context.Context, rawURL, host string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureLoaded(ctx); err != nil {
		e.logger.Debug("pac load failed", "err", err)
		return "DIRECT"
	}
	if e.callable == nil {
		return "DIRECT"
	}
	result, err := e.callable(goja.Undefined(), e.runtime.ToValue(rawURL), e.runtime.ToValue(host))
	if err != nil {
		e.logger.Debug("FindProxyForURL failed", "err", err)
		return "DIRECT"
	}
	return normalizeProxyResult(result.String())
}

func (e *Evaluator) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.runtime = nil
	e.callable = nil
}

func (e *Evaluator) dnsResolve(host string) string {
	ips, err := net.LookupIP(host)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}

func (e *Evaluator) myIPAddress() string {
	ips := config.GetHostIPs()
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return ip.String()
		}
	}
	if len(ips) > 0 {
		return ips[0].String()
	}
	return "127.0.0.1"
}

func (e *Evaluator) ensureLoaded(ctx context.Context) error {
	if e.callable != nil && (e.reloadInterval <= 0 || time.Since(e.lastLoad) < e.reloadInterval) {
		return nil
	}
	data, err := e.readSource(ctx)
	if err != nil {
		return err
	}
	text, err := decodeText(data, e.encoding)
	if err != nil {
		return err
	}
	runtime := goja.New()
	_ = runtime.Set("alert", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = runtime.Set("dnsResolve", func(host string) string { return e.dnsResolve(host) })
	_ = runtime.Set("myIpAddress", func() string { return e.myIPAddress() })
	if _, err := runtime.RunString(PACUtils + "\n" + text); err != nil {
		return err
	}
	fn, ok := goja.AssertFunction(runtime.Get("FindProxyForURL"))
	if !ok {
		return os.ErrInvalid
	}
	e.runtime = runtime
	e.callable = fn
	e.lastLoad = time.Now()
	return nil
}

func (e *Evaluator) readSource(ctx context.Context) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(e.source), "http://") || strings.HasPrefix(strings.ToLower(e.source), "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.source, nil)
		if err != nil {
			return nil, err
		}
		resp, err := e.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		return io.ReadAll(resp.Body)
	}
	path := e.source
	if strings.HasPrefix(strings.ToLower(path), "file://") {
		path = config.FileURLToLocalPath(path)
	}
	return os.ReadFile(path)
}

func decodeText(data []byte, encoding string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf-8", "utf8":
		return string(data), nil
	case "latin-1", "latin1", "iso-8859-1":
		runes := make([]rune, len(data))
		for i, b := range data {
			runes[i] = rune(b)
		}
		return string(runes), nil
	default:
		return string(data), nil
	}
}

func normalizeProxyResult(raw string) string {
	result := strings.TrimSpace(raw)
	if result == "" {
		return "DIRECT"
	}
	for _, scheme := range []string{"PROXY ", "HTTP "} {
		result = strings.ReplaceAll(result, scheme, "")
	}
	for _, scheme := range []string{"HTTPS ", "SOCKS4 ", "SOCKS5 "} {
		prefix := strings.ToLower(strings.TrimSpace(scheme)) + "://"
		result = strings.ReplaceAll(result, scheme, prefix)
	}
	result = strings.ReplaceAll(result, "SOCKS ", "socks5://")
	result = strings.ReplaceAll(result, ";", ",")
	parts := strings.Split(result, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return "DIRECT"
	}
	return strings.Join(out, ",")
}
