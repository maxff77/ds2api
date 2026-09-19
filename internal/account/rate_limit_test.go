package account

import (
	"context"
	"testing"
	"time"

	"ds2api/internal/config"
)

func newPoolFromEnvForTest(t *testing.T) *Pool {
	t.Helper()
	return NewPool(config.LoadStore())
}

func TestBudgetBlocksAccountOverLimit(t *testing.T) {
	rl := NewRateLimiter(2)

	if !rl.Allow("a") {
		t.Fatal("first request must be allowed")
	}
	rl.Record("a")
	rl.Record("a")

	if rl.Allow("a") {
		t.Fatal("a third request must be blocked at a budget of 2")
	}
}

func TestBudgetIsPerAccountNotGlobal(t *testing.T) {
	rl := NewRateLimiter(1)
	rl.Record("a")

	if rl.Allow("a") {
		t.Fatal("the spent account must be blocked")
	}
	if !rl.Allow("b") {
		t.Fatal("another account must still be allowed")
	}
}

func TestZeroBudgetDisablesTheLimiter(t *testing.T) {
	rl := NewRateLimiter(0)
	for i := 0; i < 100; i++ {
		rl.Record("a")
	}

	if !rl.Allow("a") {
		t.Fatal("a budget of zero must disable the limiter")
	}
}

func TestBudgetIsSettableAtRuntime(t *testing.T) {
	rl := NewRateLimiter(0)
	rl.Record("a")
	rl.Record("a")

	rl.SetBudget(1)
	if rl.Allow("a") {
		t.Fatal("raising the budget above the recorded volume must take effect")
	}

	rl.SetBudget(0)
	if !rl.Allow("a") {
		t.Fatal("returning the budget to zero must re-disable the limiter")
	}
}

func TestAccountAtBudgetIsNotHandedOut(t *testing.T) {
	p := newSingleAccountPoolForTest(t, "2")
	p.ApplyRuntimeLimits(2, 0, 0, 1)

	if _, ok := p.Acquire("", nil); !ok {
		t.Fatal("the first acquisition must succeed")
	}
	if _, ok := p.Acquire("", nil); ok {
		t.Fatal("the account is at budget and must not be handed out again")
	}
}

func TestPoolDefaultsToNoBudget(t *testing.T) {
	p := newSingleAccountPoolForTest(t, "4")

	for i := 0; i < 4; i++ {
		if _, ok := p.Acquire("", nil); !ok {
			t.Fatalf("acquisition %d must succeed when no budget is configured", i+1)
		}
	}
}

func TestBudgetLeavesOtherAccountsAcquirable(t *testing.T) {
	p := newPoolForTest(t, "2")
	p.ApplyRuntimeLimits(2, 0, 0, 1)

	first, ok := p.Acquire("", nil)
	if !ok {
		t.Fatal("the first acquisition must succeed")
	}
	second, ok := p.Acquire("", nil)
	if !ok {
		t.Fatal("a second account under budget must still be handed out")
	}
	if first.Email == second.Email {
		t.Fatal("expected rotation to move to the other account once the first was spent")
	}
}

// The budget must be in force from startup. Reading it only in the admin
// settings handler left it silently inert until someone re-saved settings, and
// reverting on every restart.
func TestBudgetIsInForceFromStartup(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[{"email":"acc1@example.com","token":"t1"}],
		"runtime":{"account_max_per_hour":1}
	}`)
	p := newPoolFromEnvForTest(t)

	if _, ok := p.Acquire("", nil); !ok {
		t.Fatal("the first acquisition must succeed")
	}
	if _, ok := p.Acquire("", nil); ok {
		t.Fatal("the configured budget must apply without an admin settings save")
	}
}

func TestBudgetSurvivesReset(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[{"email":"acc1@example.com","token":"t1"}],
		"runtime":{"account_max_per_hour":1}
	}`)
	p := newPoolFromEnvForTest(t)
	p.Reset()

	if _, ok := p.Acquire("", nil); !ok {
		t.Fatal("the first acquisition must succeed")
	}
	if _, ok := p.Acquire("", nil); ok {
		t.Fatal("Reset must not drop the configured budget")
	}
}

// Quarantine and the hourly budget lapse with time, but waiters were only ever
// woken by another request releasing. A waiter blocked on a time-based limit
// must re-check rather than sleep until the caller's deadline -- and with no
// deadline on the inbound request, that was "hang until the client gives up".
func TestWaiterRechecksTimeBasedLimits(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[{"email":"acc1@example.com","token":"t1"}]
	}`)
	p := newPoolFromEnvForTest(t)
	p.quarantine = NewQuarantine(40 * time.Millisecond)
	p.QuarantineAccount("acc1@example.com", "refresh_failed")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	if _, ok := p.AcquireWait(ctx, "", nil); !ok {
		t.Fatal("expected the waiter to acquire once the quarantine window lapsed")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("waiter took %v -- it slept to the deadline instead of re-checking", elapsed)
	}
}

// The budget is meant to cap requests actually served upstream. An account
// acquired and handed straight back because its token could not be prepared
// served nothing, so charging it would exhaust the budget on auth churn.
func TestAbandonedAcquisitionDoesNotSpendBudget(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[{"email":"acc1@example.com","token":"t1"}],
		"runtime":{"account_max_per_hour":2}
	}`)
	p := newPoolFromEnvForTest(t)

	for i := 0; i < 5; i++ {
		if _, ok := p.Acquire("", nil); !ok {
			t.Fatalf("acquisition %d must succeed when every prior one was abandoned", i+1)
		}
		p.ReleaseUnused("acc1@example.com")
	}
}

func TestReleaseStillSpendsBudget(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[{"email":"acc1@example.com","token":"t1"}],
		"runtime":{"account_max_per_hour":1}
	}`)
	p := newPoolFromEnvForTest(t)

	if _, ok := p.Acquire("", nil); !ok {
		t.Fatal("the first acquisition must succeed")
	}
	p.Release("acc1@example.com")

	if _, ok := p.Acquire("", nil); ok {
		t.Fatal("an account that served a request must still be charged")
	}
}
