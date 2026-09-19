# DeepSeek Ban Evasion — Implementation Plan (v2, reordered)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use `- [ ]` for tracking.

**Goal:** Stop DeepSeek from banning accounts, cheapest-and-most-likely-cause first, with measurement before any risky rewrite.

**Architecture:** Gate account acquisition on a per-account hourly budget and a ban quarantine with exponential expiry — both hang off the single existing seam `canAcquireIDLocked`. Instrument first so the cause is provable. Only then touch the TLS/identity layer.

**Tech Stack:** Go 1.26, `log/slog` (stdlib), `utls v1.8.2`, `golang.org/x/net v0.52.0`, chi, React (webui)

> **Dependency caveat — check before Phase 4.** `go.mod` pins utls v1.8.2 and x/net v0.52.0, but the local module cache only holds utls v1.7.3 and x/net v0.51.0, so those exact versions were never verified. What WAS verified, in the cached versions: `utls.HelloChrome_Auto = HelloChrome_131` (`u_common.go:612`), `utls.HelloAndroid_11_OkHttp` exists (`:649`), and `http2.Transport.DialTLSContext func(ctx, network, addr string, cfg *tls.Config) (net.Conn, error)` (`http2/transport.go:75`). Run `go mod download` and re-check `HelloChrome_Auto`'s target major before fixing the `sec-ch-ua` / User-Agent version in Task 4.2 — all three must name the same Chrome major.

**Spec:** Grilling session design tree + v1 plan post-mortem (see "Corrections" below).

## Corrections to v1 (verified against source — do not re-litigate)

| v1 claim | Verified reality |
|---|---|
| "Migrate to web endpoint" | `protocol/constants.go:10-21` — endpoints are ALREADY `chat.deepseek.com/api/v0/*`. That IS the web API. No migration exists. |
| "Web emulation avoids PoW" | FALSE. `client_completion.go:19`, `client_continue.go:58`, `client_upload.go:92` all set `x-ds-pow-response`. PoW is mandatory on the same host the web client uses. Removing it breaks completions. |
| "Switch to cookie auth" | `client_auth.go:166` uses `authorization: Bearer <token>`. DeepSeek web keeps its token in localStorage and sends it as Bearer. UNVERIFIED — gated on the DevTools capture (Phase 5). |
| `ForceAttemptHTTP2: true` fixes h2 | FALSE. With a custom `DialTLSContext`, net/http type-asserts the conn to `*tls.Conn` to hand it to the h2 RoundTripper. `*utls.UConn` is not `*tls.Conn`. Needs `http2.Transport` directly. |

## Global Constraints

- TLS fingerprint MUST match the User-Agent. No Safari-hello/Android-UA mismatches, and no hello advertising `h2` over a connection that then speaks HTTP/1.1.
- Do NOT touch endpoints, PoW, or the Bearer auth mechanism before Phase 5.
- SOCKS5 proxy is per-account via the existing `Account.ProxyID` → `config.Proxy` path (`client/proxy.go:104`). Qwen2API's container (`lohari-warp-qwen`) is NOT ds2api's; leave it alone.
- Every phase is independently revertable by one config value set to 0/false. No phase requires the next.
- Ship one phase at a time to the VPS. Observe ≥72h before the next.

---

## Phase 1 — Instrumentation (do this first, ship alone, wait a week)

