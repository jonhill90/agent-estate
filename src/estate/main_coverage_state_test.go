package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// This file is the direct regression test for agent-estate#1204 (the
// remainder of the absence-coverage audit on agent-estate#1170 that
// agent-estate#1202/#1203 did not close): Coverage.State has seven values,
// but before this file only "mixed" (main_knowledge_flags_test.go) and
// "unknown" (main_knowledge_flags_test.go / main_staleness_coverage_test.go)
// were asserted end-to-end AT TOP-LEVEL-STATE GRANULARITY. The other five
// were unit-level only (internal/knowledge/query_test.go), and
// "binary_mismatch" was not asserted as a STATE anywhere at all --
// main_generated_by_test.go's two positive-case tests only assert
// Coverage.State != "complete", which a mutation collapsing every non-
// complete state into the same wrong bucket would not catch, and only
// check for a "binary_mismatch" Reason entry, never that Coverage.State
// itself reads exactly "binary_mismatch".
//
// coverageStateJSON is the same narrow-decode shape every sibling
// Coverage-testing file in this package already uses (staleCoverageJSON,
// generatedByCoverageJSON) -- just enough of QueryResult to assert on
// Coverage.State/Reasons without importing the CoverageState string
// constants themselves, so a rename of the JSON tag (not just the Go
// identifier) is caught too.
type coverageStateJSON struct {
	Coverage struct {
		State   string `json:"state"`
		Reasons []struct {
			State  string `json:"state"`
			Source string `json:"source"`
			Detail string `json:"detail"`
		} `json:"reasons"`
	} `json:"coverage"`
}

// runKnowledgeQueryJSONFor runs `estate knowledge query --json <question>`
// against idx with every source env var pinned to a scratch fixture (never
// the operator's own vault/corpus, never this repo's own git history) and
// decodes just the Coverage shape above.
func runKnowledgeQueryJSONFor(t *testing.T, bin, idx, repoRoot, question string) coverageStateJSON {
	t.Helper()
	cmd := exec.Command(bin, "knowledge", "query", "--json", question)
	cmd.Env = append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(t.TempDir(), "ledger.jsonl"),
		"AGENT_MEMORY_VAULT="+t.TempDir(), // empty -- no facts to be stale or fresh
		"ESTATE_CORPUS="+filepath.Join(t.TempDir(), "absent.sqlite3"), // deliberately missing
		"ESTATE_REPO_ROOT="+repoRoot,
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			t.Fatalf("run estate knowledge query --json: %v\n%s", runErr, out)
		}
	}
	var got coverageStateJSON
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal QueryResult JSON: %v\n%s", err, out)
	}
	return got
}

// Why every fixture below uses a ZERO GeneratedAt, proved live (not
// assumed) while preparing this file:
//
// main.go's `knowledge query` path always folds TWO extra dimensions into
// Coverage after knowledge.Query itself returns -- foldFreshnessIntoCoverage
// (staleness) and foldGeneratedByIntoCoverage (binary mismatch), in that
// order. foldFreshnessIntoCoverage's own comparison (freshnessFindings)
// always includes "github-stars", which has no local cache file to stat at
// all (indexSourceMtimes) and therefore ALWAYS reports
// CoverageUnknownFreshness for it -- unconditionally, on every query, in
// production exactly as in a test. Coverage.withLimitedReason /
// WithFreshnessReason's own compose-to-mixed rule means ANY other
// non-complete finding (a failed source, a withheld-private match, a
// genuine binary mismatch) that is folded in ahead of or behind that
// always-present github-stars finding becomes CoverageMixed, never a pure
// single-cause state -- confirmed live: a hand-built fixture with a
// GeneratedBy.Commit mismatch and a real (non-zero) GeneratedAt reports
// Coverage.State == "mixed" (reasons: one "binary_mismatch", one "unknown"
// for github-stars), never "binary_mismatch" alone. This is exactly why
// main_generated_by_test.go's own existing tests only assert != "complete"
// -- asserting == "binary_mismatch" there would fail today for a reason
// unrelated to the regression they exist to catch.
//
// foldFreshnessIntoCoverage's own guard -- `if generatedAt.IsZero() {
// return cov }` -- is the ONE documented, already-shipped code path that
// skips this comparison (and therefore github-stars' permanent
// contribution) entirely: an index whose GeneratedAt was never
// successfully read. A real `estate knowledge` index never has a zero
// GeneratedAt (Generate always stamps time.Now()), but Query place no
// validation on it either, so this is real, reachable production logic --
// not a test-only shortcut -- and it is the ONLY way any of
// complete/degraded/limited/binary_mismatch can appear as Coverage's PURE
// top-level State via this CLI path at all, in production or in a test.
// Confirmed live for all four cases below before this file was written;
// "stale" could not be produced as a pure top-level state by any fixture
// tried (see TestKnowledgeQueryCoverageStaleNeverPureTopLevel below) --
// left uncovered as a distinct top-level assertion for that reason, not
// contorted into passing.
func writeCoverageFixture(t *testing.T, idx string, res knowledge.Result) {
	t.Helper()
	if err := knowledge.Write(idx, res); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}
}

