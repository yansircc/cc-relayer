package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/yansir/cc-relayer/internal/account"
	"github.com/yansir/cc-relayer/internal/config"
	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

// --- Manager (public API) ---

// Manager provides per-account HTTP clients and transports with utls fingerprinting.
type Manager struct {
	mu             sync.Mutex
	entries        map[string]*poolEntry
	requestTimeout time.Duration
}

type poolEntry struct {
	roundTripper http.RoundTripper
	lastUsed     time.Time
}

// NewManager creates a new transport Manager.
func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		entries:        make(map[string]*poolEntry),
		requestTimeout: cfg.RequestTimeout,
	}
}

// GetClient returns an http.Client with a per-account transport (utls + optional proxy).
func (m *Manager) GetClient(acct *account.Account) *http.Client {
	return &http.Client{
		Transport: m.getRoundTripper(acct),
		Timeout:   m.requestTimeout,
	}
}

// GetHTTPTransport returns an http.Transport for proxy scenarios (used by token refresh).
func (m *Manager) GetHTTPTransport(acct *account.Account) *http.Transport {
	if acct.Proxy == nil {
		return nil
	}
	return &http.Transport{
		DialTLSContext: proxyDialer(acct.Proxy),
	}
}

// RunCleanup starts the background cleanup goroutine. Blocks until ctx is canceled.
func (m *Manager) RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.cleanup(5 * time.Minute)
		}
	}
}

// Close closes all pooled transports.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, entry := range m.entries {
		if t, ok := entry.roundTripper.(interface{ CloseIdleConnections() }); ok {
			t.CloseIdleConnections()
		}
		delete(m.entries, key)
	}
}

// --- Pool (internal) ---

func (m *Manager) getRoundTripper(acct *account.Account) http.RoundTripper {
	key := transportKey(acct)

	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.entries[key]; ok {
		entry.lastUsed = time.Now()
		return entry.roundTripper
	}

	rt := buildRoundTripper(acct)
	m.entries[key] = &poolEntry{roundTripper: rt, lastUsed: time.Now()}
	return rt
}

func (m *Manager) cleanup(idleTimeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-idleTimeout)
	for key, entry := range m.entries {
		if entry.lastUsed.Before(cutoff) {
			if t, ok := entry.roundTripper.(interface{ CloseIdleConnections() }); ok {
				t.CloseIdleConnections()
			}
			delete(m.entries, key)
		}
	}
}

func transportKey(acct *account.Account) string {
	if acct.Proxy == nil {
		return "direct"
	}
	return fmt.Sprintf("%s://%s:%d", acct.Proxy.Type, acct.Proxy.Host, acct.Proxy.Port)
}

// --- Transport building ---

func buildRoundTripper(acct *account.Account) http.RoundTripper {
	if acct.Proxy != nil {
		return &http.Transport{
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     5 * time.Minute,
			DialTLSContext:      proxyDialer(acct.Proxy),
		}
	}
	// 直连：用 http2.Transport，绕开 utls UConn 的 *tls.Conn 类型断言问题
	return &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return dialUTLS(ctx, network, addr)
		},
	}
}

// --- TLS (utls Chrome fingerprint) ---

func dialUTLS(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	dialer := &net.Dialer{}
	rawConn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	return uTLSHandshake(ctx, rawConn, host)
}

func dialUTLSViaConn(ctx context.Context, rawConn net.Conn, serverName string) (net.Conn, error) {
	return uTLSHandshake(ctx, rawConn, serverName)
}

func uTLSHandshake(ctx context.Context, rawConn net.Conn, serverName string) (net.Conn, error) {
	tlsConn := utls.UClient(rawConn, &utls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
	}, utls.HelloChrome_Auto)

	if err := tlsConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, err
	}

	return tlsConn, nil
}

// --- Proxy (SOCKS5 + HTTP CONNECT) ---

func proxyDialer(pcfg *account.ProxyConfig) func(ctx context.Context, network, addr string) (net.Conn, error) {
	switch pcfg.Type {
	case "socks5":
		return socks5Dialer(pcfg)
	default:
		return httpConnectDialer(pcfg)
	}
}

func socks5Dialer(pcfg *account.ProxyConfig) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		proxyAddr := fmt.Sprintf("%s:%d", pcfg.Host, pcfg.Port)

		var auth *proxy.Auth
		if pcfg.Username != "" {
			auth = &proxy.Auth{
				User:     pcfg.Username,
				Password: pcfg.Password,
			}
		}

		dialer, err := proxy.SOCKS5("tcp", proxyAddr, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("socks5 dialer: %w", err)
		}

		rawConn, err := dialer.Dial(network, addr)
		if err != nil {
			return nil, fmt.Errorf("socks5 dial: %w", err)
		}

		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			rawConn.Close()
			return nil, err
		}

		return dialUTLSViaConn(ctx, rawConn, host)
	}
}

func httpConnectDialer(pcfg *account.ProxyConfig) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		proxyAddr := fmt.Sprintf("%s:%d", pcfg.Host, pcfg.Port)

		dialer := &net.Dialer{}
		rawConn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return nil, fmt.Errorf("proxy tcp dial: %w", err)
		}

		connectReq := &http.Request{
			Method: http.MethodConnect,
			URL:    nil,
			Host:   addr,
			Header: make(http.Header),
		}

		if pcfg.Username != "" {
			cred := base64.StdEncoding.EncodeToString([]byte(pcfg.Username + ":" + pcfg.Password))
			connectReq.Header.Set("Proxy-Authorization", "Basic "+cred)
		}

		if err := connectReq.Write(rawConn); err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("proxy CONNECT write: %w", err)
		}

		resp, err := http.ReadResponse(bufio.NewReader(rawConn), connectReq)
		if err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("proxy CONNECT read: %w", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			rawConn.Close()
			return nil, fmt.Errorf("proxy CONNECT failed: %s", resp.Status)
		}

		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			rawConn.Close()
			return nil, err
		}

		return dialUTLSViaConn(ctx, rawConn, host)
	}
}
