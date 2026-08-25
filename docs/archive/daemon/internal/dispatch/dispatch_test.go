package dispatch

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"agent-supervisor/daemon/internal/agent"
	"agent-supervisor/daemon/internal/claim"
	"agent-supervisor/daemon/internal/ledger"
)

// schema mirrors internal/ledger/ledger_test.go's own (unexported there,
// so reproduced here rather than shared) -- tasks.lane REFERENCES
// lanes(lane), same FK the real dispatch died on before EnsureLane existed.
const schema = `
CREATE TABLE lanes (lane TEXT PRIMARY KEY, pane_id TEXT NOT NULL, nonce TEXT NOT NULL,
 harness TEXT NOT NULL CHECK (harness IN ('codex','claude','copilot','copilot-acp','pi')),
 repo TEXT NOT NULL, server_id TEXT NOT NULL, session_id TEXT NOT NULL, command TEXT NOT NULL,
 harness_session_id TEXT DEFAULT '', harness_project_dir TEXT DEFAULT '',
 transport TEXT NOT NULL DEFAULT 'send-keys' CHECK (transport IN ('send-keys','acp','pi-rpc','claude-print')),
 updated_at INTEGER NOT NULL);
CREATE TABLE tasks (id TEXT PRIMARY KEY, lane TEXT NOT NULL REFERENCES lanes(lane),
 pane_nonce TEXT NOT NULL, summary TEXT NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('created','delivery_pending','delivered','accepted','running','complete','failed','cancelled')),
 result_path TEXT, result_sha256 TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 delivery_attempted_at INTEGER, delivered_at INTEGER, accepted_at INTEGER, completed_at INTEGER,
 worktree_path TEXT NOT NULL DEFAULT '');`

func newLedger(t *testing.T) *ledger.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "l.sqlite3")
	// Create the schema through a raw connection first -- ledger.DB's own
	// field is unexported (by design, package ledger's own doc comment:
	// this package is the one place allowed to write a terminal status),
	// so a test in a different package cannot reach it to run DDL.
	raw, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	raw.Close()

	l, err := ledger.Open(path)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	t.Cleanup(func() { l.Close(); os.Remove(path) })
	return l
}

// fakeAdapter succeeds immediately with a fixed Result -- this file is
// about the claim gate wired around RunGated, not about any one vendor
// CLI's own behaviour (that is agent/*_test.go's job).
type fakeAdapter struct{ sessionID string }

func (f fakeAdapter) Run(context.Context, string) (*agent.Result, error) {
	return &agent.Result{SessionID: f.sessionID, NumTurns: 1}, nil
}

