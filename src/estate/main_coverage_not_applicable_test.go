package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This file closes the gap agent-estate#1204's own last comment identified
// after PR #1205 merged: "not_applicable" was the one production-reachable
// Coverage.State left without end-to-end coverage. Unlike
// complete/degraded/limited/binary_mismatch (main_coverage_state_test.go),
// which the PR #1205 review found are synthetic-only in production because
// every real index carries a non-zero GeneratedAt, "not_applicable" needs
// no fixture contortion at all -- it is exactly what a fresh dispatch
// worktree reports before `estate knowledge` has ever built an index (see
// this repo's own CLAUDE.md, "Knowledge retrieval exists"), or what any
// worktree reports if the index file on disk is corrupt. Confirmed live
// against the built binary before writing this test:
//
//	$ ESTATE_KNOWLEDGE_INDEX=/tmp/missingidxtest/index.json ... \
//	    estate knowledge query --json "zzz_no_match_zzz"
//	{"state":"index_missing", "reason":"no compiled index at ... -- run
//	`estate knowledge` first", "coverage":{"state":"not_applicable"}, ...}
//	exit 2
//
// Both this test's cases bind Coverage.State AND the exact top-level
// QueryResult.Reason string -- CoverageNotApplicable's own doc comment
// (query.go) says Reasons is deliberately left empty for this state
// ("state/reason on the surrounding QueryResult already say why"), so the
// Detail a caller actually reads lives on QueryResult.Reason, not on a
// CoverageReason -- a silent rewording of either message is exactly the
// realistic regression #1203/#1205 already guard for on their own states.
type notApplicableJSON struct {
	State    string `json:"state"`
	Reason   string `json:"reason"`
	Coverage struct {
		State   string `json:"state"`
		Reasons []struct {
			State  string `json:"state"`
			Source string `json:"source"`
			Detail string `json:"detail"`
		} `json:"reasons"`
	} `json:"coverage"`
}

func runKnowledgeQueryNotApplicable(t *testing.T, bin, idx string) notApplicableJSON {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(bin, "knowledge", "query", "--json", "zzz_no_match_zzz")
	cmd.Env = append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			t.Fatalf("run estate knowledge query --json: %v\n%s", runErr, out)
		}
	}
	var got notApplicableJSON
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal QueryResult JSON: %v\n%s", err, out)
	}
	return got
}

// TestKnowledgeQueryCoverageStateNotApplicableIndexMissing is the direct
// regression test for the missing-index path: a fresh worktree (or any
// caller pointing ESTATE_KNOWLEDGE_INDEX at a path with nothing compiled
// yet) must report Coverage.State == "not_applicable" with the exact
// top-level Reason naming the missing path and the fix.
func TestKnowledgeQueryCoverageStateNotApplicableIndexMissing(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "does-not-exist.json")

	got := runKnowledgeQueryNotApplicable(t, bin, idx)

	if got.State != "index_missing" {
		t.Fatalf("state = %q, want %q", got.State, "index_missing")
	}
	wantReason := "no compiled index at " + idx + " -- run `estate knowledge` first"
	if got.Reason != wantReason {
		t.Fatalf("reason = %q, want %q", got.Reason, wantReason)
	}
	if got.Coverage.State != "not_applicable" {
		t.Fatalf("Coverage.State = %q, want %q\nreasons: %+v", got.Coverage.State, "not_applicable", got.Coverage.Reasons)
	}
	if len(got.Coverage.Reasons) != 0 {
		t.Fatalf("Coverage.Reasons = %+v, want none -- CoverageNotApplicable's own doc comment says the top-level state/reason already carry the why", got.Coverage.Reasons)
	}
}

// TestKnowledgeQueryCoverageStateNotApplicableIndexUnreadable is the
// sibling path: a corrupt (not valid JSON) index file on disk, which any
// worktree can hit from a truncated write or an interrupted regeneration
// -- Coverage.State must still read "not_applicable", with the top-level
// Reason carrying the exact wrapped-decode-error message (path + the
// underlying json error text), which is what distinguishes this case from
// index_missing in the JSON a caller actually reads.
func TestKnowledgeQueryCoverageStateNotApplicableIndexUnreadable(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(idx, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write corrupt fixture: %v", err)
	}

	got := runKnowledgeQueryNotApplicable(t, bin, idx)

	if got.State != "index_unreadable" {
		t.Fatalf("state = %q, want %q", got.State, "index_unreadable")
	}
	wantReason := idx + " is not a valid compiled index: invalid character 'o' in literal null (expecting 'u')"
	if got.Reason != wantReason {
		t.Fatalf("reason = %q, want %q", got.Reason, wantReason)
	}
	if got.Coverage.State != "not_applicable" {
		t.Fatalf("Coverage.State = %q, want %q\nreasons: %+v", got.Coverage.State, "not_applicable", got.Coverage.Reasons)
	}
	if len(got.Coverage.Reasons) != 0 {
		t.Fatalf("Coverage.Reasons = %+v, want none -- CoverageNotApplicable's own doc comment says the top-level state/reason already carry the why", got.Coverage.Reasons)
	}
}
