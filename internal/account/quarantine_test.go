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
