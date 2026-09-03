package reconcile

import (
	"strings"
	"testing"
	"time"
)

func now() time.Time { return time.Date(2026, 9, 3, 6, 0, 0, 0, time.UTC) }

// The rule that must not break: never free a slot for a turn you did not
// observe finish. A worktree that still exists means the turn might be alive,
// and age is not evidence -- a long turn and a dead one look identical.
func TestALiveWorktreeKeepsItsSlotHoweverOld(t *testing.T) {
	c := Candidate{ID: "a", State: "dispatched", Worktree: "/wt/a",
		At: now().Add(-48 * time.Hour)}
	v := Judge([]Candidate{c}, func(string) bool { return true }, now())
	if v[0].Reclaim {
		t.Fatal("an existing worktree must never be reclaimed on age alone")
	}
	if !strings.Contains(v[0].Reason, "look alike") {
		t.Errorf("the reason should say why age is not evidence: %s", v[0].Reason)
	}
}

// The positive observation: the worktree is gone, so the turn has nowhere to
// be writing. That is a fact, not a guess.
func TestAVanishedWorktreeFreesTheSlot(t *testing.T) {
	c := Candidate{ID: "b", State: "dispatched", Worktree: "/wt/b", At: now()}
	v := Judge([]Candidate{c}, func(string) bool { return false }, now())
	if !v[0].Reclaim {
		t.Fatal("a turn whose worktree is gone cannot be running")
	}
	if !strings.Contains(v[0].Reason, "nowhere to be writing") {
		t.Errorf("the reason must state the observation: %s", v[0].Reason)
	}
}

// Could-not-measure never frees a slot.
func TestNoWorktreeRecordedIsNeverReclaimed(t *testing.T) {
	c := Candidate{ID: "c", State: "dispatched", Worktree: "", At: now()}
	v := Judge([]Candidate{c}, func(string) bool { return false }, now())
	if v[0].Reclaim {
		t.Fatal("with nothing to observe, nothing may be reclaimed")
	}
}

// A state this package does not understand is reported, never reclaimed.
// Tonight four records written by a newer binary carried a state the running
// binary did not know, and they held slots forever -- but guessing they had
// ended would have been worse than reporting them.
func TestAnUnknownStateIsReportedNotReclaimed(t *testing.T) {
	c := Candidate{ID: "d", State: "authored", Worktree: "/gone", At: now()}
	v := Judge([]Candidate{c}, func(string) bool { return false }, now())
	if v[0].Reclaim {
		t.Fatal("a state we do not understand is not evidence a turn ended")
	}
	if !strings.Contains(v[0].Reason, "authored") {
		t.Errorf("the reason must name the state it could not judge: %s", v[0].Reason)
	}
}

// "unknown" is judged the same way as "dispatched": it is not terminal, so it
// still holds a slot, and only a vanished worktree frees it.
func TestUnknownIsJudgedByObservationToo(t *testing.T) {
	live := Judge([]Candidate{{ID: "e", State: "unknown", Worktree: "/wt/e"}},
		func(string) bool { return true }, now())
	if live[0].Reclaim {
		t.Error("an unknown turn with a live worktree keeps its slot")
	}
	gone := Judge([]Candidate{{ID: "f", State: "unknown", Worktree: "/wt/f"}},
		func(string) bool { return false }, now())
	if !gone[0].Reclaim {
		t.Error("an unknown turn whose worktree is gone can be reclaimed")
	}
}
