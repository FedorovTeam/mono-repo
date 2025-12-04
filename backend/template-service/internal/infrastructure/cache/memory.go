package cache

import (
	"sync"
	"time"
)

type MemoryCache struct {
	mu    sync.Mutex
	store map[string]cacheItem
}

type cacheItem struct {
	value     string
	expiresAt int64
}

func NewMemoryCache() *MemoryCache {
	return &MemoryCache{store: make(map[string]cacheItem)}
}

func (c *MemoryCache) Set(key, value string, ttl int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key] = cacheItem{value: value, expiresAt: time.Now().Unix() + ttl}
	return nil
}

func (c *MemoryCache) Get(key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.store[key]
	if !ok || item.expiresAt < time.Now().Unix() {
		return "", nil
	}
	return item.value, nil
}
