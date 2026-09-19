package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

type DialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

type Client struct {
	http *http.Client
}

func New(timeout time.Duration) *Client {
	return NewWithDialContext(timeout, nil)
}

func NewWithDialContext(timeout time.Duration, dialContext DialContextFunc) *Client {
	return &Client{http: &http.Client{Timeout: timeout, Transport: newRoundTripper(dialContext, nil)}}
}

// NewWithRootCAsForTest trusts an explicit certificate pool. Tests need this to
// reach an httptest server's self-signed certificate; the production
// constructors above deliberately do not relax certificate verification.
func NewWithRootCAsForTest(timeout time.Duration, rootCAs *x509.CertPool) *Client {
	return &Client{http: &http.Client{Timeout: timeout, Transport: newRoundTripper(nil, rootCAs)}}
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.http.Do(req)
}

func NewFallbackClient(timeout time.Duration, dialContext DialContextFunc) *http.Client {
	useEnvProxy := dialContext == nil
	if dialContext == nil {
		dialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	base := &http.Transport{
		ForceAttemptHTTP2:   false,
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		DialContext:         dialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if useEnvProxy {
		base.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{Timeout: timeout, Transport: base}
}

// newRoundTripper speaks HTTP/2 over a Chrome TLS fingerprint.
//
// http.Transport cannot do this: given a custom DialTLSContext it type-asserts
// the connection to *tls.Conn before handing it to the HTTP/2 round-tripper,
// and a *utls.UConn is not that type, so ForceAttemptHTTP2 silently leaves the
// connection on HTTP/1.1. http2.Transport accepts a custom TLS dialer by
// design, which is why it is used directly here.
func newRoundTripper(dialContext DialContextFunc, rootCAs *x509.CertPool) http.RoundTripper {
	return &http2.Transport{
		DialTLSContext: chromeTLSDialer(dialContext, rootCAs),
	}
}

// chromeTLSDialer presents a Chrome ClientHello. The ALPN it advertises is
// Chrome's own and is left untouched, so the protocol that gets negotiated is
// the protocol the handshake offered.
//
// When no explicit dialer is supplied the ambient HTTPS_PROXY is honoured by
// CONNECT-tunnelling to it first. http2.Transport has no Proxy field, so
// without this the env proxy would stop being used silently. An explicit
// dialer means a per-account proxy is already in play and the env proxy is
// deliberately not layered on top of it.
func chromeTLSDialer(dialContext DialContextFunc, rootCAs *x509.CertPool) func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
	useEnvProxy := dialContext == nil
	if dialContext == nil {
		dialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	return func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		plainConn, err := dialPossiblyViaProxy(ctx, dialContext, network, addr, useEnvProxy)
		if err != nil {
			return nil, err
		}
		host, _, _ := net.SplitHostPort(addr)
		uConn := utls.UClient(plainConn, &utls.Config{
			ServerName: host,
			RootCAs:    rootCAs,
			MinVersion: tls.VersionTLS12,
		}, utls.HelloChrome_Auto)
		if err := uConn.HandshakeContext(ctx); err != nil {
			_ = plainConn.Close()
			return nil, err
		}
		// http2.Transport does not check ALPN on a connection from a custom
		// dialer, so without this the HTTP/2 preface would go to a peer that
		// only offered HTTP/1.1. Fail here instead; the caller's fallback
		// client handles the degraded path.
		if negotiated := uConn.ConnectionState().NegotiatedProtocol; negotiated != http2.NextProtoTLS {
			_ = uConn.Close()
			return nil, fmt.Errorf("peer declined HTTP/2, negotiated ALPN %q", negotiated)
		}
		return uConn, nil
	}
}

// connectTunnelTimeout bounds the CONNECT exchange when the caller supplied no
// deadline of its own.
const connectTunnelTimeout = 20 * time.Second

// proxyForRequest resolves the ambient proxy for a request. It is a variable
// because Go unconditionally bypasses proxies for loopback addresses, which
// makes the tunnelling path unreachable from a test against a local server.
var proxyForRequest = http.ProxyFromEnvironment

// dialPossiblyViaProxy dials addr directly, or CONNECT-tunnels to it through
// the ambient HTTPS_PROXY when one is configured.
func dialPossiblyViaProxy(ctx context.Context, dialContext DialContextFunc, network, addr string, useEnvProxy bool) (net.Conn, error) {
	if !useEnvProxy {
		return dialContext(ctx, network, addr)
	}
	proxyURL, err := proxyForRequest(&http.Request{
		URL:    &url.URL{Scheme: "https", Host: addr},
		Header: make(http.Header),
	})
	if err != nil || proxyURL == nil {
		return dialContext(ctx, network, addr)
	}
	conn, err := dialContext(ctx, network, proxyAddress(proxyURL))
	if err != nil {
		return nil, err
	}
	// A proxy that accepts TCP but never answers CONNECT would otherwise hang
	// the request forever: the streaming client carries no timeout of its own.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(connectTunnelTimeout)
	}
	_ = conn.SetDeadline(deadline)
	if err := connectTunnel(conn, addr, proxyURL); err != nil {
		_ = conn.Close()
		return nil, err
	}
	// Clear it again so the tunnelled connection is not bounded by the
	// handshake budget; long-lived streams run over this conn.
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func proxyAddress(proxyURL *url.URL) string {
	if proxyURL.Port() != "" {
		return proxyURL.Host
	}
	if proxyURL.Scheme == "https" {
		return net.JoinHostPort(proxyURL.Hostname(), "443")
	}
	return net.JoinHostPort(proxyURL.Hostname(), "80")
}

func connectTunnel(conn net.Conn, addr string, proxyURL *url.URL) error {
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if user := proxyURL.User; user != nil {
		password, _ := user.Password()
		req.Header.Set("Proxy-Authorization",
			"Basic "+base64.StdEncoding.EncodeToString([]byte(user.Username()+":"+password)))
	}
	if err := req.Write(conn); err != nil {
		return err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy CONNECT to %s failed: %s", addr, resp.Status)
	}
	// The reader is discarded here, so anything the proxy pipelined into the
	// same segment would be swallowed and the TLS handshake would stall on
	// bytes that have already been consumed. net/http guards this the same way.
	if br.Buffered() > 0 {
		return fmt.Errorf("proxy sent %d bytes after the CONNECT response", br.Buffered())
	}
	return nil
}
