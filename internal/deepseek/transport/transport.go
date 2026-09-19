package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
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
func chromeTLSDialer(dialContext DialContextFunc, rootCAs *x509.CertPool) func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
	if dialContext == nil {
		dialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	return func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		plainConn, err := dialContext(ctx, network, addr)
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
		return uConn, nil
	}
}
