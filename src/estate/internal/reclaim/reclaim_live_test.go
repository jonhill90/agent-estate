package reclaim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

// TestReclaimEndToEndDistinguishesLiveFromDead is agent-estate#1194's own
// "prove it" requirement: a stranded record in a fixture that gets
// reconciled, and a live one that does not -- run through the REAL probe
// (PSProbe, a real `ps` shell-out), not the fake Probe every other test in
// this package drives Assess with. reclaim_test.go already proves the pure
// decision function is correct against contrived inputs; this proves the
// whole real pipeline -- a real dead pid, a real live one, real `ps` output
// -- agrees, on this actual host, before anything here gets wired to run on
// every dispatch's own test pass.
//
// The live fixture execs a symlink to /bin/sleep NAMED "claude", not a copy
// of it: a byte-copy of a signed system binary gets killed by macOS
// code-signing enforcement the instant it runs (measured directly while
// building this test -- a copied /bin/sleep went <defunct> within the same
// tick it started), while a symlink execs the original, still-validly-
// signed binary and survives. `ps -o comm=` reports the invoked path, which
// is enough for Assess's wantComm substring check ("claude") to treat it as
// the genuine article -- exactly the substring test production code applies
// to a real dispatched turn's own `claude` process.
func TestReclaimEndToEndDistinguishesLiveFromDead(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("ps not on PATH -- cannot drive the real probe, see PSProbe's own doc comment (this estate runs on one OS today)")
	}
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep not on PATH -- cannot build the live/dead fixtures")
	}

	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	if err := os.Symlink(sleepBin, claudePath); err != nil {
		t.Fatalf("could not symlink %s as %s: %v", sleepBin, claudePath, err)
	}

	// The live fixture: a real, currently-running process whose own comm
	// genuinely contains "claude" -- the positive case Assess must NOT
	// reclaim. Long enough to outlive the whole test; killed on cleanup.
	live := exec.Command(claudePath, "30")
	if err := live.Start(); err != nil {
		t.Fatalf("could not start the live fixture: %v", err)
	}
	livePID := live.Process.Pid
	liveAt := time.Now()
	t.Cleanup(func() {
		_ = live.Process.Kill()
		_ = live.Wait()
	})

	// The dead fixture: a real process, already exited by the time it is
	// assessed -- Wait() blocks until it genuinely has. Its own comm is
	// irrelevant: Assess decides !info.Exists before ever looking at Comm.
	dead := exec.Command(sleepBin, "0")
	if err := dead.Start(); err != nil {
		t.Fatalf("could not start the dead fixture: %v", err)
	}
	deadPID := dead.Process.Pid
	deadAt := time.Now()
	if err := dead.Wait(); err != nil {
		t.Fatalf("dead fixture did not exit cleanly: %v", err)
	}

	l, err := ledger.Open(filepath.Join(tmp, "ledger.jsonl"))
	if err != nil {
		t.Fatalf("could not open a temp ledger: %v", err)
	}
	if err := l.Append(ledger.Record{ID: "live-turn", PID: livePID, State: ledger.Dispatched, At: liveAt}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ledger.Record{ID: "dead-turn", PID: deadPID, State: ledger.Dispatched, At: deadAt}); err != nil {
		t.Fatal(err)
	}

	inflight, err := l.InFlight()
	if err != nil {
		t.Fatalf("could not read the temp ledger's in-flight records: %v", err)
	}
	if len(inflight) != 2 {
		t.Fatalf("expected both fixture records in flight, got %d: %+v", len(inflight), inflight)
	}

	// A real boot-time reading. If unavailable this run, an empty
	// time.Time makes Assess skip the reboot check entirely (its own
	// documented behaviour, the same tolerance printReclaimable applies) --
	// this test's fixtures are both recorded seconds ago regardless, so the
	// reboot path is never the one deciding either of them.
	boot, berr := BootTime()
	if berr != nil {
		t.Logf("BootTime unavailable this run (%v) -- proceeding without the reboot check, same tolerance production applies", berr)
	}

	assessments := Report(inflight, boot, PSProbe)
	byID := map[string]Assessment{}
	for _, a := range assessments {
		byID[a.Record.ID] = a
	}

	if a := byID["dead-turn"]; !a.Reclaimable {
		t.Fatalf("a real, already-exited process (pid %d) must be reclaimable through the real probe, got %+v", deadPID, a)
	}
	if a := byID["live-turn"]; a.Reclaimable {
		t.Fatalf("a real, still-running claude-named process (pid %d) must NOT be reclaimable through the real probe -- this is the trap agent-estate#1194 warns against: a live dispatch reclaimed out from under itself. got %+v", livePID, a)
	}
}

