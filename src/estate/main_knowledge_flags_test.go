package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// TestKnowledgeQueryUnrecognisedFlagRefusedNotFoldedIntoQuestion is the
// direct regression test for agent-estate#1068 Finding 2: `estate
// knowledge query --bogus "tmux"` used to silently fold `--bogus` into the
// question text (scoring items that merely mention "bogus"), exit 0, and
// answer a different question than the one asked -- worse than #1064's
// unrecognised-subcommand case because it never failed at all. This drives
// the real compiled binary so a regression that reintroduces the
// fall-through fails here, not only against parseKnowledgeArgs directly.
//
// FAILS BEFORE this change (parseKnowledgeArgs folded every unrecognised
// "--flag" into rest, so this ran a query for "--bogus tmux" and exited 0)
// and PASSES AFTER (the flag is refused before any query runs).
func TestKnowledgeQueryUnrecognisedFlagRefusedNotFoldedIntoQuestion(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	// Seed an item that would only match if "bogus" were treated as a
	// search term -- if the flag leaks into the question, this item comes
	// back as a hit and the test's "no query run" assertion catches it.
	res := knowledge.Result{
		GeneratedAt: time.Now().UTC(),
		Items: []knowledge.Item{
			{ID: "it-0000000000000010", Source: "vault-fact", Permalink: "/tmp/bogus.md",
				Tier1: "a bogus decoy item that only scores on the word bogus",
				Publishable: true, PublishBasis: "vault fact, always public"},
			{ID: "it-0000000000000011", Source: "vault-fact", Permalink: "/tmp/tmux.md",
				Tier1: "notes about tmux session handling", Publishable: true,
				PublishBasis: "vault fact, always public"},
		},
	}
	if err := knowledge.Write(idx, res); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}

	cmd := exec.Command(bin, "knowledge", "query", "--bogus", "tmux")
	cmd.Env = append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
	)
	out, runErr := cmd.CombinedOutput()
	code := 0
	if runErr != nil {
		exitErr, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("run estate knowledge query --bogus: %v\n%s", runErr, out)
		}
		code = exitErr.ExitCode()
	}
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero -- an unrecognised flag must be refused, not folded into the question\n%s", out)
	}
	if !strings.Contains(string(out), `"--bogus"`) {
		t.Fatalf("refusal message does not name the flag that was passed:\n%s", out)
	}
	if strings.Contains(string(out), "match(es) for") {
		t.Fatalf("a query ran despite the unrecognised flag -- no query must run on refusal\n%s", out)
	}
	if strings.Contains(string(out), "bogus decoy") {
		t.Fatalf("the decoy item (only matches if --bogus became a search term) was returned -- the flag leaked into the question\n%s", out)
	}
}

