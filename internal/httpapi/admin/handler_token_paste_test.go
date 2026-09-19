package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"ds2api/internal/account"
	"ds2api/internal/config"
	adminaccounts "ds2api/internal/httpapi/admin/accounts"
)

// updateWithToken drives PUT /admin/accounts/{identifier} with a token in the
// body, through the real chi route context the handler reads the id from.
func updateWithToken(t *testing.T, h *adminaccounts.Handler, identifier, token string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"token": token})
	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/"+identifier, bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("identifier", identifier)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.UpdateAccount(rec, req)
	return rec
}

// DeepSeek blocks this client's programmatic login, so a token obtained from a
// real browser session is the only way an account becomes usable. Nothing in
// the admin surface accepted one.
func TestUpdateAccountAcceptsAPastedToken(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[{"email":"acc1@example.com","password":"p"}]
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	h := &adminaccounts.Handler{Store: store, Pool: pool}

	rec := updateWithToken(t, h, "acc1@example.com", "pasted-browser-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	acc, ok := store.FindAccount("acc1@example.com")
	if !ok {
		t.Fatal("account vanished")
	}
	if acc.Token != "pasted-browser-token" {
		t.Fatalf("expected the pasted token to be stored, got %q", acc.Token)
	}
}

// An account quarantined for failing to log in is recovered by pasting a
// token. Leaving it held would make the fix look like it did nothing.
func TestPastingATokenReleasesQuarantine(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[{"email":"acc1@example.com","password":"p"}]
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	pool.QuarantineAccount("acc1@example.com", "login_failed")
	h := &adminaccounts.Handler{Store: store, Pool: pool}

	if _, ok := pool.Acquire("acc1@example.com", nil); ok {
		t.Fatal("precondition: the account should be quarantined")
	}

	if rec := updateWithToken(t, h, "acc1@example.com", "pasted-browser-token"); rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	if _, ok := pool.Acquire("acc1@example.com", nil); !ok {
		t.Fatal("an account given a pasted token must return to rotation")
	}
}

// Editing a name must not wipe a token the account already holds.
func TestUpdateAccountWithoutTokenKeepsTheExistingOne(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[{"email":"acc1@example.com","password":"p"}]
	}`)
	store := config.LoadStore()
	if err := store.UpdateAccountToken("acc1@example.com", "existing-token"); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	h := &adminaccounts.Handler{Store: store, Pool: account.NewPool(store)}

	body, _ := json.Marshal(map[string]any{"name": "renamed"})
	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/acc1@example.com", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("identifier", "acc1@example.com")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.UpdateAccount(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	acc, _ := store.FindAccount("acc1@example.com")
	if acc.Token != "existing-token" {
		t.Fatalf("a rename must not disturb the token, got %q", acc.Token)
	}
	if acc.Name != "renamed" {
		t.Fatalf("expected the rename to apply, got %q", acc.Name)
	}
}
