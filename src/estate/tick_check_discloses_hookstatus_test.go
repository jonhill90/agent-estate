package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/hookstatus"
)

// TestHookStatusDirtyFilesFindsNonCurrentAndMiswiredFiles pins the
// classification `estate hook-status`'s own exit code and printHookStatus's
// tick-check disclosure both delegate to -- agent-dotfiles#356's two-gap
// shape means a file can be flagged for either reason independently, or
// both at once, and a Current+correctly-wired file must never appear.
func TestHookStatusDirtyFilesFindsNonCurrentAndMiswiredFiles(t *testing.T) {
	rep := hookstatus.Report{Files: []hookstatus.File{
		{Path: "hooks/current-and-wired.sh", State: hookstatus.Current, Wired: true, ShouldWire: true},
		{Path: "hooks/drifted.sh", State: hookstatus.Drifted, Wired: true, ShouldWire: true},
		{Path: "hooks/stale.sh", State: hookstatus.Stale, Wired: true, ShouldWire: true},
		{Path: "hooks/absent.sh", State: hookstatus.Absent, Wired: false, ShouldWire: true},
		{Path: "hooks/current-but-unwired.sh", State: hookstatus.Current, Wired: false, ShouldWire: true},
		{Path: "hooks/current-and-not-expected.sh", State: hookstatus.Current, Wired: false, ShouldWire: false},
	}}

	bad := hookStatusDirtyFiles(rep)

	var got []string
	for _, f := range bad {
		got = append(got, f.Path)
	}
	want := []string{"hooks/drifted.sh", "hooks/stale.sh", "hooks/absent.sh", "hooks/current-but-unwired.sh"}
	if len(got) != len(want) {
		t.Fatalf("hookStatusDirtyFiles() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hookStatusDirtyFiles()[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestRenderHookStatusCleanReport is the goal state: every file Current and
// correctly wired must render as an explicit "clean" line, never silence --
// the same discipline printReclaimable's own "reclaimable: none" line
// established for the sibling disclosure just above it.
func TestRenderHookStatusCleanReport(t *testing.T) {
	rep := hookstatus.Report{Files: []hookstatus.File{
		{Path: "hooks/a.sh", State: hookstatus.Current, Wired: true, ShouldWire: true},
		{Path: "hooks/b.sh", State: hookstatus.Current, Wired: false, ShouldWire: false},
	}}

	got := renderHookStatus(rep, "/checkout", nil)

	const want = "hook-status: clean -- 2 hook(s) tracked at /checkout, all current and correctly wired\n"
	if got != want {
		t.Fatalf("renderHookStatus() = %q, want %q", got, want)
	}
}

// TestRenderHookStatusReportsEachDirtyFileWithItsOwnGap covers every
// combination the real Compute output can produce: a state gap alone, a
// wiring gap alone, and both together on the same file -- #357's own
// finding that these are two independent gaps, not one.
func TestRenderHookStatusReportsEachDirtyFileWithItsOwnGap(t *testing.T) {
	rep := hookstatus.Report{Files: []hookstatus.File{
		{Path: "hooks/clean.sh", State: hookstatus.Current, Wired: true, ShouldWire: true},
		{Path: "hooks/ledger-write-guard.sh", State: hookstatus.Drifted, Wired: true, ShouldWire: true},
		{Path: "hooks/keychain-write-guard.sh", State: hookstatus.Absent, Wired: false, ShouldWire: true},
		{Path: "hooks/stale-but-wired-right.sh", State: hookstatus.Stale, Wired: true, ShouldWire: true},
	}}

	got := renderHookStatus(rep, "/checkout", nil)

	if !strings.HasPrefix(got, "hook-status: 3 of 4 hook(s) at /checkout are not current or not correctly wired:\n") {
		t.Fatalf("renderHookStatus() header wrong; got:\n%s", got)
	}
	if strings.Contains(got, "hooks/clean.sh") {
		t.Fatalf("renderHookStatus() named a Current, correctly-wired file as dirty; got:\n%s", got)
	}
	if !strings.Contains(got, "drifted") || !strings.Contains(got, "hooks/ledger-write-guard.sh") {
		t.Fatalf("renderHookStatus() did not report the drifted file; got:\n%s", got)
	}
	if !strings.Contains(got, "absent") || !strings.Contains(got, "wired=false") || !strings.Contains(got, "should_wire=true") ||
		!strings.Contains(got, "hooks/keychain-write-guard.sh") {
		t.Fatalf("renderHookStatus() did not report the absent, unwired-but-expected file with both gaps; got:\n%s", got)
	}
	if !strings.Contains(got, "stale") || !strings.Contains(got, "hooks/stale-but-wired-right.sh") {
		t.Fatalf("renderHookStatus() did not report the stale file; got:\n%s", got)
	}
	if !strings.Contains(got, "report only") || !strings.Contains(got, "tick check never deploys or repairs anything") {
		t.Fatalf("renderHookStatus() dropped the report-only trailer that makes the non-fatal contract explicit; got:\n%s", got)
	}
}

// TestRenderHookStatusHonestWhenComputeFailed mirrors
// TestTickCheckReclaimableHonestWhenLedgerUnreadable's typed-absence
// discipline for the sibling disclosure: a resolve failure (no checkout, no
// gh auth, a rate limit) must print an honest "could not be determined"
// line, never a fabricated "clean" that looks identical to a real one.
func TestRenderHookStatusHonestWhenComputeFailed(t *testing.T) {
	got := renderHookStatus(hookstatus.Report{}, "", errors.New("could not resolve /nonexistent's own origin remote: exit status 128"))

	const want = "hook-status: could not be determined -- could not resolve /nonexistent's own origin remote: exit status 128\n"
	if got != want {
		t.Fatalf("renderHookStatus() = %q, want %q", got, want)
	}
	if strings.Contains(got, "clean") {
		t.Fatalf("renderHookStatus() must never say \"clean\" when Compute itself failed; got: %q", got)
	}
}

// TestTickCheckDisclosesHookStatusHonestlyWhenCheckoutInvalid is the
// end-to-end CLI sibling: `tick check`, run for real against a scratch
// directory that is not a git checkout at all, must print the same honest
// disclosure -- and, critically, must fail at hookstatus.Compute's first
// local git call rather than attempting a live GitHub read, which is what
// keeps this test (and every other tick-check test in this package, via
// withHermeticHookCheckout) fast and network-free.
func TestTickCheckDisclosesHookStatusHonestlyWhenCheckoutInvalid(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")
	notAGitCheckout := t.TempDir()

	if err := os.WriteFile(ledgerPath, nil, 0o600); err != nil {
		t.Fatalf("write scratch ledger: %v", err)
	}
	if err := os.WriteFile(tickLog, nil, 0o600); err != nil {
		t.Fatalf("write scratch tick log: %v", err)
	}

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
		"ESTATE_HOOK_CHECKOUT="+notAGitCheckout,
	)

	out := runEstate(t, bin, repoRoot, env, "tick", "check")

	if !strings.Contains(out, "hook-status: could not be determined --") {
		t.Fatalf("tick check did not honestly disclose an uncomputable hook-status; got:\n%s", out)
	}
	if strings.Contains(out, "hook-status: clean") {
		t.Fatalf("tick check reported hook-status clean for a checkout it could not actually read; got:\n%s", out)
	}
}

// TestTickCheckHookStatusDoesNotChangeExitCode is
// TestTickCheckReclaimableDoesNotChangeExitCode's sibling for this
// disclosure: a stalled tick must still exit 1 with an uncomputable
// hook-status present, exactly as before this disclosure existed --
// tick check's exit code is the loop's stop contract, and hook-status is
// information, never a stop condition (the brief this PR closes explicitly
// asks that a stale or absent guard not fail the tick outright).
func TestTickCheckHookStatusDoesNotChangeExitCode(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")
	notAGitCheckout := t.TempDir()

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
		"ESTATE_HOOK_CHECKOUT="+notAGitCheckout,
	)

	// Three ticks, same phase item, no artifact -- the stall condition.
	for i := 0; i < 3; i++ {
		runEstate(t, bin, repoRoot, env, "tick", "record", "phase-0")
	}

	out, code := runEstateAnyExit(t, bin, repoRoot, env, "tick", "check")

	if code != 1 {
		t.Fatalf("tick check exit code = %d, want 1 (stalled) -- hook-status must not change the stop contract; got:\n%s", code, out)
	}
	if !strings.Contains(out, "hook-status: could not be determined --") {
		t.Fatalf("tick check did not disclose hook-status on a stalled tick; got:\n%s", out)
	}
	if !strings.Contains(out, "STALLED") {
		t.Fatalf("tick check did not still report STALLED with a hook-status disclosure present; got:\n%s", out)
	}
}
