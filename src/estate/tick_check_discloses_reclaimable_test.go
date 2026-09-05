package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTickCheckDisclosesReclaimableRecord reproduces agent-estate#1194:
// `estate reclaim` correctly detects a ledger record left `dispatched` with
// no live process behind it, and reports before repairing -- but nothing
// ever calls it, so `inflight` and `pressure` keep counting a phantom slot
// against host capacity until a human happens to run `estate reclaim` by
// hand. `tick check` is the surface every tick already runs, so it must
// name a stranded record itself, every run, without being told to.
func TestTickCheckDisclosesReclaimableRecord(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	// A dispatched record naming a pid nothing on this host is using --
	// reclaim.PSProbe (the real `ps` probe, the same one `estate reclaim`
	// itself uses) will report it not running.
	const strandedID = "1193-stranded-lane"
	const deadPID = 999999999
	writeLedgerRecords(t, ledgerPath, ledgerRecord{ID: strandedID, State: "dispatched", PID: deadPID})

	if err := os.WriteFile(tickLog, nil, 0o600); err != nil {
		t.Fatalf("write scratch tick log: %v", err)
	}

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	out := runEstate(t, bin, repoRoot, env, "tick", "check")

	if !strings.Contains(out, "reclaimable: 1 lane has a non-terminal record with no live process") {
		t.Fatalf("tick check did not name the stranded record; got:\n%s", out)
	}
	if !strings.Contains(out, strandedID) {
		t.Fatalf("tick check's reclaimable disclosure did not name the stranded lane %q; got:\n%s", strandedID, out)
	}
}

// TestTickCheckDisclosesNoReclaimableRecordsExplicitly is the clean-ledger
// sibling: when nothing is reclaimable, `tick check` must say so plainly
// rather than printing nothing -- silence here is indistinguishable from
// the check never having run at all.
func TestTickCheckDisclosesNoReclaimableRecordsExplicitly(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	if err := os.WriteFile(ledgerPath, nil, 0o600); err != nil {
		t.Fatalf("write scratch ledger: %v", err)
	}
	if err := os.WriteFile(tickLog, nil, 0o600); err != nil {
		t.Fatalf("write scratch tick log: %v", err)
	}

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	out := runEstate(t, bin, repoRoot, env, "tick", "check")

	const want = "reclaimable: none -- no in-flight ledger record lacks a live process"
	if !strings.Contains(out, want) {
		t.Fatalf("tick check did not explicitly report a clean ledger as reclaimable-free; got:\n%s", out)
	}
}

// TestTickCheckReclaimableHonestWhenLedgerUnreadable reproduces the third
// typed state: a ledger that cannot be read must produce an honest message,
// never a fabricated "reclaimable: none" that would look identical to a
// genuinely clean ledger.
func TestTickCheckReclaimableHonestWhenLedgerUnreadable(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	// A malformed line: ledger.Open succeeds (it never reads the file), but
	// Ledger.Current/InFlight fails the moment it is actually read.
	if err := os.WriteFile(ledgerPath, []byte("not valid json\n"), 0o600); err != nil {
		t.Fatalf("write scratch ledger: %v", err)
	}
	if err := os.WriteFile(tickLog, nil, 0o600); err != nil {
		t.Fatalf("write scratch tick log: %v", err)
	}

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	out := runEstate(t, bin, repoRoot, env, "tick", "check")

	const want = "reclaimable: could not be determined -- ledger unreadable:"
	if !strings.Contains(out, want) {
		t.Fatalf("tick check did not honestly report an unreadable ledger; got:\n%s", out)
	}
	if strings.Contains(out, "reclaimable: none") {
		t.Fatalf("tick check reported a clean ledger for one it could not actually read; got:\n%s", out)
	}
}

// TestTickCheckReclaimableDoesNotChangeExitCode confirms the disclosure is
// purely additive: a stalled tick (three consecutive ticks with no
// artifact) still exits 1 with a reclaimable record on the ledger, exactly
// as it did before this record existed. tick check's exit code is the
// loop's stop contract; a reclaimable record is information, not a stall.
func TestTickCheckReclaimableDoesNotChangeExitCode(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	// Three ticks, same phase item, no artifact -- the stall condition.
	for i := 0; i < 3; i++ {
		runEstate(t, bin, repoRoot, env, "tick", "record", "phase-0")
	}

	writeLedgerRecords(t, ledgerPath, ledgerRecord{ID: "1193-stranded-lane", State: "dispatched", PID: 999999999})

	out, code := runEstateAnyExit(t, bin, repoRoot, env, "tick", "check")

	if code != 1 {
		t.Fatalf("tick check exit code = %d, want 1 (stalled) -- a reclaimable record must not change the stop contract; got:\n%s", code, out)
	}
	if !strings.Contains(out, "reclaimable: 1 lane has a non-terminal record with no live process") {
		t.Fatalf("tick check did not disclose the reclaimable record on a stalled tick; got:\n%s", out)
	}
	if !strings.Contains(out, "STALLED") {
		t.Fatalf("tick check did not still report STALLED with a reclaimable record present; got:\n%s", out)
	}
}

// runEstateAnyExit is runEstate's sibling for a command whose exit code
// matters to the assertion (runEstate itself fails the test on any nonzero
// exit, which is wrong for `tick check` against a deliberately stalled log).
// It combines stdout and stderr, matching how a human reads the command's
// own terminal output -- the STALLED line goes to stderr, the reclaimable
// disclosure to stdout.
func runEstateAnyExit(t *testing.T, bin, dir string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		return out.String(), 0
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("estate %s did not run: %v\noutput:\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String(), exitErr.ExitCode()
}
