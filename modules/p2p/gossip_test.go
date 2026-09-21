package p2p

import (
	"fmt"
	"testing"
)

func TestSeenCacheDeduplicationAndEviction(t *testing.T) {
	cache := NewSeenCache(5)

	// Add 5 items
	for i := 1; i <= 5; i++ {
		key := fmt.Sprintf("event-%d", i)
		if !cache.Add(key) {
			t.Fatalf("expected key %s to be new", key)
		}
	}

	// Re-adding existing item should return false
	if cache.Add("event-3") {
		t.Fatalf("expected event-3 to be marked as duplicate")
	}

	// Add 6th item, should evict event-1
	if !cache.Add("event-6") {
		t.Fatalf("expected event-6 to be new")
	}

	if cache.Has("event-1") {
		t.Fatalf("expected event-1 to have been evicted")
	}
	if !cache.Has("event-6") {
		t.Fatalf("expected event-6 to be present")
	}
	if cache.Len() != 5 {
		t.Fatalf("expected cache length 5, got %d", cache.Len())
	}
}
