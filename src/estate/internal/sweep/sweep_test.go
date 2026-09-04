package sweep

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/reclaim"
)

const root = "/tmp/estate-dispatch/repo-abc123"

func rec(id string, state ledger.State) ledger.Record {
	return ledger.Record{
		ID:       id,
		Issue:    "1000",
		Lane:     id,
		State:    state,
		At:       time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		Worktree: filepath.Join(root, id),
		Branch:   "dispatch/" + id,
		Base:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

// alive and dead are the two shapes reclaim can positively distinguish.
func alive(pid int) (reclaim.ProcessInfo, error) {
	return reclaim.ProcessInfo{Exists: true, Comm: "claude"}, nil
}
func dead(pid int) (reclaim.ProcessInfo, error) {
	return reclaim.ProcessInfo{Exists: false}, nil
}

// cfg is a Config that says yes to every path and records what it was asked
// to remove, so a test asserts on the DECISION rather than on git.
func cfg(probe reclaim.Probe) (Config, *[]string) {
	var removed []string
	c := Config{
		Root:   root,
		Probe:  probe,
		Exists: func(string) bool { return true },
		Remove: func(r ledger.Record) error {
			removed = append(removed, r.ID)
			return nil
		},
	}
	return c, &removed
}

func find(t *testing.T, results []Result, id string) Result {
	t.Helper()
	for _, r := range results {
		if r.Record.ID == id {
			return r
		}
	}
	t.Fatalf("no result for %s", id)
	return Result{}
}

// The direction that fixes the leak: a turn that finished is swept.
func TestATerminalTurnsWorktreeIsSwept(t *testing.T) {
	c, removed := cfg(alive)
	results := Run([]ledger.Record{rec("done", ledger.Complete), rec("broke", ledger.Failed)}, c)
	for _, id := range []string{"done", "broke"} {
		if r := find(t, results, id); !r.Removed {
			t.Fatalf("%s reached a terminal state and its worktree was not swept: %s", id, r.Reason)
		}
	}
	if len(*removed) != 2 {
		t.Fatalf("expected both terminal turns offered for removal, got %v", *removed)
	}
}

// The direction that must never regress, and the one agent-estate#1000
// names explicitly: `unknown` is never swept, at any age, whatever the
// process is doing. A timed-out turn may have produced work nothing has
// collected, and its worktree is the only copy.
func TestAnUnknownTurnIsNeverSweptEvenWhenItsProcessIsGone(t *testing.T) {
	c, removed := cfg(dead)
	r := find(t, Run([]ledger.Record{rec("timed-out", ledger.Unknown)}, c), "timed-out")
	if r.Eligible || r.Removed {
		t.Fatalf("an unknown turn's worktree was swept: %s", r.Reason)
	}
	if len(*removed) != 0 {
		t.Fatalf("Remove was called for an unknown turn: %v", *removed)
	}
	if !strings.Contains(r.Reason, "unknown") {
		t.Fatalf("the refusal does not say why: %s", r.Reason)
	}
}

// A turn still recorded in flight whose process is alive is not a corpse.
// Its worktree is where a running agent is working right now.
func TestALiveInFlightTurnsWorktreeIsLeftAlone(t *testing.T) {
	c, removed := cfg(alive)
	in := rec("running", ledger.Dispatched)
	in.PID = 4242
	r := find(t, Run([]ledger.Record{in}, c), "running")
	if r.Eligible || r.Removed {
		t.Fatalf("a live turn's worktree was swept out from under it: %s", r.Reason)
	}
	if len(*removed) != 0 {
		t.Fatalf("Remove was called for a live turn: %v", *removed)
	}
}

// The corpse. A dispatch killed outright -- OOM, SIGKILL, a lost tmux
// server -- never records a terminal state and never runs any teardown of
// its own. Nothing but a later process reading the record can collect it,
// and only a POSITIVE observation that the process is gone makes it a
// candidate.
func TestAKilledDispatchesWorktreeIsSweptByALaterProcess(t *testing.T) {
	c, removed := cfg(dead)
	in := rec("oom-killed", ledger.Dispatched)
	in.PID = 4242
	r := find(t, Run([]ledger.Record{in}, c), "oom-killed")
	if !r.Removed {
		t.Fatalf("a killed dispatch's worktree leaked: %s", r.Reason)
	}
	if len(*removed) != 1 {
		t.Fatalf("expected the corpse's worktree offered for removal, got %v", *removed)
	}
}

// Age is not evidence, and neither is a pid nobody could look at. This is
// reclaim's rule; the sweep must not have its own, softer one.
func TestAnInFlightTurnWithNothingObservableIsLeftAlone(t *testing.T) {
	c, _ := cfg(func(int) (reclaim.ProcessInfo, error) {
		return reclaim.ProcessInfo{}, errors.New("ps unavailable")
	})
	unprobeable := rec("cannot-look", ledger.Dispatched)
	unprobeable.PID = 4242
	noPID := rec("no-pid", ledger.Dispatched)

	results := Run([]ledger.Record{unprobeable, noPID}, c)
	for _, id := range []string{"cannot-look", "no-pid"} {
		if r := find(t, results, id); r.Eligible {
			t.Fatalf("%s was swept on no positive observation at all: %s", id, r.Reason)
		}
	}
}

// A recorded path is a string out of a file. `git worktree remove` is what
// it feeds. Confinement to this repository's own dispatch root is checked
// before anything is offered to Remove at all -- internal/isolate.Reattach
// checks it again, and both would have to be broken for a bad path to
// reach git.
func TestAPathOutsideTheDispatchRootIsNeverOffered(t *testing.T) {
	c, removed := cfg(alive)
	cases := map[string]string{
		"elsewhere":     "/Users/jon/source/repos/Personal/agent-estate",
		"the root":      root,
		"nested":        filepath.Join(root, "some-turn", "src"),
		"traversal":     filepath.Join(root, "..", "other-repo", "turn"),
		"another repo":  "/tmp/estate-dispatch/repo-zzz999/turn",
		"relative junk": "turn",
	}
	var records []ledger.Record
	for name, path := range cases {
		r := rec(name, ledger.Complete)
		r.Worktree = path
		records = append(records, r)
	}
	for _, res := range Run(records, c) {
		if res.Eligible {
			t.Fatalf("%s (%s) was offered for removal", res.Record.ID, res.Record.Worktree)
		}
	}
	if len(*removed) != 0 {
		t.Fatalf("something outside the dispatch root was offered to Remove: %v", *removed)
	}
}

// Records written before agent-estate#1000 added the field carry no
// worktree path. They are reported as unsweepable, never guessed at from
// the id -- guessing a path is how a sweep starts deleting the wrong thing.
func TestARecordWithNoWorktreePathIsReportedNotGuessed(t *testing.T) {
	c, removed := cfg(alive)
	old := rec("pre-1000", ledger.Complete)
	old.Worktree = ""
	r := find(t, Run([]ledger.Record{old}, c), "pre-1000")
	if r.Eligible {
		t.Fatal("a record with no recorded worktree path was offered for removal anyway")
	}
	if len(*removed) != 0 {
		t.Fatalf("Remove was called with no path: %v", *removed)
	}
	if !strings.Contains(r.Reason, "no worktree path") {
		t.Fatalf("the reason does not say what is missing: %s", r.Reason)
	}
}

// A worktree already gone is a fact, not a failure.
func TestAnAlreadyGoneWorktreeIsReportedNotRetried(t *testing.T) {
	c, removed := cfg(alive)
	c.Exists = func(string) bool { return false }
	r := find(t, Run([]ledger.Record{rec("gone", ledger.Complete)}, c), "gone")
	if r.Eligible || r.Removed {
		t.Fatalf("a worktree that is not there was offered for removal: %s", r.Reason)
	}
	if len(*removed) != 0 {
		t.Fatalf("Remove was called for a worktree that does not exist: %v", *removed)
	}
}

// Remove's own refusal is the last word. The sweep can only ever OFFER a
// worktree; a refusal keeps it, and the refusal's own reason is what gets
// reported -- not a summary of it, and never "swept".
func TestRemovesRefusalIsTheLastWordAndItsReasonSurvives(t *testing.T) {
	c, _ := cfg(alive)
	c.Remove = func(ledger.Record) error {
		return errors.New("isolate: /tmp/x holds uncommitted work; refusing to remove it")
	}
	r := find(t, Run([]ledger.Record{rec("held", ledger.Complete)}, c), "held")
	if r.Removed {
		t.Fatal("a worktree Remove refused was reported as removed")
	}
	if !strings.Contains(r.Reason, "holds uncommitted work") {
		t.Fatalf("Remove's own reason was lost: %s", r.Reason)
	}
}

// The bound exists so a sweep on the path to a dispatch cannot wait on the
// network without limit. What it leaves is REPORTED -- a cap nobody can see
// reads as "there was nothing left", which is exactly how a leak hides.
func TestTheBoundIsAnnouncedRatherThanSilent(t *testing.T) {
	c, removed := cfg(alive)
	c.Max = 2
	var records []ledger.Record
	for i := 0; i < 5; i++ {
		records = append(records, rec(fmt.Sprintf("turn-%d", i), ledger.Complete))
	}
	results := Run(records, c)
	if len(*removed) != 2 {
		t.Fatalf("the bound of 2 was not honoured: %v", *removed)
	}
	deferred := 0
	for _, r := range results {
		if r.Removed {
			continue
		}
		if !r.Eligible {
			t.Fatalf("%s was ruled ineligible when it was only over the bound: %s", r.Record.ID, r.Reason)
		}
		if !strings.Contains(r.Reason, "bound") {
			t.Fatalf("%s was dropped without saying the bound is why: %s", r.Record.ID, r.Reason)
		}
		deferred++
	}
	if deferred != 3 {
		t.Fatalf("expected 3 records reported as left for the next sweep, got %d", deferred)
	}
}

// Report mode removes nothing. This is what `estate sweep-worktrees` does
// without --apply, and the only thing separating it from the apply path is
// that no Remover is supplied -- so a missing seam can never mean "delete
// with no explanation".
func TestReportModeRemovesNothing(t *testing.T) {
	c, _ := cfg(alive)
	c.Remove = nil
	r := find(t, Run([]ledger.Record{rec("done", ledger.Complete)}, c), "done")
	if r.Removed {
		t.Fatal("report mode removed a worktree")
	}
	if !r.Eligible || !strings.Contains(r.Reason, "would remove") {
		t.Fatalf("report mode did not say what it would have done: %s", r.Reason)
	}
}

// Every record produces exactly one result, swept or not. A sweep that
// silently omits what it decided against is an instrument that cannot see
// the thing it is supposed to report.
func TestEveryRecordIsAccountedFor(t *testing.T) {
	c, _ := cfg(alive)
	records := []ledger.Record{
		rec("a", ledger.Complete),
		rec("b", ledger.Unknown),
		rec("c", ledger.Dispatched),
		rec("d", ledger.Failed),
	}
	results := Run(records, c)
	if len(results) != len(records) {
		t.Fatalf("got %d results for %d records", len(results), len(records))
	}
	for _, r := range results {
		if strings.TrimSpace(r.Reason) == "" {
			t.Fatalf("%s was decided with no reason given", r.Record.ID)
		}
	}
}
