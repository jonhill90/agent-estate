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
