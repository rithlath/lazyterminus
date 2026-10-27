// Package cache is a small concurrency-safe TTL cache for Terminus read
// results.
package cache

import (
	"strings"
	"sync"
	"time"
)

type entry struct {
	v       any
	expires time.Time
}

// Cache maps string keys to values that expire after a per-entry TTL.
type Cache struct {
	Now func() time.Time

	mu sync.Mutex
	m  map[string]entry
}

// New returns an empty cache.
func New() *Cache { return &Cache{m: map[string]entry{}} }

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Get returns the value for key if present and not expired.
func (c *Cache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || !c.now().Before(e.expires) {
		return nil, false
	}
	return e.v, true
}

// Set stores v under key for ttl. A non-positive ttl stores nothing.
func (c *Cache) Set(key string, v any, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = entry{v: v, expires: c.now().Add(ttl)}
}

// Delete removes key.
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}

// DeletePrefix removes every key starting with prefix.
func (c *Cache) DeletePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.m {
		if strings.HasPrefix(k, prefix) {
			delete(c.m, k)
		}
	}
}

// Typed fetches key from c, or calls load and caches its result for ttl on
// success. force skips the cached value.
func Typed[T any](c *Cache, key string, ttl time.Duration, force bool, load func() (T, error)) (T, error) {
	if !force {
		if v, ok := c.Get(key); ok {
			if t, ok := v.(T); ok {
				return t, nil
			}
		}
	}
	v, err := load()
	if err == nil {
		c.Set(key, v, ttl)
	}
	return v, err
}
