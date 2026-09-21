package p2p

import (
	"sync"
	"time"
)

// SeenCache keeps a bounded set of recent event IDs and blob hashes
// to prevent infinite forwarding loops and duplicate processing across the swarm.
type SeenCache struct {
	mu         sync.Mutex
	items      map[string]int64
	queue      []string
	maxEntries int
}

// NewSeenCache creates a thread-safe seen cache bounded to maxEntries.
func NewSeenCache(maxEntries int) *SeenCache {
	if maxEntries <= 0 {
		maxEntries = DefaultGossipCacheSize
	}
	return &SeenCache{
		items:      make(map[string]int64, maxEntries),
		queue:      make([]string, 0, maxEntries),
		maxEntries: maxEntries,
	}
}

// Add checks if the key was already seen.
// If it is new, it records it and returns true.
// If it was already seen, it returns false.
func (sc *SeenCache) Add(key string) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	now := time.Now().Unix()
	if _, exists := sc.items[key]; exists {
		sc.items[key] = now
		return false
	}

	if len(sc.queue) >= sc.maxEntries {
		// Evict oldest from queue
		oldest := sc.queue[0]
		sc.queue = sc.queue[1:]
		delete(sc.items, oldest)
	}

	sc.items[key] = now
	sc.queue = append(sc.queue, key)
	return true
}

// Has checks if the key exists in the seen cache without adding it.
func (sc *SeenCache) Has(key string) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	_, exists := sc.items[key]
	return exists
}

// Len returns the current count of items in the seen cache.
func (sc *SeenCache) Len() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return len(sc.items)
}
