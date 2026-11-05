package tmdb

import (
	"container/list"
	"sync"
	"time"
)

// respCache is an LRU cache of response bodies with a per-entry TTL, bounded in
// entries and bytes. Values are shared and must be treated as read-only.
type respCache struct {
	mu         sync.Mutex
	maxEntries int
	maxBytes   int
	bytes      int
	ll         *list.List // front = most recently used
	m          map[string]*list.Element
}

type cacheEntry struct {
	key string
	val []byte
	exp time.Time
}

func newRespCache(maxEntries, maxBytes int) *respCache {
	return &respCache{maxEntries: maxEntries, maxBytes: maxBytes, ll: list.New(), m: make(map[string]*list.Element)}
}

func (c *respCache) get(key string, now time.Time) ([]byte, bool) {
	if c.maxEntries <= 0 {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*cacheEntry)
	if !now.Before(e.exp) {
		c.remove(el)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

func (c *respCache) put(key string, val []byte, ttl time.Duration, now time.Time) {
	if c.maxEntries <= 0 || ttl <= 0 || len(val) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[key]; ok {
		c.remove(el)
	}
	c.m[key] = c.ll.PushFront(&cacheEntry{key: key, val: val, exp: now.Add(ttl)})
	c.bytes += len(val)
	for c.ll.Len() > c.maxEntries || c.bytes > c.maxBytes {
		c.remove(c.ll.Back())
	}
}

func (c *respCache) remove(el *list.Element) {
	e := c.ll.Remove(el).(*cacheEntry)
	delete(c.m, e.key)
	c.bytes -= len(e.val)
}

func (c *respCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
