package transport

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/yansir/cc-relayer/internal/account"
)

type poolEntry struct {
	transport *http.Transport
	lastUsed  time.Time
}

// Pool manages per-account HTTP transports with idle cleanup.
type Pool struct {
	mu      sync.Mutex
	entries map[string]*poolEntry
}

func newPool() *Pool {
	return &Pool{
		entries: make(map[string]*poolEntry),
	}
}

// Get returns or creates an HTTP transport for the given account.
func (p *Pool) Get(acct *account.Account) *http.Transport {
	key := transportKey(acct)

	p.mu.Lock()
	defer p.mu.Unlock()

	if entry, ok := p.entries[key]; ok {
		entry.lastUsed = time.Now()
		return entry.transport
	}

	t := buildTransport(acct)
	p.entries[key] = &poolEntry{
		transport: t,
		lastUsed:  time.Now(),
	}
	return t
}

// RunCleanup periodically removes transports idle longer than idleTimeout.
func (p *Pool) RunCleanup(ctx context.Context, interval, idleTimeout time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.cleanup(idleTimeout)
		}
	}
}

func (p *Pool) cleanup(idleTimeout time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	cutoff := time.Now().Add(-idleTimeout)
	for key, entry := range p.entries {
		if entry.lastUsed.Before(cutoff) {
			entry.transport.CloseIdleConnections()
			delete(p.entries, key)
		}
	}
}

// Close closes all transports in the pool.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for key, entry := range p.entries {
		entry.transport.CloseIdleConnections()
		delete(p.entries, key)
	}
}

// transportKey returns a unique key for the account's transport configuration.
func transportKey(acct *account.Account) string {
	if acct.Proxy == nil {
		return "direct"
	}
	return fmt.Sprintf("%s://%s:%d", acct.Proxy.Type, acct.Proxy.Host, acct.Proxy.Port)
}