// unrelatedItem is a filler item every fixture below carries so the index
// is never literally empty (an empty compiled index is its own build-defect
// signal in query.go, unrelated to Coverage, and would just be noise in
// these tests' own JSON output) -- it never matches "zzz_no_match_zzz".
func unrelatedItem() knowledge.Item {
	return knowledge.Item{
		ID: "it-0000000000001204", Source: "vault-fact", Permalink: "/tmp/unrelated.md",
		Tier1: "an item unrelated to any question this file asks", Publishable: true,
		PublishBasis: "vault fact, always public",
	}
}

// TestKnowledgeQueryCoverageStateBinaryMismatchPure is the priority case
// this issue names: agent-estate#1204's audit found "binary_mismatch"
// asserted NOWHERE as a top-level Coverage.State value -- only its Reason
// entry (main_generated_by_test.go) was ever checked, alongside a
// != "complete" assertion loose enough to also pass for "mixed". This
// binds the exact top-level State AND the exact Detail message, so a
// mutation that reports the wrong state, or the right state with a
// reworded Detail, both fail it.
func TestKnowledgeQueryCoverageStateBinaryMismatchPure(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot, head := writeGitFixtureRepo(t)
	idx := filepath.Join(t.TempDir(), "index.json")
	staleCommit := "deadbeefcafedeadbeefcafedeadbeefcafedead"
	writeCoverageFixture(t, idx, knowledge.Result{
		// Zero GeneratedAt -- see writeCoverageFixture's own doc comment
		// above for why this is the one way to isolate this state.
		GeneratedBy: knowledge.GeneratedBy{Commit: staleCommit},
		Sources:     []knowledge.SourceResult{{Name: "github-stars", OK: true, Count: 0}},
		Items:       []knowledge.Item{unrelatedItem()},
	})

	got := runKnowledgeQueryJSONFor(t, bin, idx, repoRoot, "zzz_no_match_zzz")

	if got.Coverage.State != "binary_mismatch" {
		t.Fatalf("Coverage.State = %q, want %q\nreasons: %+v", got.Coverage.State, "binary_mismatch", got.Coverage.Reasons)
	}
	if len(got.Coverage.Reasons) != 1 {
		t.Fatalf("Coverage.Reasons = %+v, want exactly one entry", got.Coverage.Reasons)
	}
	wantDetail := fmt.Sprintf(
		"index built by %s, this checkout is at %s -- usually fine, not a refusal; regenerate with `estate knowledge` if this query needs the newer commit's own changes reflected",
		staleCommit[:12], head[:12])
	if got.Coverage.Reasons[0].State != "binary_mismatch" || got.Coverage.Reasons[0].Detail != wantDetail {
		t.Fatalf("Coverage.Reasons[0] = %+v, want {state:binary_mismatch detail:%q}", got.Coverage.Reasons[0], wantDetail)
	}
}

