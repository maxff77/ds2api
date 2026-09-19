package account

import (
	"sync"
	"time"
)

// maxQuarantineBackoffSteps caps the doubling at 16x the base window.
const maxQuarantineBackoffSteps = 4

// BanRecord is one account's quarantine state.
type BanRecord struct {
	AccountID string    `json:"account_id"`
	Reason    string    `json:"reason"`
	BannedAt  time.Time `json:"banned_at"`
	Until     time.Time `json:"until"`
	Strikes   int       `json:"strikes"`
}

// Quarantine holds suspended accounts out of rotation until their window
// lapses. Each record carries its own expiry, so ordinary traffic retries the
// account once it passes -- there is no prober goroutine, timer, or probe
// request to the upstream.
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

// Ban quarantines accountID, doubling the window for each repeat offence.
func (q *Quarantine) Ban(accountID, reason string) {
	if q == nil || accountID == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	strikes := q.records[accountID].Strikes + 1
	window := q.base
	for i := 1; i < strikes && i <= maxQuarantineBackoffSteps; i++ {
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

// Active reports whether accountID is currently held out of rotation.
func (q *Quarantine) Active(accountID string) bool {
	if q == nil {
		return false
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	record, ok := q.records[accountID]
	return ok && time.Now().Before(record.Until)
}

// Release removes the record entirely, which also clears the strike count: an
// operator who has replaced a token should not be penalised by its history.
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
	for _, record := range q.records {
		out = append(out, record)
	}
	return out
}