**Why first:** the ban is gradual (days), which fits volume/behaviour, not a static fingerprint (which fails on handshake #1). Nothing below is worth building blind.

### Task 1.1: Structured request + ban events

**Files:**
- Modify: `internal/account/pool_acquire.go:48-64` (`acquireLocked`), `internal/account/pool_acquire.go:66-81` (`tryAcquire`)
- Modify: `internal/auth/request.go:176-184` (`MarkTokenInvalid`)
- Test: `internal/account/pool_metrics_test.go`

**Interfaces:**
- Produces: two slog event names — `ds_acquire` (fields: `account`, `inflight`) and `ds_token_invalid` (fields: `account`, `reason`). Phases 2-3 consume neither; the admin reads them via `docker logs`.

- [ ] **Step 1: Write the failing test**

```go
// internal/account/pool_metrics_test.go
package account

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestAcquireEmitsMetricEvent(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	p := newSingleAccountPoolForTest(t, "1")
	if _, ok := p.Acquire("", nil); !ok {
		t.Fatal("expected acquire to succeed")
	}
	if !strings.Contains(buf.String(), `"msg":"ds_acquire"`) {
		t.Fatalf("expected ds_acquire event, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "acc1@example.com") {
		t.Fatalf("expected account field, got: %s", buf.String())
	}
}
```

Note: `newSingleAccountPoolForTest(t, maxInflight string) *Pool` already exists at `internal/account/pool_test.go:27` and seeds one account with email `acc1@example.com` (the account ID the pool keys on). Reuse it; do not write a second helper. Its sibling `newPoolForTest` (`:12`) seeds two accounts if you need rotation.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/account/ -run TestAcquireEmitsMetricEvent -v`
Expected: FAIL — no `ds_acquire` in output.

- [ ] **Step 3: Emit on both acquisition paths**

```go
// internal/account/pool_acquire.go — inside acquireLocked, target branch,
// immediately after p.bumpQueue(target):
		slog.Info("ds_acquire", "account", target, "inflight", p.inUse[target])

// internal/account/pool_acquire.go — inside tryAcquire,
// immediately after p.bumpQueue(id):
		slog.Info("ds_acquire", "account", id, "inflight", p.inUse[id])
```

Add `"log/slog"` to the import block of `pool_acquire.go`.

- [ ] **Step 4: Emit on ban-ish signal**

```go
// internal/auth/request.go — inside MarkTokenInvalid, before the early return
// guard is passed, i.e. as the first statement after the guard:
func (r *Resolver) MarkTokenInvalid(a *RequestAuth) {
	if !a.UseConfigToken || a.AccountID == "" {
		return
	}
	slog.Warn("ds_token_invalid", "account", a.AccountID)
	a.Account.Token = ""
	a.DeepSeekToken = ""
	r.clearTokenRefreshMark(a.AccountID)
	_ = r.Store.UpdateAccountToken(a.AccountID, "")
}
```

Add `"log/slog"` to the import block of `internal/auth/request.go`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/account/ ./internal/auth/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/account/ internal/auth/
git commit -m "feat(observability): emit ds_acquire and ds_token_invalid slog events"
```

### Task 1.2: Deploy and collect baseline

- [ ] **Step 1: Build and deploy to VPS**

```bash
ssh root@37.27.12.92 'cd /opt/lohari/scripts && ./ds2api-auto-update.sh'
```

(That script already exists — `/etc/cron.d/ds2api-auto-update` runs it every 6h. Confirm it pulls `ghcr.io/cjackhwang/ds2api:latest`; if this plan's build is not published there, build locally and `docker load` instead.)

- [ ] **Step 2: Confirm events are flowing**

```bash
ssh root@37.27.12.92 'docker logs --since 10m lohari-ds2api 2>&1 | grep -c ds_acquire'
```
Expected: non-zero.

- [ ] **Step 3: After 7 days, pull the baseline**

```bash
ssh root@37.27.12.92 'docker logs --since 168h lohari-ds2api 2>&1 | grep ds_acquire' \
  | jq -r .account | sort | uniq -c | sort -rn
ssh root@37.27.12.92 'docker logs --since 168h lohari-ds2api 2>&1 | grep ds_token_invalid'
```

**Decision gate:** if peak requests/hour for any single account exceeds ~20, Phase 2 is the cause and the highest-value fix. If volume is low and bans still happen, the fingerprint hypothesis gains weight — jump to Phase 4.

---

## Phase 2 — Per-account hourly budget

### Task 2.1: RateLimiter gated at the single acquisition seam

**Files:**
- Create: `internal/account/rate_limit.go`
- Modify: `internal/account/pool_core.go:10-20` (Pool struct), `internal/account/pool_limits.go:62-73` (`canAcquireIDLocked`)
- Modify: `internal/config/config.go:151-156` (RuntimeConfig)
- Test: `internal/account/rate_limit_test.go`

**Interfaces:**
- Produces: `type RateLimiter` with `Allow(accountID string) bool`, `Record(accountID string)`, `SetBudget(perHour int)`. `perHour <= 0` disables the limiter entirely (this is the revert switch).
- Consumes: `config.RuntimeConfig.AccountMaxPerHour`.

- [ ] **Step 1: Write the failing test**

```go
// internal/account/rate_limit_test.go
package account

import "testing"

func TestRateLimiterBlocksOverBudget(t *testing.T) {
	rl := NewRateLimiter(2)
	if !rl.Allow("a") {
		t.Fatal("first request should be allowed")
	}
	rl.Record("a")
	rl.Record("a")
	if rl.Allow("a") {
		t.Fatal("third request should be blocked at budget 2")
	}
	if !rl.Allow("b") {
		t.Fatal("budget must be per-account, not global")
	}
}

func TestRateLimiterZeroBudgetDisables(t *testing.T) {
	rl := NewRateLimiter(0)
	for i := 0; i < 100; i++ {
		rl.Record("a")
	}
	if !rl.Allow("a") {
		t.Fatal("budget 0 must disable the limiter")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/account/ -run TestRateLimiter -v`
Expected: FAIL — `NewRateLimiter` undefined.

- [ ] **Step 3: Implement RateLimiter**

```go
// internal/account/rate_limit.go
package account

import (
	"sync"
	"time"
)

// RateLimiter caps requests per account per rolling hour.
// chisle: rolling slice per account, fine for tens of accounts at tens of
// req/hr; swap for a ring buffer if the pool ever reaches thousands.
type RateLimiter struct {
	mu          sync.Mutex
	hits    map[string][]time.Time
	perHour int
}

func NewRateLimiter(perHour int) *RateLimiter {
	return &RateLimiter{hits: map[string][]time.Time{}, perHour: perHour}
}

func (rl *RateLimiter) SetBudget(perHour int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.perHour = perHour
}

// Allow reports whether accountID is under budget. Budget <= 0 disables.
func (rl *RateLimiter) Allow(accountID string) bool {
	if rl == nil {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.perHour <= 0 {
		return true
	}
	return len(rl.pruneLocked(accountID)) < rl.perHour
}

func (rl *RateLimiter) Record(accountID string) {
	if rl == nil {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.hits[accountID] = append(rl.pruneLocked(accountID), time.Now())
}

func (rl *RateLimiter) pruneLocked(accountID string) []time.Time {
	cutoff := time.Now().Add(-time.Hour)
	kept := rl.hits[accountID][:0]
	for _, t := range rl.hits[accountID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	rl.hits[accountID] = kept
	return kept
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/account/ -run TestRateLimiter -v`
Expected: PASS

- [ ] **Step 5: Add the field to Pool and wire the seam**

```go
// internal/account/pool_core.go — add to the Pool struct, after globalMaxInflight:
	rateLimiter            *RateLimiter

// internal/account/pool_core.go — in NewPool, before p.Reset():
	p.rateLimiter = NewRateLimiter(0) // disabled until configured
```

```go
// internal/account/pool_limits.go — in canAcquireIDLocked, after the
// globalMaxInflight check and before the final return true:
	if !p.rateLimiter.Allow(accountID) {
		return false
	}
```

Record the hit where the account is actually handed out — both sites in `pool_acquire.go`, next to the `slog.Info("ds_acquire", ...)` line added in Phase 1:

```go
		p.rateLimiter.Record(target) // target branch in acquireLocked
		p.rateLimiter.Record(id)     // tryAcquire branch
```

- [ ] **Step 6: Add the config knobs**

```go
// internal/config/config.go — RuntimeConfig
type RuntimeConfig struct {
	AccountMaxInflight        int `json:"account_max_inflight,omitempty"`
	AccountMaxQueue           int `json:"account_max_queue,omitempty"`
	GlobalMaxInflight         int `json:"global_max_inflight,omitempty"`
	TokenRefreshIntervalHours int `json:"token_refresh_interval_hours,omitempty"`
	AccountMaxPerHour         int `json:"account_max_per_hour,omitempty"`
}
```

Extend `ApplyRuntimeLimits` (`pool_limits.go:9`) with a `maxPerHour int` parameter and call `p.rateLimiter.SetBudget(maxPerHour)` inside the existing lock. Update its only caller, `applyRuntimeSettings` (`internal/httpapi/admin/settings/handler_settings_runtime.go:24-34`), to pass `h.Store.Snapshot().Runtime.AccountMaxPerHour`.

- [ ] **Step 7: Run the full suite**

Run: `go test ./... `
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/account/ internal/config/ internal/httpapi/admin/settings/
git commit -m "feat(pool): per-account hourly request budget, disabled by default"
```

**Rollback:** set `runtime.account_max_per_hour` to `0` in `/data/config.json` (or via the admin settings API). No redeploy.

---

## Phase 3 — Ban quarantine with self-expiring backoff

**Design note:** v1 proposed a 12h background prober goroutine. Dropped. Quarantine carries an expiry timestamp; the pool retries the account naturally once it lapses. If it bans again, the next expiry doubles. No ticker, no goroutine, no probe traffic.

### Task 3.1: Quarantine

**Files:**
- Create: `internal/account/quarantine.go`
- Modify: `internal/account/pool_core.go` (Pool struct + NewPool), `internal/account/pool_limits.go:62-73`
- Modify: `internal/deepseek/client/client_auth.go:170-184` (`isTokenInvalid`)
- Test: `internal/account/quarantine_test.go`

**Interfaces:**
- Produces: `type Quarantine` with `Ban(accountID, reason string)`, `Active(accountID string) bool`, `Release(accountID string)`, `List() []BanRecord`; and `type BanRecord struct { AccountID, Reason string; BannedAt, Until time.Time; Strikes int }`.
- Consumed by: Phase 3 Task 3.2 (admin endpoints).

- [ ] **Step 1: Write the failing test**

```go
// internal/account/quarantine_test.go
package account

import (
	"testing"
	"time"
)

func TestQuarantineExpiresAndBacksOff(t *testing.T) {
	q := NewQuarantine(time.Hour)
	q.Ban("a", "403")
	if !q.Active("a") {
		t.Fatal("account should be quarantined")
	}
	rec := q.List()[0]
	if rec.Strikes != 1 {
		t.Fatalf("expected 1 strike, got %d", rec.Strikes)
	}

	// Second ban doubles the window.
	q.Ban("a", "403")
	if got := q.List()[0].Until.Sub(q.List()[0].BannedAt); got != 2*time.Hour {
		t.Fatalf("expected 2h window on strike 2, got %v", got)
	}
}

func TestQuarantineReleaseClearsStrikes(t *testing.T) {
	q := NewQuarantine(time.Hour)
	q.Ban("a", "403")
	q.Release("a")
	if q.Active("a") {
		t.Fatal("released account must not be active")
	}
	if len(q.List()) != 0 {
		t.Fatal("released account must leave the list")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/account/ -run TestQuarantine -v`
Expected: FAIL — `NewQuarantine` undefined.

- [ ] **Step 3: Implement Quarantine**

```go
// internal/account/quarantine.go
package account

import (
	"sync"
	"time"
)

type BanRecord struct {
	AccountID string    `json:"account_id"`
	Reason    string    `json:"reason"`
	BannedAt  time.Time `json:"banned_at"`
	Until     time.Time `json:"until"`
	Strikes   int       `json:"strikes"`
}

// Quarantine holds banned accounts out of rotation until their window lapses.
// The window doubles per strike, capped at 16x the base.
type Quarantine struct {
	mu      sync.RWMutex
	records map[string]BanRecord
	base    time.Duration
}

func NewQuarantine(base time.Duration) *Quarantine {
	if base <= 0 {
		base = 24 * time.Hour
	}
	return &Quarantine{records: map[string]BanRecord{}, base: base}
}

func (q *Quarantine) Ban(accountID, reason string) {
	if q == nil || accountID == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	strikes := q.records[accountID].Strikes + 1
	window := q.base
	for i := 1; i < strikes && i < 5; i++ {
		window *= 2
	}
	now := time.Now()
	q.records[accountID] = BanRecord{
		AccountID: accountID,
		Reason:    reason,
		BannedAt:  now,
		Until:     now.Add(window),
		Strikes:   strikes,
	}
}

func (q *Quarantine) Active(accountID string) bool {
	if q == nil {
		return false
	}
	q.mu.RLock()
	rec, ok := q.records[accountID]
	q.mu.RUnlock()
	return ok && time.Now().Before(rec.Until)
}

func (q *Quarantine) Release(accountID string) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.records, accountID)
}

func (q *Quarantine) List() []BanRecord {
	if q == nil {
		return nil
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]BanRecord, 0, len(q.records))
	for _, rec := range q.records {
		out = append(out, rec)
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/account/ -run TestQuarantine -v`
Expected: PASS

- [ ] **Step 5: Wire into Pool at the same seam**

```go
// internal/account/pool_core.go — Pool struct, after rateLimiter:
	quarantine             *Quarantine

// internal/account/pool_core.go — NewPool, next to the rateLimiter line:
	p.quarantine = NewQuarantine(24 * time.Hour)
```

```go
// internal/account/pool_limits.go — canAcquireIDLocked, before the rateLimiter check:
	if p.quarantine.Active(accountID) {
		return false
	}
```

Add `"time"` to `pool_core.go` imports.

- [ ] **Step 6: Broaden ban detection with the ban-specific keywords**

```go
// internal/deepseek/client/client_auth.go
func isTokenInvalid(status int, code int, bizCode int, msg string, bizMsg string) bool {
	msg = strings.ToLower(strings.TrimSpace(msg) + " " + strings.TrimSpace(bizMsg))
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return true
	}
	if code == 40001 || code == 40002 || code == 40003 || bizCode == 40001 || bizCode == 40002 || bizCode == 40003 {
		return true
	}
	for _, kw := range []string{
		"token", "unauthorized", "expired", "not login", "login required", "invalid jwt",
		"banned", "blocked", "suspended", "rate limit", "too many",
		"封禁", "限制", "异常",
	} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}
```

The three CJK literals are 封禁 (banned), 限制 (restricted), 异常 (anomalous).

- [ ] **Step 7: Call Ban where the token is already judged invalid**

`MarkTokenInvalid` (`internal/auth/request.go:176`) already fires on exactly this condition and already has the account ID. Give `Resolver` a `Pool` reference (or an interface with `Ban(string, string)`) and call it next to the Phase 1 slog line:

```go
	slog.Warn("ds_token_invalid", "account", a.AccountID)
	r.Pool.QuarantineAccount(a.AccountID, "token_invalid")
```

Add the passthrough on Pool:

```go
// internal/account/pool_core.go
func (p *Pool) QuarantineAccount(accountID, reason string) {
	p.quarantine.Ban(accountID, reason)
	p.mu.Lock()
	p.notifyWaiterLocked()
	p.mu.Unlock()
}
```

Read `internal/auth/request.go` for how `Resolver` is constructed and add the field at every construction site before wiring this — do not guess the constructor shape.

- [ ] **Step 8: Run the full suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/account/ internal/auth/ internal/deepseek/client/
git commit -m "feat(pool): quarantine banned accounts with self-expiring backoff"
```

### Task 3.2: Ban dashboard (admin)

**Files:**
- Create: `internal/httpapi/admin/bans/handler.go`
- Modify: `internal/httpapi/admin/handler.go:28-55`
- Modify: `internal/httpapi/admin/shared` (add `Quarantine() []account.BanRecord` + `ReleaseAccount(string)` to `PoolController`)
- Create: `webui/src/features/bans/BanDashboard.jsx`
- Modify: `webui/src/layout/DashboardShell.jsx:47-55` (navItems), `:105-125` (renderTab switch)

**Interfaces:**
- Consumes: `account.BanRecord` from Task 3.1.
- Produces: `GET /admin/bans` → `{"bans": BanRecord[]}`; `POST /admin/bans/{id}/release` → `{"ok": true}`.

- [ ] **Step 1: Implement the handler**

```go
// internal/httpapi/admin/bans/handler.go
package adminbans

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	adminshared "ds2api/internal/httpapi/admin/shared"
)

type Handler struct {
	Pool adminshared.PoolController
}

func RegisterRoutes(r chi.Router, h *Handler) {
	r.Get("/admin/bans", h.list)
	r.Post("/admin/bans/{id}/release", h.release)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"bans": h.Pool.Quarantine()})
}

func (h *Handler) release(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"detail": "missing account id"})
		return
	}
	h.Pool.ReleaseAccount(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
```

- [ ] **Step 2: Register the routes**

```go
// internal/httpapi/admin/handler.go — alongside the other handlers
	bansHandler := &adminbans.Handler{Pool: deps.Pool}
	// ...inside the r.Group protected block:
	adminbans.RegisterRoutes(pr, bansHandler)
```

- [ ] **Step 3: Build the React panel**

```jsx
// webui/src/features/bans/BanDashboard.jsx
import { useCallback, useEffect, useState } from 'react'
import { useI18n } from '../../i18n'

export default function BanDashboard({ authFetch, onMessage }) {
    const { t } = useI18n()
    const [bans, setBans] = useState([])
    const [loading, setLoading] = useState(false)

    const load = useCallback(async () => {
        setLoading(true)
        try {
            const res = await authFetch('/admin/bans')
            const data = await res.json()
            setBans(data.bans || [])
        } catch (err) {
            onMessage('error', err?.message || t('messages.networkError'))
        } finally {
            setLoading(false)
        }
    }, [authFetch, onMessage, t])

    useEffect(() => { load() }, [load])

    const release = async (id) => {
        try {
            await authFetch(`/admin/bans/${encodeURIComponent(id)}/release`, { method: 'POST' })
            onMessage('success', t('bans.released'))
            load()
        } catch (err) {
            onMessage('error', err?.message || t('messages.networkError'))
        }
    }

    if (loading) return <div className="p-6 text-muted-foreground">{t('common.loading')}</div>
    if (bans.length === 0) return <div className="p-6 text-muted-foreground">{t('bans.empty')}</div>

    return (
        <div className="bg-card border border-border rounded-xl overflow-hidden shadow-sm divide-y divide-border">
            {bans.map(b => (
                <div key={b.account_id} className="p-4 flex items-center justify-between gap-4">
                    <div>
                        <div className="font-medium">{b.account_id}</div>
                        <div className="text-sm text-muted-foreground">
                            {b.reason} · {t('bans.strikes')}: {b.strikes} · {t('bans.until')}: {new Date(b.until).toLocaleString()}
                        </div>
                    </div>
                    <button
                        onClick={() => release(b.account_id)}
                        className="px-3 py-1.5 bg-primary text-primary-foreground rounded-lg text-sm font-medium hover:bg-primary/90 transition-colors"
                    >
                        {t('bans.release')}
                    </button>
                </div>
            ))}
        </div>
    )
}
```

- [ ] **Step 4: Add the nav entry**

In `DashboardShell.jsx`, add `{ id: 'bans', label: t('nav.bans.label'), icon: ShieldAlert, description: t('nav.bans.desc') }` to `navItems`, import `ShieldAlert` from `lucide-react`, and add `case 'bans': return <BanDashboard authFetch={authFetch} onMessage={showMessage} />` to the `renderTab` switch. Add the `nav.bans.*` and `bans.*` keys to every locale file under `webui/src/i18n/` — read one to match its shape.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/admin/ webui/src/
git commit -m "feat(admin): ban dashboard with manual release"
```

---

## Phase 4 — Fix the TLS/UA contradiction (only after Phases 1-3 have run)

**Current state, verified:** `transport.go:82` sends a Safari ClientHello; `forceHTTP11ALPN` (`:100-113`) then rewrites its ALPN to `http/1.1` only. Real Safari advertises `h2`. So the fingerprint is not "Safari" — it is a mutant Safari that exists nowhere in the wild, paired with a `DeepSeek/x.y.z Android/35` User-Agent. Two independent contradictions.

**Two things must land together.** Switching the profile without fixing h2 just moves the contradiction.

### Task 4.1: Real HTTP/2 over utls

**Files:**
- Modify: `internal/deepseek/transport/transport.go`
- Test: `internal/deepseek/transport/transport_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/deepseek/transport/transport_test.go
package transport

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientNegotiatesHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Proto))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	c := NewInsecureForTest(10*time.Second, srv.Certificate())
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.Proto != "HTTP/2.0" {
		t.Fatalf("expected HTTP/2.0, got %s", resp.Proto)
	}
	_ = tls.VersionTLS12
}
```

`NewInsecureForTest` is a test-only constructor that trusts the httptest cert — add it in `transport.go` behind a clear name, since httptest uses a self-signed cert the production path must keep rejecting.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deepseek/transport/ -run TestClientNegotiatesHTTP2 -v`
Expected: FAIL — HTTP/1.1, or `NewInsecureForTest` undefined.

