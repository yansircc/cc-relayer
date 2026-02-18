package transport

import (
	"context"
	"crypto/tls"
	"net"

	utls "github.com/refraction-networking/utls"
)

// dialUTLS establishes a direct TLS connection using utls with Chrome fingerprint.
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

// dialUTLSViaConn wraps an existing connection (e.g. from a proxy) with utls TLS.
func dialUTLSViaConn(ctx context.Context, rawConn net.Conn, serverName string) (net.Conn, error) {
	return uTLSHandshake(ctx, rawConn, serverName)
}

// uTLSHandshake performs the utls handshake on a raw connection.
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
