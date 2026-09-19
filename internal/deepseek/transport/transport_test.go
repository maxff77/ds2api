package transport

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The ALPN a client offers and the protocol it then speaks must agree. A hello
// that advertises h2 over a connection that falls back to HTTP/1.1 is its own
// fingerprint contradiction, which is the bug this guards against.
func TestClientNegotiatesHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Proto))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	c := NewWithRootCAsForTest(10*time.Second, pool)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.Proto != "HTTP/2.0" {
		t.Fatalf("expected the connection to speak HTTP/2, got %s", resp.Proto)
	}
}

// The production constructor must keep rejecting a certificate it does not
// trust; only the test constructor relaxes that.
func TestProductionClientRejectsUntrustedCertificate(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	c := New(5 * time.Second)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)

	if _, err := c.Do(req); err == nil {
		t.Fatal("expected the self-signed certificate to be rejected")
	}
}

// http2.Transport has no Proxy field, so honouring HTTPS_PROXY has to happen
// inside the dialer. Without this the env proxy stops being used silently,
// which on a host that routes egress through a local proxy means every request
// quietly takes the wrong path.
func TestClientTunnelsThroughEnvProxy(t *testing.T) {
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Proto))
	}))
	origin.EnableHTTP2 = true
	origin.StartTLS()
	defer origin.Close()

	var connects int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		atomic.AddInt32(&connects, 1)
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		client, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		go func() { _, _ = io.Copy(upstream, client); upstream.Close() }()
		go func() { _, _ = io.Copy(client, upstream); client.Close() }()
	}))
	defer proxy.Close()

	// Go bypasses proxies for loopback addresses unconditionally, so point the
	// resolver at the test proxy directly rather than through the env var.
	proxyParsed, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("bad proxy url: %v", err)
	}
	prev := proxyForRequest
	proxyForRequest = func(*http.Request) (*url.URL, error) { return proxyParsed, nil }
	t.Cleanup(func() { proxyForRequest = prev })

	pool := x509.NewCertPool()
	pool.AddCert(origin.Certificate())
	c := NewWithRootCAsForTest(10*time.Second, pool)

	req, _ := http.NewRequest(http.MethodGet, origin.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("request through the env proxy failed: %v", err)
	}
	defer resp.Body.Close()

	if atomic.LoadInt32(&connects) == 0 {
		t.Fatal("expected the request to be tunnelled through the env proxy")
	}
	if resp.Proto != "HTTP/2.0" {
		t.Fatalf("expected HTTP/2 through the tunnel, got %s", resp.Proto)
	}
}

// An explicit dialer means a per-account proxy is already in play; the ambient
// env proxy must not be layered on top of it.
func TestExplicitDialerIgnoresEnvProxy(t *testing.T) {
	prev := proxyForRequest
	proxyForRequest = func(*http.Request) (*url.URL, error) {
		return &url.URL{Scheme: "http", Host: "127.0.0.1:1"}, nil
	}
	t.Cleanup(func() { proxyForRequest = prev })

	var dialed int32
	c := NewWithDialContext(5*time.Second, func(ctx context.Context, network, addr string) (net.Conn, error) {
		atomic.AddInt32(&dialed, 1)
		return nil, errors.New("dialer reached")
	})

	req, _ := http.NewRequest(http.MethodGet, "https://chat.deepseek.com/", nil)
	_, _ = c.Do(req)

	if atomic.LoadInt32(&dialed) == 0 {
		t.Fatal("expected the explicit dialer to be used instead of the env proxy")
	}
}

// http2.Transport does not verify ALPN on a connection from a custom dialer,
// so without an explicit check the HTTP/2 preface goes to a peer that only
// offered HTTP/1.1 -- a wasted connection and round trip on every request.
func TestDialerRejectsPeerThatDeclinesHTTP2(t *testing.T) {
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	origin.EnableHTTP2 = false // offers http/1.1 only
	origin.StartTLS()
	defer origin.Close()

	pool := x509.NewCertPool()
	pool.AddCert(origin.Certificate())
	c := NewWithRootCAsForTest(5*time.Second, pool)

	req, _ := http.NewRequest(http.MethodGet, origin.URL, nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected the dialer to refuse a peer that did not negotiate h2")
	}
	if !strings.Contains(err.Error(), "http/1.1") && !strings.Contains(err.Error(), "ALPN") {
		t.Fatalf("expected an ALPN error naming the negotiated protocol, got: %v", err)
	}
}

// A proxy that accepts TCP but never answers CONNECT must not hang the request
// forever: the streaming client carries no timeout of its own.
func TestConnectTunnelHonoursContextDeadline(t *testing.T) {
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer silent.Close()
	go func() {
		for {
			conn, err := silent.Accept()
			if err != nil {
				return
			}
			_ = conn // accepted, never answered
		}
	}()

	prev := proxyForRequest
	proxyForRequest = func(*http.Request) (*url.URL, error) {
		return &url.URL{Scheme: "http", Host: silent.Addr().String()}, nil
	}
	t.Cleanup(func() { proxyForRequest = prev })

	c := New(0) // no client timeout, as the streaming client is built
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://chat.deepseek.com/", nil)

	done := make(chan error, 1)
	go func() { _, e := c.Do(req); done <- e }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the stalled CONNECT to fail")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CONNECT hung past the context deadline")
	}
}
