package account

import (
	"testing"
	"time"
)

func TestQuarantineHoldsAccountForBaseWindow(t *testing.T) {
	q := NewQuarantine(time.Hour)
	q.Ban("a", "token_invalid")

	if !q.Active("a") {
		t.Fatal("a banned account must be held")
	}
	records := q.List()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Strikes != 1 {
		t.Fatalf("expected 1 strike, got %d", records[0].Strikes)
	}
	if got := records[0].Until.Sub(records[0].BannedAt); got != time.Hour {
		t.Fatalf("expected the base window, got %v", got)
	}
	if records[0].Reason != "token_invalid" {
		t.Fatalf("expected the reason to be kept, got %q", records[0].Reason)
	}
}

func TestQuarantineDoublesWindowPerStrike(t *testing.T) {
	q := NewQuarantine(time.Hour)
	for _, want := range []time.Duration{time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour} {
		q.expireForTest("a")
		q.Ban("a", "token_invalid")
		rec := q.List()[0]
		if got := rec.Until.Sub(rec.BannedAt); got != want {
			t.Fatalf("strike %d: expected window %v, got %v", rec.Strikes, want, got)
		}
	}
}

func TestQuarantineCapsTheWindow(t *testing.T) {
	q := NewQuarantine(time.Hour)
	for i := 0; i < 12; i++ {
		q.expireForTest("a")
		q.Ban("a", "token_invalid")
	}
	rec := q.List()[0]
	if got := rec.Until.Sub(rec.BannedAt); got != 16*time.Hour {
		t.Fatalf("expected the window to cap at 16x the base, got %v", got)
	}
}

func TestQuarantineLapsesOnItsOwn(t *testing.T) {
	q := NewQuarantine(time.Millisecond)
	q.Ban("a", "token_invalid")
	time.Sleep(5 * time.Millisecond)

	if q.Active("a") {
		t.Fatal("an expired record must not hold the account")
	}
}

func TestQuarantineReleaseClearsStrikes(t *testing.T) {
	q := NewQuarantine(time.Hour)
	q.Ban("a", "token_invalid")
	q.expireForTest("a")
	q.Ban("a", "token_invalid")
	q.Release("a")

	if q.Active("a") {
		t.Fatal("a released account must not be held")
	}
	if len(q.List()) != 0 {
		t.Fatal("a released account must leave the list")
	}

	// A replaced token starts clean: the next ban is a first strike again.
	q.Ban("a", "token_invalid")
	if got := q.List()[0].Strikes; got != 1 {
		t.Fatalf("expected release to clear strikes, got %d", got)
	}
}

func TestQuarantineIsPerAccount(t *testing.T) {
	q := NewQuarantine(time.Hour)
	q.Ban("a", "token_invalid")

	if q.Active("b") {
		t.Fatal("banning one account must not hold another")
	}
}

func TestQuarantinedAccountIsNotHandedOut(t *testing.T) {
	p := newSingleAccountPoolForTest(t, "2")
	p.QuarantineAccount("acc1@example.com", "token_invalid")

	if _, ok := p.Acquire("", nil); ok {
		t.Fatal("rotation must skip a quarantined account")
	}
	if _, ok := p.Acquire("acc1@example.com", nil); ok {
		t.Fatal("targeted acquisition must skip a quarantined account too")
	}
}

func TestReleasedAccountIsHandedOutAgain(t *testing.T) {
	p := newSingleAccountPoolForTest(t, "2")
	p.QuarantineAccount("acc1@example.com", "token_invalid")
	p.ReleaseAccount("acc1@example.com")

	if _, ok := p.Acquire("", nil); !ok {
		t.Fatal("a released account must return to rotation")
	}
}

func TestQuarantineLeavesOtherAccountsAcquirable(t *testing.T) {
	p := newPoolForTest(t, "2")
	p.QuarantineAccount("acc1@example.com", "token_invalid")

	acc, ok := p.Acquire("", nil)
	if !ok {
		t.Fatal("expected the healthy account to still be acquirable")
	}
	if acc.Email == "acc1@example.com" {
		t.Fatal("expected rotation to skip the quarantined account")
	}
}

// A strike is one suspension episode, not one retry. The client retries a
// failed refresh several times per request and each retry reaches Ban, so
// counting those separately drove a first failure straight to the 16x cap.
func TestQuarantineDoesNotRestrikeWhileStillActive(t *testing.T) {
	q := NewQuarantine(time.Hour)

	for i := 0; i < 6; i++ {
		q.Ban("a", "refresh_failed")
	}

	rec := q.List()[0]
	if rec.Strikes != 1 {
		t.Fatalf("retries within one episode must stay a single strike, got %d", rec.Strikes)
	}
	if got := rec.Until.Sub(rec.BannedAt); got != time.Hour {
		t.Fatalf("expected the base window, got %v", got)
	}
}

// A ban after the window has lapsed is a genuinely new episode.
func TestQuarantineStrikesAgainAfterWindowLapses(t *testing.T) {
	q := NewQuarantine(time.Millisecond)
	q.Ban("a", "refresh_failed")
	time.Sleep(5 * time.Millisecond)

	q.Ban("a", "refresh_failed")

	if got := q.List()[0].Strikes; got != 2 {
		t.Fatalf("a lapsed window then a new ban is the second episode, got %d strikes", got)
	}
}

// The admin availability figure must agree with what the pool will actually
// hand out, or the operator reads "1 available" while every request fails.
func TestStatusExcludesQuarantinedAccounts(t *testing.T) {
	p := newPoolForTest(t, "2")
	p.QuarantineAccount("acc1@example.com", "refresh_failed")

	status := p.Status()
	if got := status["available"].(int); got != 1 {
		t.Fatalf("expected 1 available account, got %d", got)
	}
	for _, id := range status["available_accounts"].([]string) {
		if id == "acc1@example.com" {
			t.Fatal("a quarantined account must not be listed as available")
		}
	}
	if got := status["quarantined"].(int); got != 1 {
		t.Fatalf("expected the quarantined count to be reported, got %v", status["quarantined"])
	}
}

// Retries within an episode must not restrike, but a more severe reason
// arriving during the window is information worth keeping -- silently dropping
// it would leave the admin panel showing the milder cause.
func TestQuarantineKeepsLatestReasonWithoutRestriking(t *testing.T) {
	q := NewQuarantine(time.Hour)
	q.Ban("a", "refresh_failed")
	q.Ban("a", "permanently_banned_by_upstream")

	rec := q.List()[0]
	if rec.Strikes != 1 {
		t.Fatalf("a reason update must not count as a new episode, got %d strikes", rec.Strikes)
	}
	if rec.Reason != "permanently_banned_by_upstream" {
		t.Fatalf("expected the later reason to be kept, got %q", rec.Reason)
	}
	if got := rec.Until.Sub(rec.BannedAt); got != time.Hour {
		t.Fatalf("a reason update must not extend the window, got %v", got)
	}
}
