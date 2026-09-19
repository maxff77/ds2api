package bans

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	adminshared "ds2api/internal/httpapi/admin/shared"
)

type Handler struct {
	Pool adminshared.PoolController
}

// ListBans reports every account currently held out of rotation.
func (h *Handler) ListBans(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"bans": h.Pool.QuarantinedAccounts()})
}

// ReleaseBan returns a quarantined account to rotation immediately.
func (h *Handler) ReleaseBan(w http.ResponseWriter, r *http.Request) {
	h.releaseAccount(w, chi.URLParam(r, "id"))
}

// ReleaseBanForTest drives the release path with an explicit account id, so
// tests do not need to build a chi routing context.
func (h *Handler) ReleaseBanForTest(w http.ResponseWriter, r *http.Request, accountID string) {
	h.releaseAccount(w, accountID)
}

func (h *Handler) releaseAccount(w http.ResponseWriter, accountID string) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "missing account id"})
		return
	}
	h.Pool.ReleaseAccount(accountID)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
