package protocol

import (
	"strings"
	"testing"
)

// The TLS handshake claims desktop Chrome. Every header must tell the same
// story: a Chrome hello paired with a mobile-app User-Agent is a combination
// that exists nowhere in the wild, which is what made the client trivially
// fingerprintable.
func TestUserAgentIsDesktopChrome(t *testing.T) {
	ua := BaseHeaders["User-Agent"]

	if !strings.HasPrefix(ua, "Mozilla/5.0") {
		t.Fatalf("expected a browser User-Agent, got %q", ua)
	}
	if !strings.Contains(ua, "Chrome/") {
		t.Fatalf("expected a Chrome User-Agent to match the TLS hello, got %q", ua)
	}
	if strings.Contains(ua, "Android") || strings.Contains(ua, "DeepSeek/") {
		t.Fatalf("mobile-app wording contradicts the desktop Chrome hello: %q", ua)
	}
}

func TestClientHintsArePresent(t *testing.T) {
	for _, header := range []string{
		"sec-ch-ua",
		"sec-ch-ua-mobile",
		"sec-ch-ua-platform",
		"sec-fetch-dest",
		"sec-fetch-mode",
		"sec-fetch-site",
	} {
		if strings.TrimSpace(BaseHeaders[header]) == "" {
			t.Fatalf("a Chrome hello without the %q client hint is its own mismatch", header)
		}
	}
}

func TestMobileAppHeadersAreGone(t *testing.T) {
	for _, header := range []string{"x-client-platform", "x-client-version", "x-client-locale"} {
		if _, exists := BaseHeaders[header]; exists {
			t.Fatalf("mobile-app header %q must not be sent by a browser client", header)
		}
	}
}

// The uTLS profile targets one Chrome major; the User-Agent and the client
// hint must name that same major or the three disagree.
func TestChromeMajorAgreesAcrossHeaders(t *testing.T) {
	if !strings.Contains(BaseHeaders["User-Agent"], "Chrome/"+ChromeMajorVersion+".") {
		t.Fatalf("User-Agent must name Chrome %s, got %q", ChromeMajorVersion, BaseHeaders["User-Agent"])
	}
	if !strings.Contains(BaseHeaders["sec-ch-ua"], `"`+ChromeMajorVersion+`"`) {
		t.Fatalf("sec-ch-ua must name Chrome %s, got %q", ChromeMajorVersion, BaseHeaders["sec-ch-ua"])
	}
}
