package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetSettingsExposesHourlyBudget(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	rec := httptest.NewRecorder()

	h.getSettings(rec, req)

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	runtime, _ := body["runtime"].(map[string]any)
	if _, exists := runtime["account_max_per_hour"]; !exists {
		t.Fatalf("expected runtime.account_max_per_hour in settings, body=%v", body)
	}
	if got := intFrom(runtime["account_max_per_hour"]); got != 0 {
		t.Fatalf("expected the budget to default to 0 (disabled), got %d", got)
	}
}

func TestUpdateSettingsSetsHourlyBudget(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	b, _ := json.Marshal(map[string]any{
		"runtime": map[string]any{"account_max_per_hour": 20},
	})
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()

	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.Snapshot().Runtime.AccountMaxPerHour; got != 20 {
		t.Fatalf("expected the budget to be persisted as 20, got %d", got)
	}
}

// Zero is the rollback, so it must be writable through the same path that set
// it -- not only by hand-editing the config file.
func TestUpdateSettingsRestoresDisabledBudget(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"runtime":{"account_max_per_hour":20}}`)
	b, _ := json.Marshal(map[string]any{
		"runtime": map[string]any{"account_max_per_hour": 0},
	})
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()

	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.Snapshot().Runtime.AccountMaxPerHour; got != 0 {
		t.Fatalf("expected an explicit zero to disable the budget, got %d", got)
	}
}
