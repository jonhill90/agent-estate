package tick

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeEscalations creates an escalation log at a path distinct from the
// tick log, mirroring write()'s helper for internal/tick's own log.
func writeEscalations(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tick-escalations.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRecordEscalationRequiresPhaseItem(t *testing.T) {
	p := filepath.Join(t.TempDir(), "esc.jsonl")
	err := RecordEscalation(p, EscalationEntry{At: time.Now(), Where: "telegram"})
	if err == nil {
		t.Fatal("an escalation naming no phase item must be refused")
	}
}

func TestRecordEscalationRequiresWhoWasTold(t *testing.T) {
	p := filepath.Join(t.TempDir(), "esc.jsonl")
	err := RecordEscalation(p, EscalationEntry{At: time.Now(), PhaseItem: "phase-0"})
	if err == nil {
		t.Fatal("an escalation naming no recipient must be refused -- it is not evidence anyone was told")
	}
}

// The deadlock itself: three stalled ticks, nobody told anyone. Stalled, and
// NOT escalated -- exit 1 territory, the loop must stop.
func TestCheckWithEscalationUnacknowledgedStallStaysUnescalated(t *testing.T) {
	tickPath := write(t, stalledA, stalledB, stalledC)
	escPath := writeEscalations(t) // empty: nobody escalated
	v, err := CheckWithEscalation(tickPath, escPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Stalled {
		t.Fatal("three artifact-less ticks must still report stalled")
	}
	if v.Escalated {
		t.Fatal("no escalation was recorded; Escalated must be false")
	}
}

// The fix: an escalation naming the exact same phase item and src head as
// the most recent tick, timestamped after it, acknowledges the stall --
// Stalled stays true (the work genuinely has not moved), but Escalated
// becomes true so a caller can act differently.
func TestCheckWithEscalationAcknowledgesMatchingStall(t *testing.T) {
	tickPath := write(t, stalledA, stalledB, stalledC)
	esc := `{"at":"2026-09-02T10:07:00Z","phase_item":"phase-0","src_head":"aaa","where":"telegram"}`
	escPath := writeEscalations(t, esc)
	v, err := CheckWithEscalation(tickPath, escPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Stalled {
		t.Fatal("an escalation must not clear the stall -- the work still has not moved")
	}
	if !v.Escalated {
		t.Fatal("a matching, fresh escalation must acknowledge the stall")
	}
	if v.EscalationCount != 1 {
		t.Fatalf("expected EscalationCount 1, got %d", v.EscalationCount)
	}
	if v.EscalatedAt.IsZero() {
		t.Fatal("EscalatedAt must be set once acknowledged")
	}
}

// An escalation recorded BEFORE the tick it would claim to acknowledge does
// not count. Otherwise a single stale escalation from an earlier, different
// stall would silently paper over every stall that comes after it forever.
func TestCheckWithEscalationStaleEscalationDoesNotAcknowledge(t *testing.T) {
	tickPath := write(t, stalledA, stalledB, stalledC)
	// Timestamped BEFORE stalledC, the most recent tick.
	esc := `{"at":"2026-09-02T10:01:00Z","phase_item":"phase-0","src_head":"aaa","where":"telegram"}`
	escPath := writeEscalations(t, esc)
	v, err := CheckWithEscalation(tickPath, escPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Stalled {
		t.Fatal("must still be stalled")
	}
	if v.Escalated {
		t.Fatal("an escalation older than the most recent tick must not acknowledge it")
	}
}

// An escalation for a DIFFERENT phase item or src head does not acknowledge
// the current stall -- it is evidence a human was told about something
// else.
func TestCheckWithEscalationMismatchedStallDoesNotAcknowledge(t *testing.T) {
	tickPath := write(t, stalledA, stalledB, stalledC)
	esc := `{"at":"2026-09-02T10:07:00Z","phase_item":"phase-1","src_head":"aaa","where":"telegram"}`
	escPath := writeEscalations(t, esc)
	v, err := CheckWithEscalation(tickPath, escPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Escalated {
		t.Fatal("an escalation for a different phase item must not acknowledge this stall")
	}
}

// Repeated escalation against the SAME stall must remain visible, not
// collapse into a single "escalated" reading indistinguishable from one
// escalation.
func TestCheckWithEscalationRepeatedEscalationIsCountedAndStaysStalled(t *testing.T) {
	tickPath := write(t, stalledA, stalledB, stalledC)
	escPath := writeEscalations(t,
		`{"at":"2026-09-02T10:07:00Z","phase_item":"phase-0","src_head":"aaa","where":"telegram"}`,
		`{"at":"2026-09-02T10:08:00Z","phase_item":"phase-0","src_head":"aaa","where":"telegram"}`,
		`{"at":"2026-09-02T10:09:00Z","phase_item":"phase-0","src_head":"aaa","where":"telegram"}`,
	)
	v, err := CheckWithEscalation(tickPath, escPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Stalled {
		t.Fatal("must still be stalled -- three escalations are not three pieces of work")
	}
	if !v.Escalated {
		t.Fatal("must be acknowledged")
	}
	if v.EscalationCount != 3 {
		t.Fatalf("expected EscalationCount 3, got %d", v.EscalationCount)
	}
	if v.Reason == "" || v.EscalationCount <= 1 {
		t.Fatal("repeated escalation must be visible, not read identically to a single one")
	}
}

// The property that matters most: a loop that escalates and THEN produces
// real work recovers. Escalating never blocks the ordinary artifact rule
// from clearing a stall once real work shows up.
func TestCheckWithEscalationThenRealWorkRecovers(t *testing.T) {
	tickPath := write(t, stalledA, stalledB, stalledC)
	escPath := writeEscalations(t,
		`{"at":"2026-09-02T10:07:00Z","phase_item":"phase-0","src_head":"aaa","where":"telegram"}`,
	)
	// Confirm it is acknowledged first.
	v, err := CheckWithEscalation(tickPath, escPath, nil)
	if err != nil || !v.Escalated {
		t.Fatalf("setup: expected an acknowledged stall, got %+v, err=%v", v, err)
	}

	// Now the loop records a real artifact -- append a 4th tick.
	f, err := os.OpenFile(tickPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"at":"2026-09-02T10:10:00Z","phase_item":"phase-0","src_head":"bbb","artifact":"docs/shipped.md"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	v2, err := CheckWithEscalation(tickPath, escPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v2.Stalled {
		t.Fatalf("real work after an escalation must recover the loop: %s", v2.Reason)
	}
}

// The other direction, required just as strongly: a loop that ONLY ever
// escalates -- no real artifact, ever -- must stay stalled indefinitely.
// Escalating on every tick must never look like a healthy, moving loop.
func TestCheckWithEscalationOnlyEscalatingNeverRecoversOnItsOwn(t *testing.T) {
	tickPath := write(t, stalledA, stalledB, stalledC)
	escPath := writeEscalations(t)

	rounds := []struct{ escAt, tickAt string }{
		{"2026-09-02T10:07:00Z", "2026-09-02T10:08:00Z"},
		{"2026-09-02T10:09:00Z", "2026-09-02T10:10:00Z"},
		{"2026-09-02T10:11:00Z", "2026-09-02T10:12:00Z"},
	}
	for _, r := range rounds {
		// Escalate the current stall.
		ef, err := os.OpenFile(escPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ef.WriteString(`{"at":"` + r.escAt + `","phase_item":"phase-0","src_head":"aaa","where":"telegram"}` + "\n"); err != nil {
			t.Fatal(err)
		}
		ef.Close()

		v, err := CheckWithEscalation(tickPath, escPath, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !v.Stalled {
			t.Fatalf("a loop that only ever escalates must stay stalled; recovered at round with esc %s", r.escAt)
		}

		// The loop ticks again, still producing nothing (same phase item,
		// same src head: no real work happened).
		tf, err := os.OpenFile(tickPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tf.WriteString(`{"at":"` + r.tickAt + `","phase_item":"phase-0","src_head":"aaa","artifact":null}` + "\n"); err != nil {
			t.Fatal(err)
		}
		tf.Close()
	}

	// Final state: still stalled.
	v, err := CheckWithEscalation(tickPath, escPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Stalled {
		t.Fatal("a loop that only ever escalates must remain stalled after every round")
	}
}

// Adversarial: CheckWithResolver (no escalation awareness at all) must be
// completely blind to escalations, however many are recorded, because they
// live in a different file under a different type with no artifact field.
// This is the structural guarantee, not a behavioural coincidence: verify it
// even when the resolver would treat a "://"-shaped token as automatically
// valid, the exact dodge RecordEscalation's doc comment names.
func TestCheckWithResolverNeverSeesEscalations(t *testing.T) {
	tickPath := write(t, stalledA, stalledB, stalledC)
	escPath := writeEscalations(t,
		`{"at":"2026-09-02T10:07:00Z","phase_item":"phase-0","src_head":"aaa","where":"https://t.me/some-chat"}`,
		`{"at":"2026-09-02T10:08:00Z","phase_item":"phase-0","src_head":"aaa","where":"https://t.me/some-chat"}`,
		`{"at":"2026-09-02T10:09:00Z","phase_item":"phase-0","src_head":"aaa","where":"https://t.me/some-chat"}`,
	)
	_ = escPath // deliberately never passed to CheckWithResolver below

	alwaysValid := func(string, string) (Resolution, string) { return ResolveValid, "stub" }
	v, err := CheckWithResolver(tickPath, alwaysValid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Stalled {
		t.Fatal("CheckWithResolver must still see the three artifact-less ticks as stalled -- escalations recorded elsewhere must have zero effect")
	}
}

// Same adversarial point from the writer's side: an artifact string shaped
// like an escalation record (the exact JSON RecordEscalation would have
// written) is not a way to smuggle "told a human" into the tick log as
// evidence of output -- it still has to pass Validate like anything else,
// and a bare recipient name resolves no path, sha, or issue.
func TestEscalationShapedArtifactCannotPassValidateAsWork(t *testing.T) {
	artifact := `{"at":"2026-09-02T10:07:00Z","phase_item":"phase-0","src_head":"aaa","where":"telegram"}`
	if err := Validate(artifact, time.Time{}, nil); err == nil {
		t.Fatal("an escalation-shaped string naming no path, sha, issue, or URL must not validate as an artifact")
	}
}