- [ ] **Step 3: Replace the transport construction**

```go
// internal/deepseek/transport/transport.go
// Replaces safariTLSDialer + forceHTTP11ALPN entirely.

func chromeTLSDialer(dialContext DialContextFunc, rootCAs *x509.CertPool) func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
	if dialContext == nil {
		dialContext = (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	return func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		plainConn, err := dialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		host, _, _ := net.SplitHostPort(addr)
		uConn := utls.UClient(plainConn, &utls.Config{
			ServerName: host,
			RootCAs:    rootCAs,
		}, utls.HelloChrome_Auto)
		if err := uConn.HandshakeContext(ctx); err != nil {
			_ = plainConn.Close()
			return nil, err
		}
		return uConn, nil
	}
}
```

Build the RoundTripper with `golang.org/x/net/http2` directly — this is the whole reason `ForceAttemptHTTP2` cannot work here:

```go
func newRoundTripper(dialContext DialContextFunc, rootCAs *x509.CertPool) http.RoundTripper {
	return &http2.Transport{
		DialTLSContext:     chromeTLSDialer(dialContext, rootCAs),
		DisableCompression: false,
		AllowHTTP:          false,
	}
}
```

Keep `NewFallbackClient` on plain `http.Transport` unchanged — it is the escape hatch if h2 misbehaves.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/deepseek/transport/ -v`
Expected: PASS, `HTTP/2.0`.

- [ ] **Step 5: Run the whole suite — SSE is the risk**

Run: `go test ./...`
Expected: PASS. Pay attention to `internal/sse/` and `internal/httpapi/openai/chat/` — streaming over h2 frames differently than over chunked HTTP/1.1, and `chat_stream_runtime.go:135-140` flushes per frame.

- [ ] **Step 6: Commit**

```bash
git add internal/deepseek/transport/
git commit -m "feat(transport): real HTTP/2 over utls via http2.Transport"
```

### Task 4.2: Align the User-Agent with the Chrome hello

**Files:**
- Modify: `internal/deepseek/protocol/constants_shared.json`
- Modify: `internal/deepseek/protocol/constants.go:92-133`
- Test: `internal/deepseek/protocol/constants_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/deepseek/protocol/constants_test.go
package protocol