// TestKnowledgeQueryCoverageStateCompletePure exercises the good-path
// state: an index built by exactly the running checkout's own commit, with
// no failed source and nothing withheld -- Coverage.State must read
// "complete" with no Reasons at all.
func TestKnowledgeQueryCoverageStateCompletePure(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot, head := writeGitFixtureRepo(t)
	idx := filepath.Join(t.TempDir(), "index.json")
	writeCoverageFixture(t, idx, knowledge.Result{
		GeneratedBy: knowledge.GeneratedBy{Commit: head},
		Sources:     []knowledge.SourceResult{{Name: "github-stars", OK: true, Count: 0}},
		Items:       []knowledge.Item{unrelatedItem()},
	})

	got := runKnowledgeQueryJSONFor(t, bin, idx, repoRoot, "zzz_no_match_zzz")

	if got.Coverage.State != "complete" {
		t.Fatalf("Coverage.State = %q, want %q\nreasons: %+v", got.Coverage.State, "complete", got.Coverage.Reasons)
	}
	if len(got.Coverage.Reasons) != 0 {
		t.Fatalf("Coverage.Reasons = %+v, want none for a complete result", got.Coverage.Reasons)
	}
}

// TestKnowledgeQueryCoverageStateDegradedPure exercises a source that
// failed when the index was built (agent-estate#1058) -- Coverage.State
// must read "degraded" and name the failed source, with its own Reason
// carried through verbatim.
func TestKnowledgeQueryCoverageStateDegradedPure(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot, head := writeGitFixtureRepo(t)
	idx := filepath.Join(t.TempDir(), "index.json")
	const failReason = "synthetic fixture failure -- corpus db could not be opened"
	writeCoverageFixture(t, idx, knowledge.Result{
		GeneratedBy: knowledge.GeneratedBy{Commit: head},
		Sources: []knowledge.SourceResult{
			{Name: "github-stars", OK: true, Count: 0},
			{Name: "corpus-parameter", OK: false, Reason: failReason},
		},
		Items: []knowledge.Item{unrelatedItem()},
	})

	got := runKnowledgeQueryJSONFor(t, bin, idx, repoRoot, "zzz_no_match_zzz")

	if got.Coverage.State != "degraded" {
		t.Fatalf("Coverage.State = %q, want %q\nreasons: %+v", got.Coverage.State, "degraded", got.Coverage.Reasons)
	}
	if len(got.Coverage.Reasons) != 1 {
		t.Fatalf("Coverage.Reasons = %+v, want exactly one entry", got.Coverage.Reasons)
	}
	r := got.Coverage.Reasons[0]
	if r.State != "degraded" || r.Source != "corpus-parameter" || r.Detail != failReason {
		t.Fatalf("Coverage.Reasons[0] = %+v, want {state:degraded source:corpus-parameter detail:%q}", r, failReason)
	}
}

// TestKnowledgeQueryCoverageStateLimitedPure exercises query-time privacy
// withholding (agent-estate#1033/#1052): a question that matches only a
// private item, run without --private. Coverage.State must read "limited"
// and its Reason must carry the same Reason text QueryResult itself
// already reports at the top level.
func TestKnowledgeQueryCoverageStateLimitedPure(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot, head := writeGitFixtureRepo(t)
	idx := filepath.Join(t.TempDir(), "index.json")
	writeCoverageFixture(t, idx, knowledge.Result{
		GeneratedBy: knowledge.GeneratedBy{Commit: head},
		Sources:     []knowledge.SourceResult{{Name: "github-stars", OK: true, Count: 0}},
		Items: []knowledge.Item{
			unrelatedItem(),
			{ID: "it-0000000000001205", Source: "corpus-parameter", Permalink: "corpus:item:it-0000000000001205",
				Tier1: "zzz_uniqueprivateterm_zzz synthetic private fixture item", Publishable: false,
				PublishBasis: "synthetic test fixture, private by construction"},
		},
	})

	got := runKnowledgeQueryJSONFor(t, bin, idx, repoRoot, "zzz_uniqueprivateterm_zzz")

	if got.Coverage.State != "limited" {
		t.Fatalf("Coverage.State = %q, want %q\nreasons: %+v", got.Coverage.State, "limited", got.Coverage.Reasons)
	}
	if len(got.Coverage.Reasons) != 1 {
		t.Fatalf("Coverage.Reasons = %+v, want exactly one entry", got.Coverage.Reasons)
	}
	wantDetail := "1 item(s) matched but is private -- rerun with --private to include them"
	if got.Coverage.Reasons[0].State != "limited" || got.Coverage.Reasons[0].Detail != wantDetail {
		t.Fatalf("Coverage.Reasons[0] = %+v, want {state:limited detail:%q}", got.Coverage.Reasons[0], wantDetail)
	}
}

