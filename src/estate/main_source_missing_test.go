package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestKnowledgeQueryJSONCoverageNamesMissingSource is agent-estate#1139
// defect C's own regression: a source that was read successfully when the
// index was built (its SourceStatuses entry is OK) but has since become
// unreachable at query time -- the brief's own example, $AGENT_MEMORY_VAULT
// pointing nowhere -- must fold into Coverage as its own
// CoverageSourceMissing reason, distinct from CoverageUnknownFreshness
// (github-stars' standing "no local file to stat" case).
//
// FAILS BEFORE this change: the vault directory's stat failure folded into
// the exact same CoverageUnknownFreshness bucket github-stars always uses,
// so Coverage.Reasons carried two state=="unknown" entries indistinguishable
// except by reading Detail's free text, and this assertion (a reason
// specifically state=="source_missing") never appeared at all.
// PASSES AFTER: the vault's reason is state=="source_missing", github-stars'
// stays state=="unknown", and the two are never conflated.
func TestKnowledgeQueryJSONCoverageNamesMissingSource(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	generatedAt := time.Now().UTC().Add(-1 * time.Hour)
	writeFixtureIndexAt(t, idx, generatedAt)

	// A path that was never created at all -- the same shape a vanished
	// $AGENT_MEMORY_VAULT takes (agent-estate#1139's own worked example),
	// not merely an env var left unset (writeCorpusFixture below covers a
	// real, present source so this test isolates the vault as the one
	// missing source).
	vaultDir := filepath.Join(t.TempDir(), "does-not-exist")
	corpusPath := writeCorpusFixture(t, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))

	got, raw := runKnowledgeQueryJSON(t, bin, idx, vaultDir, corpusPath, "zzz_no_match_question_zzz")

	var sawMissingVault, sawUnknownGithubStars bool
	for _, r := range got.Coverage.Reasons {
		if r.Source == "agent-memory-vault" {
			if r.State != "source_missing" {
				t.Fatalf("agent-memory-vault reason state = %q, want %q: %+v\nraw: %s", r.State, "source_missing", got.Coverage.Reasons, raw)
			}
			sawMissingVault = true
		}
		if r.Source == "github-stars" {
			if r.State != "unknown" {
				t.Fatalf("github-stars reason state = %q, want %q -- must stay the standing case, not be conflated with a genuinely missing source: %+v\nraw: %s", r.State, "unknown", got.Coverage.Reasons, raw)
			}
			sawUnknownGithubStars = true
		}
	}
	if !sawMissingVault {
		t.Fatalf("Coverage.Reasons does not name agent-memory-vault as source_missing: %+v\nraw: %s", got.Coverage.Reasons, raw)
	}
	if !sawUnknownGithubStars {
		t.Fatalf("Coverage.Reasons does not mention github-stars at all: %+v\nraw: %s", got.Coverage.Reasons, raw)
	}
	if got.Coverage.State == "complete" || got.Coverage.State == "unknown" {
		t.Fatalf("Coverage.State = %q -- a positively missing source must render as its own, louder state, never complete and never the same word as the standing unknown-freshness case", got.Coverage.State)
	}
}

// TestKnowledgeQueryJSONCoverageHealthyVaultHasNoMissingReason is the
// negative case a guard that always warns would fail: a real, present,
// freshly-written vault must never contribute a source_missing reason.
// A check that fires on both healthy and broken sources is exactly as
// useless as one that never fires (dispatch-brief's own "two-directional
// mutation" requirement) -- this is that other direction.
func TestKnowledgeQueryJSONCoverageHealthyVaultHasNoMissingReason(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	generatedAt := time.Now().UTC().Add(1 * time.Hour)
	writeFixtureIndexAt(t, idx, generatedAt)

	vaultDir := writeVaultFixture(t, time.Now())
	corpusPath := writeCorpusFixture(t, time.Now())

	got, raw := runKnowledgeQueryJSON(t, bin, idx, vaultDir, corpusPath, "zzz_no_match_question_zzz")

	for _, r := range got.Coverage.Reasons {
		if r.State == "source_missing" {
			t.Fatalf("Coverage.Reasons carries a source_missing reason for a real, present, freshly-written vault: %+v\nraw: %s", got.Coverage.Reasons, raw)
		}
	}
}

// TestKnowledgeQueryProseNamesSourceGoneLoudly is the prose-mode sibling of
// TestKnowledgeQueryJSONCoverageNamesMissingSource -- the human-readable
// surface must be visibly louder than the existing "note: staleness ...
// could not be checked" line, not the same wording with a different noun.
func TestKnowledgeQueryProseNamesSourceGoneLoudly(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	generatedAt := time.Now().UTC().Add(-1 * time.Hour)
	writeFixtureIndexAt(t, idx, generatedAt)

	vaultDir := filepath.Join(t.TempDir(), "does-not-exist")
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
	if !strings.Contains(text, "*** SOURCE GONE") {
		t.Fatalf("prose output missing the loud SOURCE GONE banner:\n%s", text)
	}
	if !strings.Contains(text, "agent-memory-vault") {
		t.Fatalf("prose output's SOURCE GONE banner does not name agent-memory-vault:\n%s", text)
	}
}

// TestKnowledgeQueryJSONCoverageIgnoresSourcesTheIndexNeverRead is the CI
// regression behind the scope parameter freshnessFindings now takes: the
// fixture index names only vault-fact and github-stars, so a machine
// without loops-research (every CI runner) must NOT get a source_missing
// finding for it -- "the compiled index depends on it" was a false claim
// there. FAILS BEFORE the scoping (CI reproduced source_missing for
// loops-research and Coverage.State escalating to "mixed") and PASSES
// AFTER. On a machine where loops-research exists the pre-fix code passes
// too -- CI is the environment this test exists for.
func TestKnowledgeQueryJSONCoverageIgnoresSourcesTheIndexNeverRead(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	generatedAt := time.Now().UTC().Add(1 * time.Hour)
	writeFixtureIndexAt(t, idx, generatedAt)

	vaultDir := writeVaultFixture(t, time.Now())
	corpusPath := writeCorpusFixture(t, time.Now())

	got, raw := runKnowledgeQueryJSON(t, bin, idx, vaultDir, corpusPath, "zzz_no_match_question_zzz")

	for _, r := range got.Coverage.Reasons {
		if r.Source == "loops-research" || r.Source == "corpus-db" {
			t.Fatalf("Coverage.Reasons names %q, a source the loaded index never read (fixture names only vault-fact and github-stars): %+v\nraw: %s", r.Source, got.Coverage.Reasons, raw)
		}
	}
}
