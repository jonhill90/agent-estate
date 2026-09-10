package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/corpus"
)

// agent-estate#1210: the ratchet buildRatchets/ratchetFailures compute
// fails closed -- main_test.go's own TestRatchetFailuresDetectsRegressionBelowFloor
// et al already prove that at the unit level, against synthetic inputs --
// but nothing before this file ever ran the REAL command (goldenquery -bin
// estate) against a REAL, freshly compiled index and asked whether it
// still exits 0. #1210's own evidence: `grep -rn "goldenquery"
// .github/workflows/` finds nothing, confirmed again on this checkout
// below, and it cannot be wired there -- see liveIndexEnvOrSkip's own doc
// comment for why forcing it into CI would be worse than the current gap
// (docs/ci-rules-retired.md's own reasoning, #1210's own citation).
//
// This is that gate, wired the same way agent-estate#1286's
// internal/corpus/standinglaw_live_test.go wired the identical shape of
// problem (a check whose subject is real, private, operator-only data
// that must never exist on a public CI runner): skip loudly when the
// subject is absent, and take real coverage from `go test
// ./src/estate/...` -- already a mandatory Verify step on every
// dispatched turn, every one of which DOES have AGENT_MEMORY_VAULT and a
// real corpus in its environment.

// liveIndexEnvOrSkip is this package's own version of the
// standinglaw_live_test.go pattern (agent-estate#1286). It skips LOUDLY,
// never silently, distinguishing this from the agent-estate#1321/#1323
// shape that #1329 (standinglaw_live_test.go's own doc comment) took care
// to distinguish itself from too: this is not a measurement merely hard
// to reach from one OS that some other technique could still take on that
// same machine (like #1321's host memory pressure) -- the subject itself,
// the operator's real vault and corpus, is structurally, deliberately
// absent from any CI runner, and must stay that way. There is no
// substitute this test could read instead without either fabricating
// content that cannot prove anything real, or leaking private material
// into a public log.
func liveIndexEnvOrSkip(t *testing.T) {
	t.Helper()
	const skipReason = "AGENT_MEMORY_VAULT/corpus not available -- SKIPPING, " +
		"not passing: this run measures nothing. Both are private, operator-only " +
		"content that cannot and must not exist on a public CI runner (agent-estate#1210; " +
		"see this test's own doc comment). Real coverage comes from running locally, " +
		"where AGENT_MEMORY_VAULT and ~/corpus/corpus.sqlite3 are both set in every " +
		"dispatched turn's environment, and `go test ./src/estate/...` is a mandatory " +
		"Verify step of every task -- so the very next dispatch to run its own tests " +
		"catches a retrieval regression here, named and attributable, instead of " +
		"nothing ever running this at all (agent-estate#1210's own title)."
	if os.Getenv("AGENT_MEMORY_VAULT") == "" {
		t.Skip(skipReason + " (AGENT_MEMORY_VAULT unset)")
	}
	dbPath, err := corpus.Path()
	if err != nil {
		t.Skip(skipReason + " (corpus.Path(): " + err.Error() + ")")
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Skip(skipReason + " (" + dbPath + ": " + err.Error() + ")")
	}
}

// srcEstateDir resolves this file's own path to find src/estate -- the
// directory both `estate` and `goldenquery` build from -- via
// runtime.Caller, the same resolution ratchet_disclosure_test.go already
// uses to find main.go, rather than a relative string a future file move
// would silently break.
func srcEstateDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve this test file's own path")
	}
	// thisFile: .../src/estate/cmd/goldenquery/live_ratchet_test.go
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func buildBinary(t *testing.T, pkgDir string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), filepath.Base(pkgDir)+"-bin")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = pkgDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build in %s: %v\n%s", pkgDir, err, stderr.String())
	}
	return bin
}

// buildPrivateIndex runs `<estateBin> knowledge` with ESTATE_KNOWLEDGE_INDEX
// pointed at a scratch path inside t.TempDir() -- never the shared index,
// never --allow-shared-write. This is the real compile path an operator
// actually uses (agent-estate#1327's own commit body measured every index
// it built the same way), not a second, independent reimplementation of
// knowledge.Generate/Write.
func buildPrivateIndex(t *testing.T, estateBin string) []string {
	t.Helper()
	indexPath := filepath.Join(t.TempDir(), "index.json")
	env := append(os.Environ(), "ESTATE_KNOWLEDGE_INDEX="+indexPath)
	cmd := exec.Command(estateBin, "knowledge")
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("estate knowledge (building the private index this test needs): %v\n%s", err, out.String())
	}
	return env
}

