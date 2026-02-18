package transport

import (
	"context"
	"net/http"
	"time"

	"github.com/yansir/claude-relay/internal/account"
	"github.com/yansir/claude-relay/internal/config"
)

// Manager provides per-account HTTP clients and transports with utls fingerprinting.
type Manager struct {
	pool           *Pool
	requestTimeout time.Duration
}

// NewManager creates a new transport Manager.
func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		pool:           newPool(),
		requestTimeout: cfg.RequestTimeout,
	}
}

// GetClient returns an http.Client with a per-account transport (utls + optional proxy).
func (m *Manager) GetClient(acct *account.Account) *http.Client {
	return &http.Client{
		Transport: m.pool.Get(acct),
		Timeout:   m.requestTimeout,
	}
}

// GetHTTPTransport returns the raw http.Transport for an account (used by token refresh).
func (m *Manager) GetHTTPTransport(acct *account.Account) *http.Transport {
	return m.pool.Get(acct)
}

// RunCleanup starts the background cleanup goroutine. Blocks until ctx is canceled.
func (m *Manager) RunCleanup(ctx context.Context) {
	m.pool.RunCleanup(ctx, 1*time.Minute, 5*time.Minute)
}

// Close closes all pooled transports.
func (m *Manager) Close() {
	m.pool.Close()
}

// buildTransport creates an http.Transport with utls TLS and optional proxy support.
func buildTransport(acct *account.Account) *http.Transport {
	t := &http.Transport{
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     5 * time.Minute,
		ForceAttemptHTTP2:   true,
	}

	if acct.Proxy != nil {
		t.DialTLSContext = proxyDialer(acct.Proxy)
	} else {
		t.DialTLSContext = dialUTLS
	}

	return t
}
