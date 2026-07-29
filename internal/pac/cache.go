package pac

import (
	"container/list"
	"sync"
	"time"
)

const (
	defaultCacheTTL = 30 * time.Second
	defaultCacheCap = 4096
)

type cacheEntry struct {
	key     string
	result  string
	expires time.Time
	gen     uint64
}

type resultCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	cap     int
	gen     uint64
	items   map[string]*list.Element
	order   *list.List // front = most recently used
}

func newResultCache(ttl time.Duration, cap int) *resultCache {
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	if cap <= 0 {
		cap = defaultCacheCap
	}
	return &resultCache{
		ttl:   ttl,
		cap:   cap,
		items: make(map[string]*list.Element),
		order: list.New(),
	}
}

func cacheKey(rawURL, host string) string {
	return rawURL + "\x00" + host
}

func (c *resultCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	ent := el.Value.(*cacheEntry)
	if ent.gen != c.gen || time.Now().After(ent.expires) {
		c.removeElement(el)
		return "", false
	}
	c.order.MoveToFront(el)
	return ent.result, true
}

func (c *resultCache) put(key, result string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		ent := el.Value.(*cacheEntry)
		ent.result = result
		ent.expires = time.Now().Add(c.ttl)
		ent.gen = c.gen
		c.order.MoveToFront(el)
		return
	}
	for c.order.Len() >= c.cap {
		c.removeElement(c.order.Back())
	}
	ent := &cacheEntry{
		key:     key,
		result:  result,
		expires: time.Now().Add(c.ttl),
		gen:     c.gen,
	}
	c.items[key] = c.order.PushFront(ent)
}

func (c *resultCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.items = make(map[string]*list.Element)
	c.order.Init()
}

func (c *resultCache) removeElement(el *list.Element) {
	if el == nil {
		return
	}
	ent := el.Value.(*cacheEntry)
	delete(c.items, ent.key)
	c.order.Remove(el)
}