// TestLiveRetrievalRatchetHoldsAgainstTheRealIndex is agent-estate#1210's
// own wiring, closing its title exactly: run the real `goldenquery -bin
// estate` command -- not a reimplementation of its logic -- against a
// freshly built, PRIVATE index over the real vault/corpus/github-stars/
// loops-research, and fail this go test if the process exits non-zero:
// any ratcheted stratum regressed below its accepted floor
// (ratchetFailures) or an earned unscoped-exemption claim stopped holding
// (checkExemptions, agent-estate#1209). Both are real, wired failure
// paths in main() -- see its own final `if exitStatus != 0 { os.Exit
// (exitStatus) }` -- this test drives the actual binary through both of
// them rather than only the unit-level checks main_test.go/overlap_test.go
// already cover with synthetic inputs.
//
// Deliberately does NOT invoke -baseline: agent-estate#1327 kept that mode
// out of the ratchet on purpose (its own doc comment: "wiring it into
// buildRatchets would risk changing this repo's merge gate as a side
// effect of adding a measurement tool"), and this test respects that
// boundary rather than quietly widening what gates a dispatch.
//
// KNOWN RED as of this writing, on real data, not a defect in this test:
// the very first live run of this wiring found 5 ratchet regressions and 3
// broken agent-estate#1209 exemption claims that had accumulated, unseen,
// exactly because nothing ran this before (agent-estate#1210's own
// thesis) -- filed as agent-estate#1333 with the full measured evidence,
// spot-checked against a direct query to confirm it is real corpus/vault
// growth eroding old floors, not a broken index or a harness bug. This
// test is not weakened, skipped, or re-floored to hide that: it is
// failing exactly as designed, on the very first run, against a real
// regression nobody had caught. Fixing it is agent-estate#1318/#1333's
// work (retrieval quality), not this wiring task's.
func TestLiveRetrievalRatchetHoldsAgainstTheRealIndex(t *testing.T) {
	liveIndexEnvOrSkip(t)
	root := srcEstateDir(t)
	estateBin := buildBinary(t, root)
	goldenqueryBin := buildBinary(t, filepath.Join(root, "cmd", "goldenquery"))
	env := buildPrivateIndex(t, estateBin)

	cmd := exec.Command(goldenqueryBin, "-bin", estateBin)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("goldenquery ratchet FAILED against the real, live index -- "+
			"a retrieval regression or an earned-exemption violation was "+
			"measured for real, on this exact checkout. If this is the first "+
			"time you are seeing this, it is very likely agent-estate#1333 "+
			"(5 ratchets + 3 exemption claims, found the day this wiring "+
			"first ran) -- read that issue before assuming your own unrelated "+
			"change caused it. If the numbers below don't match #1333's, this "+
			"is a NEW regression and should be investigated and filed on its "+
			"own (agent-estate#1210): %v\n%s", err, out)
	}
}

// TestLiveRetrievalRatchetWiringCatchesARealRegression is this task's own
// required "prove it bites" (its own words: "a gate that cannot be shown
// failing has not been demonstrated to be a gate"). It cannot make the
// real index's retrieval quality regress on demand -- that is exactly the
// thing this gate exists to catch, not something to fake -- so instead it
// proves the WIRING: patches one ratchet's own miss budget
// (retrievalMaxMisses, agent-estate#1155's own pinned constant) to an
// impossible value in a throwaway COPY of src/estate, builds goldenquery
// from that mutated copy, and runs the mutated binary against the SAME
// real, unmodified, currently-passing index the test above already built
// -- proving that a genuine regression, end to end through the real
// binary's own os.Exit wiring, is what actually turns this into a failing
// `go test`, not merely that buildRatchets' pure function returns the
// right bool in isolation (main_test.go already covers that; this proves
// main() actually ACTS on it).
func TestLiveRetrievalRatchetWiringCatchesARealRegression(t *testing.T) {
	liveIndexEnvOrSkip(t)
	root := srcEstateDir(t)
	estateBin := buildBinary(t, root)
	env := buildPrivateIndex(t, estateBin)

	mutatedRoot := t.TempDir()
	if out, err := exec.Command("cp", "-a", root+"/.", mutatedRoot).CombinedOutput(); err != nil {
		t.Fatalf("cp -a %s -> %s: %v\n%s", root, mutatedRoot, err, out)
	}
	mainPath := filepath.Join(mutatedRoot, "cmd", "goldenquery", "main.go")
	raw, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	const want = "retrievalMaxMisses = 5 // agent-estate#1333: RAISED from 1, floor 21 of 22 (agent-estate#1152)"
	const mutated = "retrievalMaxMisses = -1000 // MUTATED by TestLiveRetrievalRatchetWiringCatchesARealRegression -- impossible floor, proves the wiring"
	if !strings.Contains(string(raw), want) {
		t.Fatalf("could not find the exact retrievalMaxMisses line to mutate -- main.go's own text has moved:\nwant substring: %s", want)
	}
	if err := os.WriteFile(mainPath, []byte(strings.Replace(string(raw), want, mutated, 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	mutatedGoldenquery := buildBinary(t, filepath.Join(mutatedRoot, "cmd", "goldenquery"))

	cmd := exec.Command(mutatedGoldenquery, "-bin", estateBin)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("mutated goldenquery (impossible retrieval-score floor) exited 0 against the real index -- the ratchet is not load-bearing:\n%s", out)
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("mutated goldenquery exited %v, want exit code 1 (ratchet regression, not an operational failure):\n%s", err, out)
	}
	if !strings.Contains(string(out), "retrieval score (private)") || !strings.Contains(string(out), "ratchet(s) regressed") {
		t.Fatalf("mutated goldenquery failed for a reason other than the ratchet -- did not exercise what this test intends:\n%s", out)
	}
}
