package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTickRecordPrintsTurnsThatReportedCostNotTotalObservedTurns reproduces
// the reviewer's own scenario on the closed PR #989: a window in which two
// turns finish, only one of which reported a dollar figure. The dollar
// line's own claim is "N observed turn(s) that reported a cost" -- N must be
// spend.WindowedByObservation's turnsWithCost (here: 1), never turns (here:
// 2). The fix pass that closed #989 wired *e.ObservedTurns (the total) into
// that claim instead, which is exactly the defect agent-estate#995 exists to
// fix.
//
// This test fails if tick.Entry.ObservedTurnsWithCost and
// tick.Entry.ObservedTurns are ever swapped again at either main.go print
// site -- confirmed by temporarily reverting main.go's fix and re-running
// (see the PR body for both outputs pasted).
func TestTickRecordPrintsTurnsThatReportedCostNotTotalObservedTurns(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	// Tick 1: the first tick this scratch log has ever recorded, so it
	// establishes `since` for tick 2's window and prints no gap/spend of its
	// own.
	runEstate(t, bin, repoRoot, env, "tick", "record", "phase-0")

	// Between tick 1 and tick 2: two tasks reach a terminal ledger state,
	// only one of which reports a dollar figure -- the mixed-harness shape
	// that makes ObservedTurns and ObservedTurnsWithCost diverge (claude
	// reports a cost, codex as of this writing never does).
	cost := 2.5
	writeLedgerRecords(t, ledgerPath,
		ledgerRecord{ID: "task-with-cost", State: "complete", SpendCostUSD: &cost},
		ledgerRecord{ID: "task-no-cost", State: "complete", SpendCostUSD: nil},
	)

	out := runEstate(t, bin, repoRoot, env, "tick", "record", "phase-0")

	const want = "observed spend this window: $2.5000 across 1 observed turn(s) that reported a cost"
	if !strings.Contains(out, want) {
		t.Fatalf("tick record stdout does not contain %q (2 turns finished, only 1 reported a cost -- the count must be 1, not 2); got:\n%s", want, out)
	}
	// Guard the failure direction explicitly: the swapped/broken form names
	// 2, not 1.
	if strings.Contains(out, "across 2 observed turn(s) that reported a cost") {
		t.Fatalf("tick record printed the TOTAL observed turn count (2) as though it were the count that reported a cost -- got:\n%s", out)
	}
}

// TestTickCheckPrintsTurnsThatReportedCostNotTotalObservedTurns is the same
// scenario read back through `tick check`'s own printer (tick.LastEntry /
// LastRecorded.ObservedTurnsWithCost), the second site the issue names.
func TestTickCheckPrintsTurnsThatReportedCostNotTotalObservedTurns(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	runEstate(t, bin, repoRoot, env, "tick", "record", "phase-0")

	cost := 2.5
	writeLedgerRecords(t, ledgerPath,
		ledgerRecord{ID: "task-with-cost", State: "complete", SpendCostUSD: &cost},
		ledgerRecord{ID: "task-no-cost", State: "complete", SpendCostUSD: nil},
	)

	runEstate(t, bin, repoRoot, env, "tick", "record", "phase-0")

	// Fewer than tick.Window (3) entries exist, so checkImpl returns before
	// resolving any artifact -- no network call is needed for this
	// assertion, only the LastEntry printer this test targets.
	out := runEstate(t, bin, repoRoot, env, "tick", "check")

	const want = "last tick's observed spend: $2.5000 across 1 observed turn(s) that reported a cost"
	if !strings.Contains(out, want) {
		t.Fatalf("tick check stdout does not contain %q; got:\n%s", want, out)
	}
	if strings.Contains(out, "across 2 observed turn(s) that reported a cost") {
		t.Fatalf("tick check printed the TOTAL observed turn count (2) as though it were the count that reported a cost -- got:\n%s", out)
	}
}

