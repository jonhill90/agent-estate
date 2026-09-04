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

// staleCoverageJSON is the minimal shape this file reads back out of
// `estate knowledge query --json` -- just enough of QueryResult's own
// Coverage to assert on reasons by state/source without importing the
// exact CoverageState string constants (which did not all exist before
// this change), so these tests compile and fail meaningfully against the
// parent commit rather than merely failing to build.
type staleCoverageJSON struct {
	IndexGeneratedAt time.Time `json:"index_generated_at"`
	Coverage         struct {
		State   string `json:"state"`
		Reasons []struct {
			State  string `json:"state"`
			Source string `json:"source"`
			Detail string `json:"detail"`
		} `json:"reasons"`
	} `json:"coverage"`
}

// writeVaultFixture creates <dir>/agent/facts/<name> with the given mtime
// and returns the vault root (<dir>) -- a scratch fixture under the
// caller's own t.TempDir(), never the operator's real
// $AGENT_MEMORY_VAULT. Per this issue's own warning, nothing here ever
// stats or touches a real source: the file is one this test creates from
// nothing.
func writeVaultFixture(t *testing.T, mtime time.Time) string {
	t.Helper()
	root := t.TempDir()
	factsDir := filepath.Join(root, "agent", "facts")
	if err := os.MkdirAll(factsDir, 0o755); err != nil {
		t.Fatalf("mkdir vault fixture: %v", err)
	}
	fact := filepath.Join(factsDir, "fixture.md")
	if err := os.WriteFile(fact, []byte("# fixture\n"), 0o644); err != nil {
		t.Fatalf("write vault fixture fact: %v", err)
	}
	if err := os.Chtimes(fact, mtime, mtime); err != nil {
		t.Fatalf("chtimes vault fixture fact: %v", err)
	}
	return root
}

// writeCorpusFixture creates a scratch file standing in for
// ~/corpus/ledger.sqlite3 (ESTATE_CORPUS) with the given mtime -- a
// zero-byte placeholder is enough, since freshness comparison only ever
// stats it, never reads its content.
func writeCorpusFixture(t *testing.T, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.sqlite3")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write corpus fixture: %v", err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes corpus fixture: %v", err)
	}
	return path
}

// runKnowledgeQueryJSON runs `estate knowledge query --json <question>`
// against idx with vaultDir/corpusPath overriding AGENT_MEMORY_VAULT/
// ESTATE_CORPUS, and returns the decoded result plus the raw stdout (for
// the prose-mode sibling tests, which run without --json instead).
func runKnowledgeQueryJSON(t *testing.T, bin, idx, vaultDir, corpusPath, question string) (staleCoverageJSON, string) {
	t.Helper()
	cmd := exec.Command(bin, "knowledge", "query", "--json", question)
	cmd.Env = append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(t.TempDir(), "ledger.jsonl"),
		"AGENT_MEMORY_VAULT="+vaultDir,
		"ESTATE_CORPUS="+corpusPath,
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			t.Fatalf("run estate knowledge query --json: %v\n%s", runErr, out)
		}
		// A non-zero exit is expected for no_match etc.; only a non-exec
		// error (binary missing, etc.) is fatal here.
	}
	var got staleCoverageJSON
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal QueryResult JSON: %v\n%s", err, out)
	}
	return got, string(out)
}

// writeFixtureIndexAt writes a minimal compiled index at idx with the
// given GeneratedAt and no matching content -- these tests exercise
// Coverage, not ranking, so the question deliberately matches nothing.
func writeFixtureIndexAt(t *testing.T, idx string, generatedAt time.Time) {
	t.Helper()
	res := knowledge.Result{
		GeneratedAt: generatedAt,
		Sources: []knowledge.SourceResult{
			{Name: "github-stars", OK: true, Count: 1},
			{Name: "vault-fact", OK: true, Count: 1},
		},
		Items: []knowledge.Item{
			{ID: "it-0000000000001080", Source: "vault-fact", Permalink: "/tmp/unrelated.md",
				Tier1: "an item unrelated to any question this file asks", Publishable: true,
				PublishBasis: "vault fact, always public"},
		},
	}
	if err := knowledge.Write(idx, res); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}
}

// TestKnowledgeQueryJSONCoverageNamesStaleSource is the direct regression
// test for agent-estate#1080: a source demonstrably newer than the
// compiled index must fold into Coverage as a "stale" reason naming it,
// not just into the prose note printIndexFreshness already printed.
//
// FAILS BEFORE this change (Coverage never carried a freshness-derived
// reason at all -- the CoverageStale arm was declared but nothing set it)
// and PASSES AFTER (Coverage.Reasons contains state=="stale",
// source=="agent-memory-vault").
func TestKnowledgeQueryJSONCoverageNamesStaleSource(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	generatedAt := time.Now().UTC().Add(-1 * time.Hour)
	writeFixtureIndexAt(t, idx, generatedAt)

	// The vault fixture's fact file is written with the CURRENT time --
	// demonstrably after generatedAt (an hour in the past) -- so
	// agent-memory-vault must be reported stale. The corpus fixture is
	// deliberately backdated well before generatedAt so it contributes no
	// noise of its own.
	vaultDir := writeVaultFixture(t, time.Now())
	corpusPath := writeCorpusFixture(t, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))

	got, raw := runKnowledgeQueryJSON(t, bin, idx, vaultDir, corpusPath, "zzz_no_match_question_zzz")

	var named bool
	for _, r := range got.Coverage.Reasons {
		if r.State == "stale" && r.Source == "agent-memory-vault" {
			named = true
			if r.Detail == "" {
				t.Error("stale reason for agent-memory-vault has an empty detail")
			}
		}
	}
	if !named {
		t.Fatalf("Coverage.Reasons does not name agent-memory-vault as stale: %+v\nraw: %s", got.Coverage.Reasons, raw)
	}
	if got.Coverage.State == "complete" {
		t.Fatalf("Coverage.State = %q, want something other than complete when a source is stale", got.Coverage.State)
	}
}