// fakeClaimStore reproduces claim.sh's own take/release verbs as a stub
// over a lock-file directory -- the SAME fixture internal/claim's own
// tests use (reproduced here rather than shared, since it is a small,
// self-contained test fixture, not exported API). raceDelay widens the
// check-then-write window deliberately, the same reason internal/claim's
// own mutation-check pair does, so the concurrent tests below are not
// passing by accident of scheduling.
func fakeClaimStore(t *testing.T, lockDir string, raceDelay time.Duration) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake binary is a POSIX shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claim-store")
	sleep := ""
	if raceDelay > 0 {
		sleep = "sleep " + raceDelay.String() + "\n"
	}
	script := `#!/bin/sh
verb="$1"; issue="$2"; repo="$3"; lane="${4:-}"
file="` + lockDir + `/issue-$issue"
case "$verb" in
  take)
    if [ -f "$file" ]; then echo "claimed by $(cat "$file")"; exit 1; fi
` + sleep + `    if [ -f "$file" ]; then echo "claimed by $(cat "$file")"; exit 1; fi
    echo "$lane" > "$file"
    exit 0 ;;
  release)
    rm -f "$file"
    exit 0 ;;
  *) echo "unknown verb $verb" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claim store: %v", err)
	}
	return path
}

func TestRunGated_ClaimRefusal_NoLedgerWrites(t *testing.T) {
	l := newLedger(t)
	lockDir := t.TempDir()
	gate := &claim.ScriptGate{ScriptPath: fakeClaimStore(t, lockDir, 0)}
	// Pre-claim the issue under a different lane, simulating the exact
	// #28 shape: someone else already holds it.
	if err := gate.Take(context.Background(), 5, "", "other-lane"); err != nil {
		t.Fatalf("setup Take: %v", err)
	}

	out := RunGated(context.Background(), l, fakeAdapter{}, "/tmp", "claude",
		"t1", "lane-1", "brief", &IssueRef{Number: 5}, Gates{Claim: gate})

	if out.Err == nil {
		t.Fatal("expected a refusal, got nil error")
	}
	if _, err := l.Task("t1"); err != ledger.ErrNotFound {
		t.Fatalf("Task(t1) = %v, want ErrNotFound -- claim refusal must precede any ledger write", err)
	}
}

func TestRunGated_ClaimSucceeds_ReleasesOnSuccess(t *testing.T) {
	l := newLedger(t)
	lockDir := t.TempDir()
	gate := &claim.ScriptGate{ScriptPath: fakeClaimStore(t, lockDir, 0)}

	out := RunGated(context.Background(), l, fakeAdapter{sessionID: "s1"}, "/tmp", "claude",
		"t1", "lane-1", "brief", &IssueRef{Number: 9}, Gates{Claim: gate})
	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if !out.OK {
		t.Fatal("expected OK=true")
	}
	// The claim must have been released by RunGated's own terminal path --
	// a fresh Take for the same issue must now succeed.
	if err := gate.Take(context.Background(), 9, "", "someone-else"); err != nil {
		t.Fatalf("Take after a successful dispatch should succeed (claim released): %v", err)
	}
}

// MUTATION-CHECK, direction (a) — the guard fires at the DISPATCH level,
// not just inside internal/claim: two goroutines call RunGated
// near-simultaneously against the SAME issue (distinct lanes/task ids, so
// the ledger's own UNIQUE(tasks.lane) constraint is not what decides
// this). Exactly one must actually run the agent and reach a terminal
// stamp; the other must be refused before any ledger write.
func TestRunGated_ConcurrentDispatch_SameIssue_ExactlyOneWins(t *testing.T) {
	l := newLedger(t)
	lockDir := t.TempDir()
	gate := &claim.ScriptGate{ScriptPath: fakeClaimStore(t, lockDir, 50*time.Millisecond)}

	var wg sync.WaitGroup
	outs := make([]Outcome, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			taskID := "t" + string(rune('1'+i))
			lane := "lane-" + string(rune('1'+i))
			outs[i] = RunGated(context.Background(), l, fakeAdapter{sessionID: taskID}, "/tmp", "claude",
				taskID, lane, "brief", &IssueRef{Number: 42}, Gates{Claim: gate})
		}(i)
	}
	wg.Wait()

	wins, refusals := 0, 0
	for _, o := range outs {
		if o.Err == nil && o.OK {
			wins++
		} else if o.Err != nil {
			refusals++
		}
	}
	if wins != 1 || refusals != 1 {
		t.Fatalf("wins=%d refusals=%d (want exactly 1 each) -- outs=%+v", wins, refusals, outs)
	}
}

// MUTATION-CHECK, direction (b) — the guard does not pass by accident:
// same near-simultaneous dispatch, same fake claim store, but Gates{}
// carries no Claim gate at all (the "before this daemon's fix" shape --
// exactly what daemon/internal/ledger looked like when this task started:
// `grep -rn "claim" daemon/` returned zero hits). Both dispatches must now
// proceed, reproducing the #28 incident this whole package exists to
// close.
func TestRunGated_ConcurrentDispatch_SameIssue_WithoutClaimGate_BothProceed(t *testing.T) {
	l := newLedger(t)

	var wg sync.WaitGroup
	outs := make([]Outcome, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			taskID := "u" + string(rune('1'+i))
			lane := "ulane-" + string(rune('1'+i))
			// No Claim gate -- Gates{} the same as before this task's fix.
			outs[i] = RunGated(context.Background(), l, fakeAdapter{sessionID: taskID}, "/tmp", "claude",
				taskID, lane, "brief", &IssueRef{Number: 42}, Gates{})
		}(i)
	}
	wg.Wait()

	wins := 0
	for _, o := range outs {
		if o.Err == nil && o.OK {
			wins++
		}
	}
	if wins != 2 {
		t.Fatalf("wins=%d, want 2 (both should proceed with no claim gate wired -- "+
			"this is the #28 shape the claim gate exists to close)", wins)
	}
}
