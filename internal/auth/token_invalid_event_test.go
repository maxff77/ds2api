package auth

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

func TestMarkTokenInvalidEmitsEventNamingTheAccount(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

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
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	t.Setenv("DS2API_CONFIG_JSON", `{"keys":["k1"],"accounts":[]}`)
	store := config.LoadStore()
	r := NewResolver(store, account.NewPool(store), nil)

	r.MarkTokenInvalid(&RequestAuth{UseConfigToken: false, DeepSeekToken: "caller-token"})

	if strings.Contains(buf.String(), "ds_token_invalid") {
		t.Fatalf("unmanaged caller must not emit an account event, got: %s", buf.String())
	}
}