// TestTickCheckSurvivesABrokenSpendPairOnDisk is the reviewer's own
// reproduction on agent-estate#997, kept as a regression test: a tick log
// whose last line carries observed_spend_usd with no
// observed_turns_with_cost. No write path in this repo produces that, but
// `tick check` does not write entries -- it reads back whatever JSON is on
// the last line, and it used to dereference the missing pointer and panic,
// taking the loop's own stop condition down with it (main.go:719, SIGSEGV).
//
// Against the code before the fix this test fails at runEstate, which
// reports the panic and exit 2; both runs are pasted in the PR comment.
//
// It asserts three things, in order of what would be worst to lose: the
// command survives; it does not print an invented turn count beside the real
// dollar figure; and it says plainly that the entry could not be read.
func TestTickCheckSurvivesABrokenSpendPairOnDisk(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	// The reviewer's exact hand-written line: a dollar figure whose
	// denominator is absent.
	const brokenPair = `{"at":"2026-09-03T10:00:00Z","phase_item":"phase-0","src_head":"deadbeef","artifact":null,"observed_turns":2,"observed_spend_usd":2.5}`
	if err := os.WriteFile(tickLog, []byte(brokenPair+"\n"), 0o600); err != nil {
		t.Fatalf("write scratch tick log: %v", err)
	}
	if err := os.WriteFile(ledgerPath, nil, 0o600); err != nil {
		t.Fatalf("write scratch ledger: %v", err)
	}

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	// runEstate fails the test on any non-zero exit, so a panic here is a
	// failure with the crash pasted into the log.
	out := runEstate(t, bin, repoRoot, env, "tick", "check")

	// A count that was never recorded must not appear beside the figure --
	// "across 0 observed turn(s)" would be exactly the fabricated denominator
	// this field pair exists to prevent, and any other count would be worse.
	if strings.Contains(out, "observed turn(s) that reported a cost") {
		t.Fatalf("tick check printed a turn count for an entry that carries none -- got:\n%s", out)
	}
	if !strings.Contains(out, "last tick's observed spend: could not be read honestly") {
		t.Fatalf("tick check must say the entry could not be read, not stay silent about it; got:\n%s", out)
	}
	if !strings.Contains(out, "no count of the turns that reported it") {
		t.Fatalf("tick check must name which pairing broke; got:\n%s", out)
	}
}

// The other half of the same guarantee: an entry written before any of these
// fields existed carries none of them and must keep reading exactly as it
// does today -- "not recorded", no crash, and never the unreadable branch.
func TestTickCheckOldEntryWithNeitherFieldIsUnchanged(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	const oldEntry = `{"at":"2026-09-03T10:00:00Z","phase_item":"phase-0","src_head":"deadbeef","artifact":null}`
	if err := os.WriteFile(tickLog, []byte(oldEntry+"\n"), 0o600); err != nil {
		t.Fatalf("write scratch tick log: %v", err)
	}
	if err := os.WriteFile(ledgerPath, nil, 0o600); err != nil {
		t.Fatalf("write scratch ledger: %v", err)
	}

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	out := runEstate(t, bin, repoRoot, env, "tick", "check")

	const want = "last tick's observed spend: not recorded"
	if !strings.Contains(out, want) {
		t.Fatalf("an entry predating agent-estate#982 must still read as %q; got:\n%s", want, out)
	}
	if strings.Contains(out, "could not be read honestly") {
		t.Fatalf("an old entry is not a broken one -- it must not take the unreadable branch; got:\n%s", out)
	}
}

// ledgerRecord is the minimal shape internal/ledger.Record needs for
// Ledger.Current() to read back a single terminal record per task id.
type ledgerRecord struct {
	ID           string   `json:"id"`
	State        string   `json:"state"`
	SpendCostUSD *float64 `json:"spend_cost_usd,omitempty"`
}

// writeLedgerRecords appends one JSON line per record, each timestamped
// `now` so it lands inside the window between the previous tick (already
// recorded) and the next one (not yet run).
func writeLedgerRecords(t *testing.T, path string, records ...ledgerRecord) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open ledger %s: %v", path, err)
	}
	defer f.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, r := range records {
		line := struct {
			ledgerRecord
			At string `json:"at"`
		}{ledgerRecord: r, At: now}
		b, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal ledger record: %v", err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatalf("write ledger record: %v", err)
		}
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func buildEstateBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "estate")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build estate: %v\n%s", err, stderr.String())
	}
	return bin
}

func runEstate(t *testing.T, bin, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("estate %s: %v\nstdout:\n%sstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}
