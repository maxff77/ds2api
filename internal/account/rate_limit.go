package account

import (
	"sync"
	"time"
)

// RateLimiter caps how many requests one account serves per rolling hour.
// The pool already caps concurrency; this caps volume over time, which is the
// shape of traffic that reads as automated upstream.
//
// chisle: a pruned timestamp slice per account is adequate for tens of
// accounts at tens of requests per hour. Reach for a ring buffer only if the
// pool ever grows by orders of magnitude.
type RateLimiter struct {
	mu      sync.Mutex
	hits    map[string][]time.Time
	perHour int
}

// NewRateLimiter builds a limiter. A perHour of zero or less disables it.
func NewRateLimiter(perHour int) *RateLimiter {
	return &RateLimiter{hits: map[string][]time.Time{}, perHour: perHour}
}

// SetBudget changes the hourly cap. Zero or less disables the limiter, which
// is the rollback: a config write, no redeploy.
func (rl *RateLimiter) SetBudget(perHour int) {
	if rl == nil {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.perHour = perHour
}

// Allow reports whether accountID is still under its hourly budget.
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

// Record marks one request served by accountID.
func (rl *RateLimiter) Record(accountID string) {
	if rl == nil || accountID == "" {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.hits[accountID] = append(rl.pruneLocked(accountID), time.Now())
}

func (rl *RateLimiter) pruneLocked(accountID string) []time.Time {
	cutoff := time.Now().Add(-time.Hour)
	kept := rl.hits[accountID][:0]
	for _, at := range rl.hits[accountID] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	rl.hits[accountID] = kept
	return kept
}
