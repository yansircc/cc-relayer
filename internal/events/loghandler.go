package events

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"
)

type LogLine struct {
	Level   string         `json:"level"`
	Message string         `json:"msg"`
	Time    time.Time      `json:"ts"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

type LogHandler struct {
	inner       slog.Handler
	mu          sync.RWMutex
	ring        []LogLine
	ringSize    int
	ringPos     int
	ringCount   int
	subscribers map[int]chan LogLine
	nextID      int
	level       slog.Leveler
	attrs       []slog.Attr
	groups      []string
}

func NewLogHandler(level slog.Leveler, ringSize int) *LogHandler {
	if ringSize <= 0 {
		ringSize = 1000
	}
	return &LogHandler{
		inner:       slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}),
		ring:        make([]LogLine, ringSize),
		ringSize:    ringSize,
		subscribers: make(map[int]chan LogLine),
		level:       level,
	}
}

func (h *LogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *LogHandler) Handle(ctx context.Context, r slog.Record) error {
	if err := h.inner.Handle(ctx, r); err != nil {
		return err
	}

	attrs := make(map[string]any)
	prefix := groupPrefix(h.groups)
	for _, a := range h.attrs {
		attrs[prefix+a.Key] = a.Value.Any()
	}
	r.Attrs(func(a slog.Attr) bool {
		attrs[prefix+a.Key] = a.Value.Any()
		return true
	})

	line := LogLine{
		Level:   r.Level.String(),
		Message: r.Message,
		Time:    r.Time,
	}
	if len(attrs) > 0 {
		line.Attrs = attrs
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.ring[h.ringPos] = line
	h.ringPos = (h.ringPos + 1) % h.ringSize
	if h.ringCount < h.ringSize {
		h.ringCount++
	}

	for _, ch := range h.subscribers {
		select {
		case ch <- line:
		default:
		}
	}
	return nil
}

func (h *LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogHandler{
		inner:       h.inner.WithAttrs(attrs),
		ring:        h.ring,
		ringSize:    h.ringSize,
		ringPos:     h.ringPos,
		ringCount:   h.ringCount,
		subscribers: h.subscribers,
		nextID:      h.nextID,
		level:       h.level,
		attrs:       append(cloneAttrs(h.attrs), attrs...),
		groups:      h.groups,
		mu:          sync.RWMutex{},
	}
}

func (h *LogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &LogHandler{
		inner:       h.inner.WithGroup(name),
		ring:        h.ring,
		ringSize:    h.ringSize,
		ringPos:     h.ringPos,
		ringCount:   h.ringCount,
		subscribers: h.subscribers,
		nextID:      h.nextID,
		level:       h.level,
		attrs:       cloneAttrs(h.attrs),
		groups:      append(append([]string{}, h.groups...), name),
		mu:          sync.RWMutex{},
	}
}

func (h *LogHandler) Subscribe() (id int, ch <-chan LogLine, recent []LogLine) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c := make(chan LogLine, 64)
	id = h.nextID
	h.nextID++
	h.subscribers[id] = c

	recent = h.recentLocked()
	return id, c, recent
}

func (h *LogHandler) Unsubscribe(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ch, ok := h.subscribers[id]; ok {
		delete(h.subscribers, id)
		close(ch)
	}
}

func (h *LogHandler) recentLocked() []LogLine {
	if h.ringCount == 0 {
		return nil
	}
	result := make([]LogLine, h.ringCount)
	start := (h.ringPos - h.ringCount + h.ringSize) % h.ringSize
	for i := range h.ringCount {
		result[i] = h.ring[(start+i)%h.ringSize]
	}
	return result
}

func groupPrefix(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	var p string
	for _, g := range groups {
		p += g + "."
	}
	return p
}

func cloneAttrs(attrs []slog.Attr) []slog.Attr {
	if len(attrs) == 0 {
		return nil
	}
	c := make([]slog.Attr, len(attrs))
	copy(c, attrs)
	return c
}