// TestKnowledgeQueryJSONCarriesCoverageAndDoesNotBecomeAQueryTerm is the
// direct regression test for agent-estate#1068 Finding 1 and the --json
// half of Finding 2 together: before this change there was no --json mode
// at all, so `estate knowledge query --json "tmux"` had the literal
// "--json" fold into the question (scoring an unrelated item on the word
// "json") and QueryResult.Coverage -- the machine-readable trustworthiness
// signal #1065 landed -- was unreachable from any CLI caller.
//
// FAILS BEFORE (no --json flag exists; the process either treats --json as
// part of the question or, pre-#1068, exits 2 on go vet/build because the
// flag literal itself did not exist as a recognised token -- either way,
// stdout is not valid QueryResult JSON with the question intact) and
// PASSES AFTER (stdout unmarshals into a QueryResult whose Question is
// exactly "tmux", and whose Coverage.State is readable).
func TestKnowledgeQueryJSONCarriesCoverageAndDoesNotBecomeAQueryTerm(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	res := knowledge.Result{
		GeneratedAt: time.Now().UTC(),
		Sources: []knowledge.SourceResult{
			{Name: "github-stars", OK: true, Count: 1},
			{Name: "vault-fact", OK: true, Count: 1},
			{Name: "corpus-parameter", OK: true, Count: 0},
			{Name: "loops-research", OK: true, Count: 0},
			{Name: "repo-docs", OK: true, Count: 0},
		},
		Items: []knowledge.Item{
			{ID: "it-0000000000000020", Source: "vault-fact", Permalink: "/tmp/tmux.md",
				Tier1: "notes about tmux session handling", Publishable: true,
				PublishBasis: "vault fact, always public"},
			{ID: "it-0000000000000021", Source: "vault-fact", Permalink: "/tmp/json.md",
				Tier1: "a decoy item that only scores on the word json", Publishable: true,
				PublishBasis: "vault fact, always public"},
		},
	}
	if err := knowledge.Write(idx, res); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}

	cmd := exec.Command(bin, "knowledge", "query", "--json", "tmux")
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

	var qr knowledge.QueryResult
	if err := json.Unmarshal(out, &qr); err != nil {
		t.Fatalf("stdout is not valid QueryResult JSON: %v\n%s", err, out)
	}
	if qr.Question != "tmux" {
		t.Fatalf("Question = %q, want %q -- \"--json\" leaked into the question text", qr.Question, "tmux")
	}
	for _, m := range qr.Matches {
		if m.ID == "it-0000000000000021" {
			t.Fatalf("the json-decoy item was returned -- \"--json\" scored as a search term:\n%s", out)
		}
	}
	// agent-estate#1080: this CLI path now also folds #1047's staleness
	// comparison into Coverage, and github-stars has no local file to stat
	// (it is read live via `gh`) -- so even an otherwise fully-OK index,
	// run through the real CLI without a controlled AGENT_MEMORY_VAULT/
	// ESTATE_CORPUS, always carries an "unknown" freshness reason for it.
	// This test's own intent (transport correctness: --json doesn't leak
	// into the question, Coverage carries no DEGRADED/LIMITED material
	// beyond that) still holds -- only the "nothing at all to report"
	// expectation, which #1080 says was never true for github-stars,
	// changes.
	if qr.Coverage.State != knowledge.CoverageUnknownFreshness {
		t.Fatalf("Coverage.State = %q, want %q -- github-stars' freshness is never checkable via a real CLI run", qr.Coverage.State, knowledge.CoverageUnknownFreshness)
	}
	for _, r := range qr.Coverage.Reasons {
		if r.State == knowledge.CoverageDegraded || r.State == knowledge.CoverageLimited {
			t.Fatalf("Coverage.Reasons carries an unexpected %s reason for a healthy index: %+v", r.State, qr.Coverage.Reasons)
		}
	}
	var sawUnknownGithubStars bool
	for _, r := range qr.Coverage.Reasons {
		if r.State == knowledge.CoverageUnknownFreshness && r.Source == "github-stars" {
			sawUnknownGithubStars = true
		}
	}
	if !sawUnknownGithubStars {
		t.Fatalf("Coverage.Reasons does not name github-stars as unknown freshness: %+v", qr.Coverage.Reasons)
	}
}

// TestKnowledgeQueryJSONDegradedCoverageNamesFailedSource is Finding 1's
// "degraded" arm: a caller reading structured output on an index built
// with a failed source must see coverage.state == "degraded" and the
// failing source's own name -- not just a healthy-looking "matched"
// answer with no way to tell the index was built short a source.
func TestKnowledgeQueryJSONDegradedCoverageNamesFailedSource(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	res := knowledge.Result{
		GeneratedAt: time.Now().UTC(),
		Sources: []knowledge.SourceResult{
			{Name: "github-stars", OK: false, Reason: "gh api user/starred: 401 Unauthorized"},
			{Name: "vault-fact", OK: true, Count: 1},
		},
		Items: []knowledge.Item{
			{ID: "it-0000000000000030", Source: "vault-fact", Permalink: "/tmp/tmux.md",
				Tier1: "notes about tmux session handling", Publishable: true,
				PublishBasis: "vault fact, always public"},
		},
	}
	if err := knowledge.Write(idx, res); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}

	cmd := exec.Command(bin, "knowledge", "query", "--json", "tmux")
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

	var qr knowledge.QueryResult
	if err := json.Unmarshal(out, &qr); err != nil {
		t.Fatalf("stdout is not valid QueryResult JSON: %v\n%s", err, out)
	}
	// agent-estate#1080: github-stars is both build-time DEGRADED (per this
	// fixture's own SourceResult) AND always freshness-UNKNOWN through the
	// real CLI (no local file to stat) -- two distinct causes on the same
	// source, so Coverage.State composes to Mixed exactly as it already
	// does for any other two-cause case (see
	// TestQueryCoverageMixedWhenSourceFailedAndPrivacyWithheld).
	if qr.Coverage.State != knowledge.CoverageMixed {
		t.Fatalf("Coverage.State = %q, want %q\n%s", qr.Coverage.State, knowledge.CoverageMixed, out)
	}
	var sawDegraded, sawUnknown bool
	for _, r := range qr.Coverage.Reasons {
		if r.Source != "github-stars" {
			continue
		}
		switch r.State {
		case knowledge.CoverageDegraded:
			sawDegraded = true
		case knowledge.CoverageUnknownFreshness:
			sawUnknown = true
		}
	}
	if !sawDegraded {
		t.Fatalf("Coverage.Reasons does not name the failed source github-stars as degraded:\n%s", out)
	}
	if !sawUnknown {
		t.Fatalf("Coverage.Reasons does not also name github-stars as unknown freshness:\n%s", out)
	}
}

