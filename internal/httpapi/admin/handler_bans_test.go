package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/config"
	adminbans "ds2api/internal/httpapi/admin/bans"
)

func newBansPool(t *testing.T, configJSON string) *account.Pool {
	t.Helper()
	t.Setenv("DS2API_CONFIG_JSON", configJSON)
	return account.NewPool(config.LoadStore())
}

func TestListBansReturnsQuarantinedAccounts(t *testing.T) {
	pool := newBansPool(t, `{"keys":["k1"],"accounts":[{"email":"acc1@example.com","token":"t1"}]}`)
	pool.QuarantineAccount("acc1@example.com", "refresh_failed")

	bans := &adminbans.Handler{Pool: pool}
	rec := httptest.NewRecorder()
	bans.ListBans(rec, httptest.NewRequest(http.MethodGet, "/admin/bans", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	list, _ := body["bans"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 quarantined account, body=%s", rec.Body.String())
	}
	entry, _ := list[0].(map[string]any)
	if entry["account_id"] != "acc1@example.com" {
		t.Fatalf("expected the account id, got %v", entry["account_id"])
	}
	if entry["reason"] != "refresh_failed" {
		t.Fatalf("expected the reason to be reported, got %v", entry["reason"])
	}
	if intFrom(entry["strikes"]) != 1 {
		t.Fatalf("expected the strike count to be reported, got %v", entry["strikes"])
	}
}

func TestListBansIsEmptyWhenNothingIsQuarantined(t *testing.T) {
	pool := newBansPool(t, `{"keys":["k1"],"accounts":[{"email":"acc1@example.com","token":"t1"}]}`)

	bans := &adminbans.Handler{Pool: pool}
	rec := httptest.NewRecorder()
	bans.ListBans(rec, httptest.NewRequest(http.MethodGet, "/admin/bans", nil))

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	list, _ := body["bans"].([]any)
	if len(list) != 0 {
		t.Fatalf("expected an empty list, body=%s", rec.Body.String())
	}
}

func TestReleaseBanReturnsAccountToRotation(t *testing.T) {
	pool := newBansPool(t, `{"keys":["k1"],"accounts":[{"email":"acc1@example.com","token":"t1"}]}`)
	pool.QuarantineAccount("acc1@example.com", "refresh_failed")

	bans := &adminbans.Handler{Pool: pool}
	rec := httptest.NewRecorder()
	bans.ReleaseBanForTest(rec, httptest.NewRequest(http.MethodPost, "/admin/bans/acc1@example.com/release", nil), "acc1@example.com")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := pool.Acquire("acc1@example.com", nil); !ok {
		t.Fatal("a released account must return to rotation")
	}
}

func TestReleaseBanRejectsMissingAccount(t *testing.T) {
	pool := newBansPool(t, `{"keys":["k1"]}`)

	bans := &adminbans.Handler{Pool: pool}
	rec := httptest.NewRecorder()
	bans.ReleaseBanForTest(rec, httptest.NewRequest(http.MethodPost, "/admin/bans//release", nil), "")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing account id, got %d", rec.Code)
	}
}