import (
	"strings"
	"testing"
)

func TestBaseHeadersAreChromeConsistent(t *testing.T) {
	ua := BaseHeaders["User-Agent"]
	if !strings.Contains(ua, "Chrome/") || !strings.HasPrefix(ua, "Mozilla/5.0") {
		t.Fatalf("User-Agent must be a Chrome UA to match the TLS hello, got %q", ua)
	}
	if strings.Contains(ua, "Android") {
		t.Fatalf("Android UA contradicts the Chrome desktop TLS fingerprint: %q", ua)
	}
	for _, k := range []string{"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "sec-fetch-site"} {
		if BaseHeaders[k] == "" {
			t.Fatalf("missing client hint %q — a Chrome hello without Chrome hints is its own mismatch", k)
		}
	}
	if BaseHeaders["x-client-platform"] == "android" {
		t.Fatal("x-client-platform must not claim android")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deepseek/protocol/ -v`
Expected: FAIL.

- [ ] **Step 3: Rewrite the shared constants**

```json
{
  "client": {
    "name": "Chrome",
    "platform": "web",
    "version": "131.0.0.0",
    "locale": "zh-CN"
  },
  "base_headers": {
    "Host": "chat.deepseek.com",
    "Accept": "*/*",
    "Content-Type": "application/json",
    "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
    "Origin": "https://chat.deepseek.com",
    "Referer": "https://chat.deepseek.com/",
    "sec-ch-ua": "\"Google Chrome\";v=\"131\", \"Chromium\";v=\"131\", \"Not_A Brand\";v=\"24\"",
    "sec-ch-ua-mobile": "?0",
    "sec-ch-ua-platform": "\"Windows\"",
    "sec-fetch-dest": "empty",
    "sec-fetch-mode": "cors",
    "sec-fetch-site": "same-origin"
  }
}
```

The `sec-ch-ua` version, the `Chrome/<version>` in the UA, and whatever `utls.HelloChrome_Auto` ships in utls v1.8.2 must all name the same major. Check utls's current Chrome target before picking `131` and adjust all three together.

- [ ] **Step 4: Make buildBaseHeaders emit a web UA**

```go
// internal/deepseek/protocol/constants.go
func normalizeClientConstants(in clientConstants) clientConstants {
	if in.Name == "" {
		in.Name = "Chrome"
	}
	if in.Platform == "" {
		in.Platform = "web"
	}
	if in.Locale == "" {
		in.Locale = "zh-CN"
	}
	return in
}

func buildBaseHeaders(client clientConstants, overrides map[string]string) map[string]string {
	out := cloneStringMap(defaultStaticBaseHeaders)
	for k, v := range overrides {
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if client.Platform == "web" && client.Version != "" {
		out["User-Agent"] = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
			"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + client.Version + " Safari/537.36"
	}
	return out
}
```

Drop the `x-client-platform` / `x-client-version` / `x-client-locale` emission — those are mobile-app headers and the web client does not send them. Delete the `AndroidAPILevel` field from `clientConstants` and its normalization.

- [ ] **Step 5: Run the suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/deepseek/protocol/
git commit -m "feat(protocol): Chrome web UA and client hints to match the TLS hello"
```

---

## Phase 5 — BLOCKED: full web identity

**Gate:** the DevTools capture (see "Capture procedure" below). Until the real web client's request shape is on disk, nothing here can be designed — v1's three wrong assumptions all came from guessing at it.

Once captured, this phase decides only:
1. Does the web client send the token as `Cookie` or `Authorization: Bearer`? (determines whether `Account.Cookie` is needed at all)
2. Does it send `x-ds-pow-response`? (if yes, PoW stays exactly as is, forever)
3. What headers does it send that Phase 4.2 missed?

Do not start this phase before Phases 1-4 have each run ≥72h in production.

---

## Rollback

| Phase | Revert | Redeploy? |
|---|---|---|
| 1 | Nothing to revert; logging only. | no |
| 2 | `runtime.account_max_per_hour = 0` | no |
| 3 | `POST /admin/bans/{id}/release` per account; quarantine windows lapse on their own. | no |
| 4 | `git revert` the two commits; the container's previous image tag is still in the local Docker cache. | yes |
| 5 | n/a | — |

Tag before each deploy so the rollback target is unambiguous:

```bash
git tag ds2api-preflight-phase<N> && git push --tags
```

## Production verification (run after every phase)

```bash
# 1. Container healthy
ssh root@37.27.12.92 'docker ps --filter name=lohari-ds2api --format "{{.Status}}"'

# 2. Completions still work end to end
ssh root@37.27.12.92 'curl -s -o /dev/null -w "%{http_code}\n" \
  -X POST http://127.0.0.1:5001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DS2API_KEY" \
  -d "{\"model\":\"deepseek-chat\",\"messages\":[{\"role\":\"user\",\"content\":\"ping\"}]}"'
# Expected: 200

# 3. No new ban signals in the last hour
ssh root@37.27.12.92 'docker logs --since 1h lohari-ds2api 2>&1 | grep -c ds_token_invalid'
# Expected: 0

# 4. Per-account spread is even (Phase 2+)
ssh root@37.27.12.92 'docker logs --since 1h lohari-ds2api 2>&1 | grep ds_acquire' \
  | jq -r .account | sort | uniq -c | sort -rn
# Expected: no account above runtime.account_max_per_hour
```

## Open risk, not addressed by any phase

All accounts egress from one IP. Whatever the per-account budget is, DeepSeek can correlate every account in the pool as a single actor by source IP. The design tree chose "single WARP, multi-account" for cost reasons, and this plan honours that — but if Phases 1-4 land and bans continue, IP correlation is the remaining explanation and the only fix is more egress IPs.

Also verified: the VPS's outbound IP is the bare `37.27.12.92`. The WARP container on the box (`lohari-warp-qwen`, `127.0.0.1:9091`) belongs to Qwen2API and is **not** in ds2api's path today. Routing ds2api through it would put both services behind one shared Cloudflare egress — which couples their fates rather than isolating them. Decide deliberately before wiring `Account.ProxyID` to it.

---

## Capture procedure (gates Phase 5)

The Chrome extension is not connected to this session, so this is a manual step. **The captured file contains a live session token — it must never be pasted into a chat, a commit, or an issue.**

1. Open `https://chat.deepseek.com` in Chrome, logged in.
2. DevTools (`F12`) → **Network** tab → filter box: `completion`.
3. Send any short message in the chat.
4. Right-click the `completion` request → **Copy** → **Copy as cURL**.
5. Paste it into a scratch file OUTSIDE the repo:

```bash
pbpaste > "$TMPDIR/ds-web-capture.txt"   # macOS
```

6. Extract only the shape — header names, and value lengths instead of values:

```bash
grep -oE "^  -H '[^:]+:" "$TMPDIR/ds-web-capture.txt" | sed "s/  -H '//; s/:$//" | sort
echo '--- auth carrier ---'
grep -oiE "(cookie|authorization):" "$TMPDIR/ds-web-capture.txt" | sort -u
echo '--- pow present? ---'
grep -ci 'x-ds-pow-response' "$TMPDIR/ds-web-capture.txt"
```

7. Share **only that command's output**. Then delete the capture:

```bash
rm "$TMPDIR/ds-web-capture.txt"
```

The three answers this yields — which header carries auth, whether PoW is present, and the full header list — are the only inputs Phase 5 needs.
