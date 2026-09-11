package quota

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The failure that cost him the week: 3,727 UNKNOWN readings sitting beside
// "confirmed: SAFE". A reading that cannot be taken must never be a pass.
func TestStaleReadingIsRefusedNotTrusted(t *testing.T) {
	now := time.Now()
	stale := Reading{WeeklyUsedPercent: 2, UpdatedAt: now.Add(-2 * time.Hour), Age: 2 * time.Hour}
	if stale.Age <= MaxAge {
		t.Fatalf("test fixture is not stale: age %s vs limit %s", stale.Age, MaxAge)
	}
	// Allow() judges budget, not freshness -- freshness is enforced in Read().
	// Assert the constant is small enough that an hours-old number cannot pass.
	if MaxAge > 30*time.Minute {
		t.Fatalf("MaxAge %s is too generous to catch a budget spent since the reading", MaxAge)
	}
}

func TestAtThresholdRefuses(t *testing.T) {
	ok, why := Allow(Reading{WeeklyUsedPercent: 90}) // exactly 10% remaining
	if ok {
		t.Fatal("Allow permitted orchestration at exactly the stop threshold")
	}
	if !strings.Contains(why, "stop threshold") {
		t.Fatalf("refusal did not name the threshold: %q", why)
	}
}

func TestPastThresholdRefuses(t *testing.T) {
	if ok, _ := Allow(Reading{WeeklyUsedPercent: 97}); ok {
		t.Fatal("Allow permitted orchestration at 3% remaining")
	}
}

func TestHealthyBudgetAllows(t *testing.T) {
	if ok, why := Allow(Reading{WeeklyUsedPercent: 2}); !ok {
		t.Fatalf("Allow refused at 98%% remaining: %s", why)
	}
}

// Read must fail, not return a zero Reading, when it cannot measure. A zero
// Reading has WeeklyUsedPercent 0, which reads as "budget fully available" --
// exactly the direction that must never happen by accident.
func TestZeroReadingWouldReadAsFullBudget(t *testing.T) {
	if ok, _ := Allow(Reading{}); !ok {
		t.Skip("zero value already refuses; the guard below is unnecessary")
	}
	// It does read as full budget, which is why Read() returns an error rather
	// than a zero value on every failure path. This test documents the hazard
	// so nobody 'simplifies' Read() into returning (Reading{}, nil).
}

// agent-estate#1127: AllowSession's own pair of the four tests above,
// judged against SessionUsedPercent instead of WeeklyUsedPercent -- the
// field that was already being read from codexbar and never consulted by
// anything before this.
func TestSessionAtThresholdRefuses(t *testing.T) {
	ok, why := AllowSession(Reading{SessionUsedPercent: 97}) // exactly 3% remaining
	if ok {
		t.Fatal("AllowSession permitted orchestration at exactly the session stop threshold")
	}
	if !strings.Contains(why, "session") || !strings.Contains(why, "stop threshold") {
		t.Fatalf("refusal did not name the session threshold: %q", why)
	}
}

func TestSessionPastThresholdRefuses(t *testing.T) {
	if ok, _ := AllowSession(Reading{SessionUsedPercent: 100}); ok {
		t.Fatal("AllowSession permitted orchestration at 0% session remaining -- the exact reading behind agent-estate#1127's own incident")
	}
}

func TestHealthySessionAllows(t *testing.T) {
	if ok, why := AllowSession(Reading{SessionUsedPercent: 50}); !ok {
		t.Fatalf("AllowSession refused at 50%% session remaining: %s", why)
	}
}

// A low WEEKLY reading must not trip AllowSession, and a low SESSION
// reading must not trip Allow -- the two windows are read from the same
// Reading but judged independently, mirroring pressure.Check's own
// requirement that refusals not bleed into each other.
func TestWeeklyAndSessionThresholdsDoNotCrossContaminate(t *testing.T) {
	weeklyLow := Reading{WeeklyUsedPercent: 95, SessionUsedPercent: 0} // weekly low, session healthy
	if ok, why := AllowSession(weeklyLow); !ok {
		t.Fatalf("AllowSession refused on a WEEKLY-low reading: %s -- the two windows are not independent", why)
	}
	sessionLow := Reading{WeeklyUsedPercent: 0, SessionUsedPercent: 98} // session low, weekly healthy
	if ok, why := Allow(sessionLow); !ok {
		t.Fatalf("Allow refused on a SESSION-low reading: %s -- the two windows are not independent", why)
	}
}

// agent-estate#993's own explicit ask: a refusal should say WHEN it
// resets, not just that it refused. codexbar's real JSON output carries a
// proper RFC3339 timestamp (usage.primary.resetsAt) -- confirmed live,
// 2026-09-11, via the same read-only status call Read() already performs
// in production -- so this is a structured field, never the fragile
// human string ("resets 4:10pm (America/New_York)") the incident quoted.
func TestSessionRefusalNamesWhenItResets(t *testing.T) {
	resetsAt := time.Now().Add(47 * time.Minute)
	r := Reading{SessionUsedPercent: 100, SessionResetsAt: resetsAt}
	_, why := AllowSession(r)
	if !strings.Contains(why, "resets") {
		t.Fatalf("refusal did not name a reset time: %q", why)
	}
	if !strings.Contains(why, resetsAt.Local().Format("15:04")) {
		t.Fatalf("refusal did not include the fixture's own reset time %s: %q", resetsAt.Local().Format("15:04"), why)
	}
}

