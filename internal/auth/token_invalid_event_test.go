package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

func TestMarkTokenInvalidEmitsEventNamingTheAccount(t *testing.T) {
	var buf bytes.Buffer
	prev := config.Logger
	config.Logger = slog.New(slog.NewJSONHandler(&buf, nil))
	t.Cleanup(func() { config.Logger = prev })

	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[{"email":"acc1@example.com","token":"token1"}]
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	r := NewResolver(store, pool, func(_ context.Context, _ config.Account) (string, error) {
		return "", nil
	})

	a := &RequestAuth{
		AccountID:      "acc1@example.com",
		UseConfigToken: true,
		DeepSeekToken:  "token1",
		Account:        config.Account{Email: "acc1@example.com", Token: "token1"},
	}
	r.MarkTokenInvalid(a)

	out := buf.String()
	if !strings.Contains(out, `"msg":"ds_token_invalid"`) {
		t.Fatalf("expected a ds_token_invalid event, got: %s", out)
	}
	if !strings.Contains(out, "acc1@example.com") {
		t.Fatalf("expected the event to name the account, got: %s", out)
	}
	if !strings.Contains(out, `"reason"`) {
		t.Fatalf("expected the event to carry a reason, got: %s", out)
	}
}

// An unmanaged caller owns its own token; there is no pooled account to report.
func TestMarkTokenInvalidStaysSilentForUnmanagedCaller(t *testing.T) {
	var buf bytes.Buffer
	prev := config.Logger
	config.Logger = slog.New(slog.NewJSONHandler(&buf, nil))
	t.Cleanup(func() { config.Logger = prev })

	t.Setenv("DS2API_CONFIG_JSON", `{"keys":["k1"],"accounts":[]}`)
	store := config.LoadStore()
	r := NewResolver(store, account.NewPool(store), nil)

	r.MarkTokenInvalid(&RequestAuth{UseConfigToken: false, DeepSeekToken: "caller-token"})

	if strings.Contains(buf.String(), "ds_token_invalid") {
		t.Fatalf("unmanaged caller must not emit an account event, got: %s", buf.String())
	}
}

func TestMarkTokenInvalidQuarantinesTheAccount(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[
			{"email":"acc1@example.com","token":"token1"},
			{"email":"acc2@example.com","token":"token2"}
		]
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	r := NewResolver(store, pool, func(_ context.Context, _ config.Account) (string, error) {
		return "", nil
	})

	r.MarkTokenInvalid(&RequestAuth{
		AccountID:      "acc1@example.com",
		UseConfigToken: true,
		DeepSeekToken:  "token1",
		Account:        config.Account{Email: "acc1@example.com", Token: "token1"},
	})

	if _, ok := pool.Acquire("acc1@example.com", nil); ok {
		t.Fatal("an account whose token was judged invalid must leave rotation")
	}
	records := pool.QuarantinedAccounts()
	if len(records) != 1 || records[0].AccountID != "acc1@example.com" {
		t.Fatalf("expected the account to be quarantined, got %+v", records)
	}
}

// The production path never calls MarkTokenInvalid: on an auth failure the
// client asks for a refresh and, when that fails, switches account. A refresh
// that fails means no working token can be obtained for this account, which is
// what a suspension looks like from here.
func TestFailedRefreshQuarantinesTheAccount(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[
			{"email":"acc1@example.com","password":"p","token":"token1"},
			{"email":"acc2@example.com","password":"p","token":"token2"}
		]
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	r := NewResolver(store, pool, func(_ context.Context, _ config.Account) (string, error) {
		return "", errors.New("account suspended")
	})

	a := &RequestAuth{
		AccountID:      "acc1@example.com",
		UseConfigToken: true,
		DeepSeekToken:  "token1",
		Account:        config.Account{Email: "acc1@example.com", Password: "p", Token: "token1"},
	}
	if r.RefreshToken(context.Background(), a) {
		t.Fatal("expected the refresh to fail")
	}

	if _, ok := pool.Acquire("acc1@example.com", nil); ok {
		t.Fatal("an account that cannot obtain a token must leave rotation")
	}
}

func TestSuccessfulRefreshLeavesAccountInRotation(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[{"email":"acc1@example.com","password":"p","token":"token1"}]
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	r := NewResolver(store, pool, func(_ context.Context, _ config.Account) (string, error) {
		return "fresh-token", nil
	})

	a := &RequestAuth{
		AccountID:      "acc1@example.com",
		UseConfigToken: true,
		DeepSeekToken:  "token1",
		Account:        config.Account{Email: "acc1@example.com", Password: "p", Token: "token1"},
	}
	if !r.RefreshToken(context.Background(), a) {
		t.Fatal("expected the refresh to succeed")
	}

	if _, ok := pool.Acquire("acc1@example.com", nil); !ok {
		t.Fatal("an ordinary token refresh must not quarantine the account")
	}
}

// The client retries a failed refresh up to maxRetries per request, and every
// retry reaches the quarantine trigger. One failing request must still be one
// episode, or a first failure lands straight on the maximum backoff.
func TestRepeatedRefreshFailuresAreOneEpisode(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[{"email":"acc1@example.com","password":"p","token":"token1"}]
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	r := NewResolver(store, pool, func(_ context.Context, _ config.Account) (string, error) {
		return "", errors.New("account suspended")
	})

	for i := 0; i < 6; i++ {
		r.RefreshToken(context.Background(), &RequestAuth{
			AccountID:      "acc1@example.com",
			UseConfigToken: true,
			DeepSeekToken:  "token1",
			Account:        config.Account{Email: "acc1@example.com", Password: "p", Token: "token1"},
		})
	}

	records := pool.QuarantinedAccounts()
	if len(records) != 1 {
		t.Fatalf("expected 1 quarantined account, got %d", len(records))
	}
	if records[0].Strikes != 1 {
		t.Fatalf("one failing request must be one strike, got %d (window %v)",
			records[0].Strikes, records[0].Until.Sub(records[0].BannedAt))
	}
}

// An account with no token at all logs in through loginAndPersist without
// passing through RefreshToken. Quarantining only on the refresh path left a
// never-loggable account in rotation forever, burning a request every time it
// came up -- the exact failure quarantine exists to stop.
func TestFailedInitialLoginQuarantinesTheAccount(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[
			{"email":"acc1@example.com","password":"p"},
			{"email":"acc2@example.com","password":"p"}
		]
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	r := NewResolver(store, pool, func(_ context.Context, _ config.Account) (string, error) {
		return "", errors.New("RISK_DEVICE_DETECTED")
	})

	a := &RequestAuth{
		AccountID:      "acc1@example.com",
		UseConfigToken: true,
		Account:        config.Account{Email: "acc1@example.com", Password: "p"},
	}
	if err := r.ensureManagedToken(context.Background(), a); err == nil {
		t.Fatal("expected the initial login to fail")
	}

	if _, ok := pool.Acquire("acc1@example.com", nil); ok {
		t.Fatal("an account that cannot obtain a token at all must leave rotation")
	}
}
