package transport

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"

	"github.com/yansir/claude-relay/internal/account"
	"golang.org/x/net/proxy"
)

// proxyDialer returns a DialTLSContext function that connects through the given proxy
// and wraps the connection with utls TLS.
func proxyDialer(pcfg *account.ProxyConfig) func(ctx context.Context, network, addr string) (net.Conn, error) {
	switch pcfg.Type {
	case "socks5":
		return socks5Dialer(pcfg)
	default:
		// http and https proxies use CONNECT
		return httpConnectDialer(pcfg)
	}
}

// socks5Dialer creates a SOCKS5 dial function.
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

// httpConnectDialer creates an HTTP CONNECT tunnel dial function.
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
