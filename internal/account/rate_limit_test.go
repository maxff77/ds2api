package account

import "testing"

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
