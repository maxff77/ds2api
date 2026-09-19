package transport

import (
	"crypto/x509"
	"net/http"
	"net/http/httptest"
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