// TestKnowledgeQueryJSONCoverageFreshHasNoStaleReason is the negative
// case: every source's mtime is safely before the index's own
// GeneratedAt, so no CoverageReason may claim state=="stale" for any of
// them. github-stars is exempt from this claim (it is always
// CoverageUnknownFreshness -- see the next test) so this only asserts the
// absence of "stale", not the absence of every reason.
func TestKnowledgeQueryJSONCoverageFreshHasNoStaleReason(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	// GeneratedAt an hour in the FUTURE relative to real wall-clock time
	// guarantees nothing created "now" during this test (or any real,
	// unrelated file this process cannot control, e.g. loops-research)
	// can be observed as newer than it.
	generatedAt := time.Now().UTC().Add(1 * time.Hour)
	writeFixtureIndexAt(t, idx, generatedAt)

	vaultDir := writeVaultFixture(t, time.Now())
	corpusPath := writeCorpusFixture(t, time.Now())

	got, raw := runKnowledgeQueryJSON(t, bin, idx, vaultDir, corpusPath, "zzz_no_match_question_zzz")

	for _, r := range got.Coverage.Reasons {
		if r.State == "stale" {
			t.Fatalf("Coverage.Reasons carries a stale reason on a fresh index: %+v\nraw: %s", got.Coverage.Reasons, raw)
		}
	}
}

// TestKnowledgeQueryJSONCoverageGithubStarsUnknownNotFresh is the direct
// regression test for this issue's governing rule -- "absence of evidence
// is not evidence of freshness": github-stars has no local file to stat at
// all, so it must report CoverageUnknownFreshness, and Coverage.State must
// never read "complete" (the caller-facing shorthand for "safe to trust as
// fully fresh") on its account.
//
// FAILS BEFORE this change (github-stars contributed nothing to Coverage;
// a caller reading Coverage.State alone saw no signal that freshness was
// never checked for it) and PASSES AFTER.
func TestKnowledgeQueryJSONCoverageGithubStarsUnknownNotFresh(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	generatedAt := time.Now().UTC().Add(1 * time.Hour)
	writeFixtureIndexAt(t, idx, generatedAt)

	vaultDir := writeVaultFixture(t, time.Now())
	corpusPath := writeCorpusFixture(t, time.Now())

	got, raw := runKnowledgeQueryJSON(t, bin, idx, vaultDir, corpusPath, "zzz_no_match_question_zzz")

	var sawUnknownGithubStars bool
	for _, r := range got.Coverage.Reasons {
		if r.Source == "github-stars" {
			if r.State != "unknown" {
				t.Fatalf("github-stars reason state = %q, want %q: %+v\nraw: %s", r.State, "unknown", got.Coverage.Reasons, raw)
			}
			sawUnknownGithubStars = true
		}
	}
	if !sawUnknownGithubStars {
		t.Fatalf("Coverage.Reasons does not mention github-stars at all: %+v\nraw: %s", got.Coverage.Reasons, raw)
	}
	if got.Coverage.State == "complete" {
		t.Fatalf("Coverage.State = %q -- github-stars' unchecked freshness must never render as complete/fresh", got.Coverage.State)
	}
}

// TestKnowledgeQueryProseFreshnessNoteUnchanged pins printIndexFreshness's
// own printed text -- #1080 folds staleness into Coverage's structure, it
// must not change or duplicate what prose mode already prints (the "index
// built ... ago" line and the per-source staleness notes).
func TestKnowledgeQueryProseFreshnessNoteUnchanged(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	generatedAt := time.Now().UTC().Add(-1 * time.Hour)
	writeFixtureIndexAt(t, idx, generatedAt)

	vaultDir := writeVaultFixture(t, time.Now())
	corpusPath := writeCorpusFixture(t, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))

	cmd := exec.Command(bin, "knowledge", "query", "zzz_no_match_question_zzz")
	cmd.Env = append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(t.TempDir(), "ledger.jsonl"),
		"AGENT_MEMORY_VAULT="+vaultDir,
		"ESTATE_CORPUS="+corpusPath,
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			t.Fatalf("run estate knowledge query: %v\n%s", runErr, out)
		}
	}
	text := string(out)
	if !strings.Contains(text, "index built ") {
		t.Fatalf("prose output missing the index-age line:\n%s", text)
	}
	if !strings.Contains(text, "index is BEHIND its sources: agent-memory-vault") {
		t.Fatalf("prose output missing the BEHIND note naming agent-memory-vault:\n%s", text)
	}
	if !strings.Contains(text, "note: staleness against github-stars") {
		t.Fatalf("prose output missing the github-stars unknown note:\n%s", text)
	}
	if !strings.Contains(text, "reported as unknown, not assumed fresh") {
		t.Fatalf("prose output missing the unknown-not-fresh wording:\n%s", text)
	}
}