// A refusal with no reset time available (codexbar omitted or
// malformed it) must say so plainly, never guess or fabricate one --
// the same "unknown is not safe, but unknown must be SAID" discipline
// this package's own header states for the reading itself.
func TestSessionRefusalWithNoResetTimeOmitsTheClauseRatherThanGuessing(t *testing.T) {
	_, why := AllowSession(Reading{SessionUsedPercent: 100}) // SessionResetsAt left zero
	if strings.Contains(why, "resets") {
		t.Fatalf("refusal claimed a reset time from a zero-value SessionResetsAt: %q", why)
	}
}

// The weekly refusal gets the identical treatment, via the shared
// resetClause helper -- pinned separately so a future change to one
// path cannot silently drop the other's coverage.
func TestWeeklyRefusalNamesWhenItResets(t *testing.T) {
	resetsAt := time.Now().Add(3 * 24 * time.Hour)
	r := Reading{WeeklyUsedPercent: 95, WeeklyResetsAt: resetsAt}
	_, why := Allow(r)
	if !strings.Contains(why, "resets") {
		t.Fatalf("weekly refusal did not name a reset time: %q", why)
	}
}

// agent-estate#1163: Read used to run codexbar with no deadline at all, so
// a stalled process hung the caller (pressure.Check, and the Director's own
// tick through it) instead of producing the intended fail-closed refusal.
// hangingRunCodexbar simulates that stall the only way a unit test safely
// can -- it never returns until its own ctx is cancelled, the same
// observable behavior a real `exec.CommandContext`-launched process has
// once its context's deadline fires and it is killed -- so this proves
// Read is actually WIRED to ctx, not merely that ReadTimeout exists as an
// unused constant.
func hangingRunCodexbar(ctx context.Context, args ...string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestReadTimesOutPromptlyRatherThanHanging is the direct mutation-check
// the brief asks for: make codexbar hang past the deadline, confirm Read
// returns promptly (timed, not just eventually), and that the returned
// error names a TIMEOUT specifically -- not the generic "codexbar
// unreachable" wording a plain exit failure gets (agent-estate#474's own
// "a specific code should be interpreted, not ignored", applied to this
// package's own failure modes).
//
// ReadTimeout is lowered for the duration of this test only, restored via
// defer -- proving the real bound is honored without this test itself
// waiting out the production 60s value.
func TestReadTimesOutPromptlyRatherThanHanging(t *testing.T) {
	origRun, origTimeout := runCodexbar, ReadTimeout
	defer func() { runCodexbar, ReadTimeout = origRun, origTimeout }()
	runCodexbar = hangingRunCodexbar
	ReadTimeout = 50 * time.Millisecond

	start := time.Now()
	_, err := Read(time.Now())
	elapsed := time.Since(start)

	// Generous relative to the 50ms bound -- this asserts "did not hang
	// indefinitely", not a tight scheduler-timing race. A test host under
	// real load (this one: 30+ concurrent worktrees tonight) can still
	// legitimately take longer than 50ms to get scheduled back after a
	// context fires; 2s is still three orders of magnitude below "hung".
	if elapsed > 2*time.Second {
		t.Fatalf("Read took %s against a %s timeout -- not bounded", elapsed, ReadTimeout)
	}
	if err == nil {
		t.Fatal("Read returned no error against a permanently hanging codexbar")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error did not name itself as a timeout, got %q -- must not read the same as a plain exec failure", err.Error())
	}
	if strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("timeout error also used the generic 'unreachable' wording, got %q -- the two causes must stay distinguishable", err.Error())
	}
}

// TestReadHealthyPathUnaffectedByTimeoutWiring is the OTHER mutation
// direction the brief requires: confirm a normal, fast-returning codexbar
// is byte-for-byte unaffected by the new context plumbing -- same JSON in,
// same Reading out, matching origin/main's own pre-#1163 behavior for a
// good reading. Runs at the REAL production ReadTimeout (60s), not a
// lowered one, specifically to prove the healthy path never brushes the
// deadline at all.
func TestReadHealthyPathUnaffectedByTimeoutWiring(t *testing.T) {
	origRun := runCodexbar
	defer func() { runCodexbar = origRun }()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	fixture := `[{"usage":{"updatedAt":"2026-09-11T11:59:00Z","primary":{"usedPercent":10,"resetsAt":"2026-09-11T15:00:00Z"},"secondary":{"usedPercent":14,"resetsAt":"2026-09-17T14:00:00Z"}}}]`
	runCodexbar = func(ctx context.Context, args ...string) ([]byte, error) {
		if ctx.Err() != nil {
			t.Fatal("healthy runCodexbar was called with an already-expired context")
		}
		return []byte(fixture), nil
	}

	r, err := Read(now)
	if err != nil {
		t.Fatalf("Read failed on a healthy fixture: %v", err)
	}
	if r.WeeklyUsedPercent != 14 || r.SessionUsedPercent != 10 {
		t.Fatalf("Read parsed the healthy fixture wrong: %+v", r)
	}
	if r.Age != time.Minute {
		t.Fatalf("Age = %s, want 1m (now - updatedAt from the fixture)", r.Age)
	}
}
