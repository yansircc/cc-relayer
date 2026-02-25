package events

import (
	"sync"
	"time"
)

type EventType string

const (
	EventBan       EventType = "ban"
	EventRefresh   EventType = "refresh"
	EventRateLimit EventType = "ratelimit"
	EventRecover   EventType = "recover"
	EventFiveHStop EventType = "5h_stop"
	EventOverload  EventType = "overload"
	EventRequest   EventType = "request"
)

type Event struct {
	Type      EventType `json:"type"`
	AccountID string    `json:"account_id,omitempty"`
	UserID    string    `json:"user_id,omitempty"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"ts"`
}

type Bus struct {
	mu          sync.RWMutex
	ring        []Event
	ringSize    int
	ringPos     int
	ringCount   int
	subscribers map[int]chan Event
	nextID      int
}

func NewBus(ringSize int) *Bus {
	if ringSize <= 0 {
		ringSize = 200
	}
	return &Bus{
		ring:        make([]Event, ringSize),
		ringSize:    ringSize,
		subscribers: make(map[int]chan Event),
	}
}

func (b *Bus) Publish(e Event) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.ring[b.ringPos] = e
	b.ringPos = (b.ringPos + 1) % b.ringSize
	if b.ringCount < b.ringSize {
		b.ringCount++
	}

	for _, ch := range b.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
}

func (b *Bus) Subscribe() (id int, ch <-chan Event, recent []Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	c := make(chan Event, 64)
	id = b.nextID
	b.nextID++
	b.subscribers[id] = c

	recent = b.recentLocked()
	return id, c, recent
}

func (b *Bus) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if ch, ok := b.subscribers[id]; ok {
		delete(b.subscribers, id)
		close(ch)
	}
}

func (b *Bus) recentLocked() []Event {
	if b.ringCount == 0 {
		return nil
	}
	result := make([]Event, b.ringCount)
	start := (b.ringPos - b.ringCount + b.ringSize) % b.ringSize
	for i := range b.ringCount {
		result[i] = b.ring[(start+i)%b.ringSize]
	}
	return result
}
