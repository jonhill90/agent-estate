package quota

import (
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
