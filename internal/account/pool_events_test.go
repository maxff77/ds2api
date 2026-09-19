package account

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// captureLogs swaps the default structured logger for a buffer for the
// duration of the test. The emitted record is the deliverable here -- the
// operator reads it from the container log stream -- so asserting on it is
// asserting on external behaviour.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestRotatingAcquireEmitsEventNamingTheAccount(t *testing.T) {
	buf := captureLogs(t)
	p := newSingleAccountPoolForTest(t, "1")

	if _, ok := p.Acquire("", nil); !ok {
		t.Fatal("expected acquire to succeed")
	}

	out := buf.String()
	if !strings.Contains(out, `"msg":"ds_acquire"`) {
		t.Fatalf("expected a ds_acquire event, got: %s", out)
	}
	if !strings.Contains(out, "acc1@example.com") {
		t.Fatalf("expected the event to name the account, got: %s", out)
	}
}

func TestTargetedAcquireEmitsEventNamingTheAccount(t *testing.T) {
	buf := captureLogs(t)
	p := newSingleAccountPoolForTest(t, "1")

	if _, ok := p.Acquire("acc1@example.com", nil); !ok {
		t.Fatal("expected targeted acquire to succeed")
	}

	out := buf.String()
	if !strings.Contains(out, `"msg":"ds_acquire"`) {
		t.Fatalf("targeted acquisition must emit the event too, got: %s", out)
	}
	if !strings.Contains(out, "acc1@example.com") {
		t.Fatalf("expected the event to name the account, got: %s", out)
	}
}

func TestAcquireEventCarriesInflightCount(t *testing.T) {
	buf := captureLogs(t)
	p := newPoolForTest(t, "2")

	if _, ok := p.Acquire("acc1@example.com", nil); !ok {
		t.Fatal("expected acquire to succeed")
	}
	if _, ok := p.Acquire("acc1@example.com", nil); !ok {
		t.Fatal("expected second acquire on the same account to succeed")
	}

	out := buf.String()
	if !strings.Contains(out, `"inflight":2`) {
		t.Fatalf("expected the second event to report inflight 2, got: %s", out)
	}
}