// TestKnowledgeQueryCoverageStaleNeverPureTopLevel is not a regression test
// for a bug -- it is the recorded, reproducible evidence behind this file's
// decision to leave "stale" uncovered as a pure top-level Coverage.State
// assertion (agent-estate#1204 explicitly allows this: "any state you
// cannot reach honestly, leave uncovered and say why"). A source
// demonstrably newer than the index always co-occurs with github-stars'
// permanent CoverageUnknownFreshness finding (see writeCoverageFixture's
// own doc comment above), which the compose-to-mixed rule always turns into
// CoverageMixed, never a bare CoverageStale. This test pins that: it FAILS
// if a future change ever makes "stale" reachable alone (at which point the
// real assertion belongs in a renamed test, not here) and otherwise
// documents, by construction, that "mixed" -- not "stale" -- is what a
// caller actually observes whenever a source goes stale via this CLI path.
func TestKnowledgeQueryCoverageStaleNeverPureTopLevel(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot, head := writeGitFixtureRepo(t)
	idx := filepath.Join(t.TempDir(), "index.json")
	genAt := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	writeCoverageFixture(t, idx, knowledge.Result{
		GeneratedAt: genAt,
		GeneratedBy: knowledge.GeneratedBy{Commit: head},
		Sources:     []knowledge.SourceResult{{Name: "github-stars", OK: true, Count: 0}},
		Items:       []knowledge.Item{unrelatedItem()},
	})

	vaultDir := t.TempDir()
	factsDir := filepath.Join(vaultDir, "agent", "facts")
	if err := os.MkdirAll(factsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(factsDir, "fixture.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Left at the CURRENT mtime -- demonstrably after genAt (a fixed point
	// in the past), so agent-memory-vault is reported stale.

	cmd := exec.Command(bin, "knowledge", "query", "--json", "zzz_no_match_zzz")
	cmd.Env = append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(t.TempDir(), "ledger.jsonl"),
		"AGENT_MEMORY_VAULT="+vaultDir,
		"ESTATE_CORPUS="+filepath.Join(t.TempDir(), "absent.sqlite3"),
		"ESTATE_REPO_ROOT="+repoRoot,
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			t.Fatalf("run estate knowledge query --json: %v\n%s", runErr, out)
		}
	}
	var got coverageStateJSON
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal QueryResult JSON: %v\n%s", err, out)
	}

	var sawStale bool
	for _, r := range got.Coverage.Reasons {
		if r.State == "stale" && r.Source == "agent-memory-vault" {
			sawStale = true
		}
	}
	if !sawStale {
		t.Fatalf("Coverage.Reasons does not name agent-memory-vault as stale -- fixture stopped producing the finding this test is built on: %+v", got.Coverage.Reasons)
	}
	// The actual claim this test exists to pin: a stale finding NEVER
	// surfaces as a pure top-level "stale" -- github-stars' own permanent
	// unknown-freshness finding always turns it into "mixed" instead.
	if got.Coverage.State != "mixed" {
		t.Fatalf("Coverage.State = %q -- expected \"mixed\" (stale + github-stars' permanent unknown), not a pure \"stale\"; "+
			"if this now reads \"stale\", the state IS reachable end-to-end and this test's own doc comment is stale, not the code",
			got.Coverage.State)
	}
}