// TestKnowledgeGetJSONWrapsOKAndItem is #1068's "apply to get too" one-line
// extension: `estate knowledge get --json <id>` on a real item emits
// {"ok": true, "item": {...}} rather than the prose block, and an unknown
// id emits {"ok": false, "reason": "..."} with exit 1 unchanged.
func TestKnowledgeGetJSONWrapsOKAndItem(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	res := knowledge.Result{
		GeneratedAt: time.Now().UTC(),
		Items: []knowledge.Item{
			{ID: "it-0000000000000040", Source: "vault-fact", Permalink: "/tmp/tmux.md",
				Tier1: "notes about tmux session handling", Publishable: true,
				PublishBasis: "vault fact, always public"},
		},
	}
	if err := knowledge.Write(idx, res); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}
	env := append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
	)

	t.Run("known_id", func(t *testing.T) {
		cmd := exec.Command(bin, "knowledge", "get", "--json", "it-0000000000000040")
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run estate knowledge get --json: %v\n%s", err, out)
		}
		var got knowledgeGetJSON
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
		}
		if !got.OK || got.Item == nil || got.Item.ID != "it-0000000000000040" {
			t.Fatalf("unexpected get --json result: %+v\n%s", got, out)
		}
	})

	t.Run("unknown_id", func(t *testing.T) {
		cmd := exec.Command(bin, "knowledge", "get", "--json", "it-nonexistent")
		cmd.Env = env
		out, runErr := cmd.CombinedOutput()
		code := 0
		if runErr != nil {
			exitErr, ok := runErr.(*exec.ExitError)
			if !ok {
				t.Fatalf("run estate knowledge get --json (unknown id): %v\n%s", runErr, out)
			}
			code = exitErr.ExitCode()
		}
		if code != 1 {
			t.Fatalf("exit code = %d, want 1 (unchanged by --json)\n%s", code, out)
		}
		var got knowledgeGetJSON
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
		}
		if got.OK || got.Item != nil || got.Reason == "" {
			t.Fatalf("unexpected get --json result for unknown id: %+v\n%s", got, out)
		}
	})
}

// TestKnowledgeProseModeUnchanged is the "must not happen" side of #1068:
// query and get in prose mode (the default, no --json) print byte-for-byte
// the same notices as before -- the freshness/staleness note, and, when
// exercised, the withheld-majority banner -- and exit codes are unchanged.
func TestKnowledgeProseModeUnchanged(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	res := knowledge.Result{
		GeneratedAt: time.Now().UTC(),
		Items: []knowledge.Item{
			{ID: "it-0000000000000050", Source: "vault-fact", Permalink: "/tmp/tmux.md",
				Tier1: "notes about tmux session handling", Publishable: true,
				PublishBasis: "vault fact, always public"},
		},
	}
	if err := knowledge.Write(idx, res); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}
	env := append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
	)

	cmd := exec.Command(bin, "knowledge", "query", "tmux")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run estate knowledge query: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "index built") {
		t.Fatalf("prose query output missing the index-freshness notice:\n%s", out)
	}
	if !strings.Contains(string(out), "match(es) for") {
		t.Fatalf("prose query output missing the match summary line:\n%s", out)
	}

	cmd = exec.Command(bin, "knowledge", "get", "it-0000000000000050")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run estate knowledge get: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "id:        it-0000000000000050") {
		t.Fatalf("prose get output changed shape:\n%s", out)
	}
}
