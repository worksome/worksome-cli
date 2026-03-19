package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

type cacheEntry struct {
	data      json.RawMessage
	expiresAt time.Time
}

// Cache is a simple in-memory cache for GraphQL query responses keyed by query
// string + variables. Entries expire after the configured TTL.
type Cache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

// NewCache creates a Cache that keeps entries for the given TTL duration.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
	}
}

// key produces a deterministic hash from a query and its variables.
func (c *Cache) key(query string, vars map[string]any) string {
	h := sha256.New()
	h.Write([]byte(query))
	if vars != nil {
		b, _ := json.Marshal(vars)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns the cached response for the given query+variables, if present and
// not expired. The second return value indicates whether a valid entry was found.
func (c *Cache) Get(query string, vars map[string]any) (json.RawMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := c.key(query, vars)
	entry, ok := c.entries[k]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(c.entries, k)
		return nil, false
	}
	return entry.data, true
}

// Set stores a response in the cache for the given query+variables.
func (c *Cache) Set(query string, vars map[string]any, data json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := c.key(query, vars)
	c.entries[k] = cacheEntry{data: data, expiresAt: time.Now().Add(c.ttl)}
}