// TestLiveReclaimableRecordsInRealLedger is agent-estate#1194's actual
// wiring: it opens the SAME ledger `estate` itself reads (Open resolves
// ESTATE_LEDGER exactly as main.go's own dispatch/inflight/reclaim commands
// do), and fails, loudly and by name, if any in-flight record is currently
// reclaimable -- reusing reclaim.Report/Assess/PSProbe/BootTime verbatim,
// never a second "is this pid alive" check that could itself drift from
// what `estate reclaim` and `tick check`'s own printReclaimable already
// trust (agent-estate#1196).
//
// WHY THIS AND NOT ONLY tick check's DISCLOSURE. #1196 already wires this
// exact detection into `tick check`, which already fixed the FIRST gap --
// see PR #1196's own delivered comment on the issue, which caught a real
// stranded lane 15 minutes after merging. But `tick check` only runs
// inside the Director's own tick loop, itself a CronCreate job that is
// session-only and dies with the session (docs/canonical/director-loop.md's own
// "What is not verified here" -- the identical shape agent-estate#1248's
// watcher review is being made to answer). A reconciler-surfacer that only
// runs inside the session that might strand the record has the same defect
// one level up: if the Director's session is the thing that died, nothing
// is left to run `tick check` at all, and the phantom goes undisclosed
// until a human happens to look.
//
// This test's own caller is different in kind, not just a second copy of
// the same mechanism: `go test ./src/estate/...` is the Verify step every
// task's own brief already requires, run inside a FRESH session on every
// dispatch, never dependent on any one long-lived session surviving. What
// owns it is "the estate continues to receive dispatched work at all" --
// and if that stops being true, there is no more capacity being denied by
// a phantom anyway. So the very next dispatch to run its own tests, on any
// lane, not only the one whose process died, now fails here with a named
// cause -- exactly the pattern agent-estate#1329's live standing-law guard
// established and this issue's own brief points at as "the pattern that
// worked."
//
// This is disclosure, not repair: it never calls Apply, exactly like
// printReclaimable and `estate reclaim` with no flag. Freeing a slot stays
// a deliberate `estate reclaim --apply`, run by whoever this failure
// reaches -- the issue's own "not proposed" section is explicit that no
// auto-reclaim belongs anywhere, including here.
//
// Loud-skips, never silently passes, when the ledger cannot be resolved at
// all (e.g. no home directory in this environment) -- see agent-estate#1329's
// own doc comment for why that is the honest response to a genuinely
// missing prerequisite, distinct from #1321's skip-that-reads-as-a-pass.
// It does NOT skip merely because the ledger is empty or the file does not
// exist yet: `ledger.Open`'s own doc comment treats a missing file at a
// legitimate default path as a real, valid "nothing dispatched yet" --
// InFlight then correctly reports zero records, and this test passes on
// that real answer rather than skipping past it.
func TestLiveReclaimableRecordsInRealLedger(t *testing.T) {
	l, err := ledger.Open(os.Getenv("ESTATE_LEDGER"))
	if err != nil {
		t.Skipf("could not resolve the real ledger this environment would use (%v) -- nothing to check here, not a finding", err)
	}

	inflight, err := l.InFlight()
	if err != nil {
		t.Fatalf("could not read the real ledger's in-flight records: %v -- this is a failure to look, not evidence the ledger is clean", err)
	}
	if len(inflight) == 0 {
		t.Log("reclaimable: 0 in-flight ledger records to assess -- nothing to reconcile")
		return
	}

	boot, berr := BootTime()
	if berr != nil {
		t.Logf("BootTime unavailable this run (%v) -- proceeding without the reboot check, same tolerance printReclaimable applies", berr)
	}

	assessments := Report(inflight, boot, PSProbe)
	var reclaimable []Assessment
	for _, a := range assessments {
		if a.Reclaimable {
			reclaimable = append(reclaimable, a)
		}
	}
	if len(reclaimable) == 0 {
		t.Logf("reclaimable: none -- %d in-flight record(s), all have a live process behind them", len(inflight))
		return
	}

	msg := strconv.Itoa(len(reclaimable)) + " ledger record(s) are non-terminal with no live process behind them:\n"
	for _, a := range reclaimable {
		msg += "  " + a.Record.ID + "  " + a.Reason + "  (dispatched " + a.Record.At.UTC().Format(time.RFC3339) + ")\n"
	}
	msg += "run `estate reclaim --apply` to free these slots -- this test only reports, it never calls Apply itself"
	t.Fatal(msg)
}
