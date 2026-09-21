package modules

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
)

// RecordSubscriber represents an active SSE client listening for matching records.
type RecordSubscriber struct {
	Ch         chan *Record
	Owner      string
	Collection string
	Label      string
	Search     string
}

// Matches checks whether a record satisfies the subscriber's filter criteria.
func (s *RecordSubscriber) Matches(rec *Record) bool {
	if rec == nil {
		return false
	}
	if s.Owner != "" && rec.Owner != s.Owner {
		return false
	}
	if s.Collection != "" && rec.Collection != s.Collection {
		return false
	}
	if s.Label != "" {
		found := false
		for _, l := range rec.Labels {
			if l == s.Label {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if s.Search != "" {
		var dataStr string
		if len(rec.Data) > 0 {
			dataStr = string(rec.Data)
		}
		if !strings.Contains(strings.ToLower(dataStr), strings.ToLower(s.Search)) {
			return false
		}
	}
	return true
}

// RecordBroadcaster manages real-time fan-out of published records to active SSE subscribers.
type RecordBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[*RecordSubscriber]struct{}
	// dropped counts records skipped because a subscriber's buffer was
	// full (slow consumer). Exposed via /metrics so silent record loss
	// is visible to operators.
	dropped atomic.Int64
}

// NewRecordBroadcaster creates an empty thread-safe broadcaster.
func NewRecordBroadcaster() *RecordBroadcaster {
	return &RecordBroadcaster{
		subscribers: make(map[*RecordSubscriber]struct{}),
	}
}

// Subscribe registers a new subscriber.
func (b *RecordBroadcaster) Subscribe(sub *RecordSubscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers[sub] = struct{}{}
}

// TrySubscribe registers a new subscriber but refuses (returning false)
// when the configured per-node SSE cap is already reached. The check and
// insert happen under the same lock, so concurrent joins can't overshoot.
func (b *RecordBroadcaster) TrySubscribe(sub *RecordSubscriber) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if max := MaxSSESubscribers; max > 0 && len(b.subscribers) >= max {
		return false
	}
	b.subscribers[sub] = struct{}{}
	return true
}

// Unsubscribe removes an active subscriber.
func (b *RecordBroadcaster) Unsubscribe(sub *RecordSubscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subscribers, sub)
}

// SubscriberCount returns the current number of active subscribers.
func (b *RecordBroadcaster) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// Broadcast distributes a newly stored record to all matching subscribers non-blockingly.
func (b *RecordBroadcaster) Broadcast(rec *Record) {
	if rec == nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()

	for sub := range b.subscribers {
		if sub.Matches(rec) {
			select {
			case sub.Ch <- rec:
			default:
				// Slow consumer buffer saturated — count and skip to
				// prevent blocking fan-out.
				b.dropped.Add(1)
			}
		}
	}
}

// Dropped returns how many records were skipped for slow consumers.
func (b *RecordBroadcaster) Dropped() int64 {
	return b.dropped.Load()
}

// FormatSSE formats a Record into a standard Server-Sent Event block.
func FormatSSE(rec *Record) ([]byte, error) {
	data, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	res := "event: record\nid: " + rec.ID + "\ndata: " + string(data) + "\n\n"
	return []byte(res), nil
}
