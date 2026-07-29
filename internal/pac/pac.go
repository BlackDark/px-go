package pac

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BlackDark/px-go/internal/config"
	"github.com/dop251/goja"
)

const defaultPoolSize = 4

type pacSlot struct {
	runtime  *goja.Runtime
	callable goja.Callable
}

type runtimePool struct {
	slots chan *pacSlot
	gen   uint64 // cache generation at pool publish time
}

type Evaluator struct {
	source         string
	encoding       string
	reloadInterval time.Duration
	client         *http.Client
	logger         *slog.Logger
	poolSize       int

	mu        sync.Mutex
	loadWait  *sync.Cond
	lastLoad  time.Time
	reloading bool
	closed    bool
	pool      atomic.Pointer[runtimePool]
	cache     *resultCache

	// onSlotHeld is an optional test hook invoked while a pool slot is checked out.
	onSlotHeld func()
	// onBeforeCheckout is an optional test hook invoked after pool Load, before taking a slot.
	onBeforeCheckout func(p *runtimePool)
}

func New(source, encoding string, reloadInterval time.Duration, logger *slog.Logger) *Evaluator {
	if encoding == "" {
		encoding = "utf-8"
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	e := &Evaluator{
		source:         source,
		encoding:       encoding,
		reloadInterval: reloadInterval,
		client:         &http.Client{Timeout: 15 * time.Second},
		logger:         logger,
		poolSize:       defaultPoolSize,
		cache:          newResultCache(defaultCacheTTL, defaultCacheCap),
	}
	e.loadWait = sync.NewCond(&e.mu)
	return e
}

func (e *Evaluator) FindProxyForURL(ctx context.Context, rawURL, host string) string {
	key := cacheKey(rawURL, host)
	// Serve cache without waiting on soft reload.
	if cached, ok := e.cache.get(key); ok {
		e.kickReloadIfStale()
		return cached
	}
	e.ensureLoaded(ctx)
	if cached, ok := e.cache.get(key); ok {
		return cached
	}
	p := e.pool.Load()
	if p == nil {
		return "DIRECT"
	}
	if e.onBeforeCheckout != nil {
		e.onBeforeCheckout(p)
	}
	slot := <-p.slots
	defer func() { p.slots <- slot }()
	if e.onSlotHeld != nil {
		e.onSlotHeld()
	}
	result, err := slot.callable(goja.Undefined(), slot.runtime.ToValue(rawURL), slot.runtime.ToValue(host))
	if err != nil {
		e.logger.Debug("FindProxyForURL failed", "err", err)
		return "DIRECT"
	}
	out := normalizeProxyResult(result.String())
	// Bind put to the pool's generation so a concurrent reload cannot accept a stale result.
	e.cache.putIfGen(key, out, p.gen)
	return out
}

func (e *Evaluator) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	e.pool.Store(nil)
	e.loadWait.Broadcast()
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

func (e *Evaluator) staleLocked() bool {
	return e.reloadInterval > 0 && time.Since(e.lastLoad) >= e.reloadInterval
}

func (e *Evaluator) loadedLocked() bool {
	return e.pool.Load() != nil
}

func (e *Evaluator) kickReloadIfStale() {
	e.mu.Lock()
	if e.closed || !e.loadedLocked() || !e.staleLocked() || e.reloading {
		e.mu.Unlock()
		return
	}
	e.reloading = true
	e.mu.Unlock()
	go e.backgroundReload()
}

// ensureLoaded fetches/parses PAC outside the eval critical section.
// Soft reload runs asynchronously so routing is never blocked on fetch.
// Cold start: waiters block on loadWait until the first load finishes.
func (e *Evaluator) ensureLoaded(ctx context.Context) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	for !e.closed && !e.loadedLocked() && e.reloading {
		e.loadWait.Wait()
	}
	if e.closed {
		e.mu.Unlock()
		return
	}
	if e.loadedLocked() {
		if e.staleLocked() && !e.reloading {
			e.reloading = true
			e.mu.Unlock()
			go e.backgroundReload()
			return
		}
		e.mu.Unlock()
		return
	}
	e.reloading = true
	e.mu.Unlock()
	if err := e.loadAndSwap(ctx); err != nil {
		e.logger.Debug("pac load failed", "err", err)
	}
	e.mu.Lock()
	e.reloading = false
	e.loadWait.Broadcast()
	e.mu.Unlock()
}

func (e *Evaluator) backgroundReload() {
	// Timeout is applied inside loadAndSwap via WithoutCancel + client timeout.
	if err := e.loadAndSwap(context.Background()); err != nil {
		e.logger.Debug("pac reload failed", "err", err)
		e.mu.Lock()
		if !e.closed {
			// Keep old pool; advance lastLoad so we don't hammer the source.
			e.lastLoad = time.Now()
		}
		e.reloading = false
		e.loadWait.Broadcast()
		e.mu.Unlock()
		return
	}
	e.mu.Lock()
	e.reloading = false
	e.loadWait.Broadcast()
	e.mu.Unlock()
}

func (e *Evaluator) loadAndSwap(ctx context.Context) error {
	// Detach from request cancellation so one client cancel doesn't abort a
	// shared reload/cold-load that other goroutines may be waiting on.
	timeout := e.client.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	data, err := e.readSource(loadCtx)
	if err != nil {
		return err
	}
	text, err := decodeText(data, e.encoding)
	if err != nil {
		return err
	}
	p, err := e.buildPool(text)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.cache.invalidate()
	p.gen = e.cache.generation()
	e.pool.Store(p)
	e.lastLoad = time.Now()
	return nil
}

func (e *Evaluator) buildPool(text string) (*runtimePool, error) {
	size := e.poolSize
	if size <= 0 {
		size = defaultPoolSize
	}
	p := &runtimePool{slots: make(chan *pacSlot, size)}
	for i := 0; i < size; i++ {
		runtime, fn, err := e.compile(text)
		if err != nil {
			return nil, err
		}
		p.slots <- &pacSlot{runtime: runtime, callable: fn}
	}
	return p, nil
}

func (e *Evaluator) compile(text string) (*goja.Runtime, goja.Callable, error) {
	runtime := goja.New()
	_ = runtime.Set("alert", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = runtime.Set("dnsResolve", func(host string) string { return e.dnsResolve(host) })
	_ = runtime.Set("myIpAddress", func() string { return e.myIPAddress() })
	if _, err := runtime.RunString(PACUtils + "\n" + text); err != nil {
		return nil, nil, err
	}
	fn, ok := goja.AssertFunction(runtime.Get("FindProxyForURL"))
	if !ok {
		return nil, nil, os.ErrInvalid
	}
	return runtime, fn, nil
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
		if resp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil, fmt.Errorf("pac fetch: http %d", resp.StatusCode)
		}
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
