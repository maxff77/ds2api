package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"ds2api/internal/auth"
	"ds2api/internal/config"
)

// rejectingClient returns a Client whose only transport answers every request
// with the given HTTP status and JSON body, so tests can drive the upstream's
// rejection wording through the public request path.
func rejectingClient(status int, body string) *Client {
	rt := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	return &Client{
		regular:    &http.Client{Transport: rt},
		fallback:   &http.Client{Transport: rt},
		maxRetries: 1,
	}
}

// directAuth is an unmanaged caller: UseConfigToken is false, so the pow path
// never reaches token refresh or account switching and no resolver is needed.
func directAuth() *auth.RequestAuth {
	return &auth.RequestAuth{
		DeepSeekToken:  "t",
		UseConfigToken: false,
		Account:        config.Account{},
	}
}

func TestGetPowReportsAuthFailureOnSuspensionWording(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"chinese banned", `{"code":1,"msg":"账号已被封禁","data":{}}`},
		{"chinese restricted", `{"code":1,"msg":"该账号存在限制","data":{}}`},
		{"chinese anomalous", `{"code":1,"msg":"账号异常","data":{}}`},
		{"english banned", `{"code":1,"msg":"this account has been banned","data":{}}`},
		{"english suspended", `{"code":1,"msg":"account suspended","data":{}}`},
		{"english rate limit", `{"code":1,"msg":"rate limit exceeded","data":{}}`},
		{"english too many", `{"code":1,"msg":"too many requests","data":{}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := rejectingClient(http.StatusOK, tc.body)

			_, err := c.GetPowForTarget(context.Background(), directAuth(), "/api/v0/chat/completion", 1)
			if err == nil {
				t.Fatal("expected the rejection to surface as an error")
			}

			var failure *RequestFailure
			if !errors.As(err, &failure) {
				t.Fatalf("suspension wording must be reported as an auth failure, got a generic error: %v", err)
			}
			if failure.Kind != FailureDirectUnauthorized {
				t.Fatalf("expected kind %q, got %q", FailureDirectUnauthorized, failure.Kind)
			}
		})
	}
}

// Regression guard: the wording the predicate already recognised must keep
// being recognised after the keyword set is restructured.
func TestGetPowStillReportsPreexistingAuthWording(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"http unauthorized", http.StatusUnauthorized, `{"code":1,"msg":"nope","data":{}}`},
		{"http forbidden", http.StatusForbidden, `{"code":1,"msg":"nope","data":{}}`},
		{"code 40001", http.StatusOK, `{"code":40001,"msg":"","data":{}}`},
		{"expired token", http.StatusOK, `{"code":1,"msg":"token expired","data":{}}`},
		{"not login", http.StatusOK, `{"code":1,"msg":"not login","data":{}}`},
		{"invalid jwt", http.StatusOK, `{"code":1,"msg":"invalid jwt","data":{}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := rejectingClient(tc.status, tc.body)

			_, err := c.GetPowForTarget(context.Background(), directAuth(), "/api/v0/chat/completion", 1)
			var failure *RequestFailure
			if !errors.As(err, &failure) {
				t.Fatalf("expected an auth failure, got: %v", err)
			}
			if failure.Kind != FailureDirectUnauthorized {
				t.Fatalf("expected kind %q, got %q", FailureDirectUnauthorized, failure.Kind)
			}
		})
	}
}

// A failure with no auth or suspension signal must stay generic, so that
// ordinary upstream errors are not mistaken for a dead account.
func TestGetPowLeavesUnrelatedFailureGeneric(t *testing.T) {
	c := rejectingClient(http.StatusOK, `{"code":1,"msg":"internal server hiccup","data":{}}`)

	_, err := c.GetPowForTarget(context.Background(), directAuth(), "/api/v0/chat/completion", 1)
	if err == nil {
		t.Fatal("expected an error")
	}
	var failure *RequestFailure
	if errors.As(err, &failure) {
		t.Fatalf("unrelated failure must not be classified as auth, got kind %q", failure.Kind)
	}
}

// The generic Chinese words for "limit" and "anomaly" appear in ordinary
// request errors too. Classifying those as an account failure would, once
// quarantine lands, freeze a perfectly healthy account for a day.
func TestGetPowDoesNotClassifyRequestLimitsAsAccountFailure(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"context length limit", `{"code":1,"msg":"上下文长度限制","data":{}}`},
		{"content anomaly", `{"code":1,"msg":"内容异常","data":{}}`},
		{"english token limit", `{"code":1,"msg":"context length limit exceeded","data":{}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := rejectingClient(http.StatusOK, tc.body)

			_, err := c.GetPowForTarget(context.Background(), directAuth(), "/api/v0/chat/completion", 1)
			var failure *RequestFailure
			if errors.As(err, &failure) {
				t.Fatalf("request-level limit must stay generic, got auth kind %q", failure.Kind)
			}
		})
	}
}
