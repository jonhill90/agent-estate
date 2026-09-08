package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// staleWithheldJSON is the minimal shape these tests read back out of
// `estate knowledge query --json` -- State/Reason/Coverage/Matches only,
// the same "just enough to assert without importing every constant"
// approach staleCoverageJSON (main_staleness_coverage_test.go) already
// uses, so these tests fail meaningfully (wrong state string, not a build
// error) against the pre-#1306 binary that has no StateStaleWithheldRefused
// at all.
type staleWithheldJSON struct {
	State    string     `json:"state"`
	Reason   string     `json:"reason"`
	Matches  []struct{} `json:"matches,omitempty"`
	Coverage struct {
		State   string `json:"state"`
		Reasons []struct {
			State  string `json:"state"`
			Source string `json:"source"`
			Detail string `json:"detail"`
		} `json:"reasons"`
	} `json:"coverage"`
}

// writeWithheldFixtureIndexAt writes a compiled index at idx, generated at
// generatedAt, with publicN publishable items and privateN non-publishable
// items, ALL matching the single question term "widget" -- so a query for
// "widget" scores every one of them and StateMatchedWithheldMajority (or
// plain StateMatched) is decided purely by the publicN/privateN ratio
// Query already computes, never a second ratio invented by this fixture.
func writeWithheldFixtureIndexAt(t *testing.T, idx string, generatedAt time.Time, publicN, privateN int) {
	t.Helper()
	res := knowledge.Result{
		GeneratedAt: generatedAt,
		Sources: []knowledge.SourceResult{
			{Name: "github-stars", OK: true, Count: 1},
			{Name: knowledge.VaultSourceName, OK: true, Count: publicN + privateN},
		},
	}
	id := 1
	for i := 0; i < publicN; i++ {
		res.Items = append(res.Items, knowledge.Item{
			ID:           fmt.Sprintf("it-%016d", id),
			Source:       knowledge.VaultItemSourceTag,
			Permalink:    "/tmp/public.md",
			Tier1:        "the widget rotates every ninety days, public copy",
			Publishable:  true,
			PublishBasis: "vault fact, always public",
		})
		id++
	}
	for i := 0; i < privateN; i++ {
		res.Items = append(res.Items, knowledge.Item{
			ID:           fmt.Sprintf("it-%016d", id),
			Source:       knowledge.VaultItemSourceTag,
			Permalink:    "/tmp/private.md",
			Tier1:        "the widget rotates every ninety days, private copy",
			Publishable:  false,
			PublishBasis: "vault fact, private by default",
		})
		id++
	}
	if err := knowledge.Write(idx, res); err != nil {
		t.Fatalf("write withheld fixture index: %v", err)
	}
}

// runStaleWithheldQuery runs `estate knowledge query --json widget`
// against idx, with vaultDir/corpusPath standing in for
// AGENT_MEMORY_VAULT/ESTATE_CORPUS the same way runKnowledgeQueryJSON
// (main_staleness_coverage_test.go) does, and returns the decoded result,
// the raw stdout, and the process exit code -- item 3's own "what is the
// exit code" requirement needs the real one, not just $? folded away.
func runStaleWithheldQuery(t *testing.T, bin, idx, vaultDir, corpusPath string, asJSON bool) (staleWithheldJSON, string, int) {
	t.Helper()
	args := []string{"knowledge", "query"}
	if asJSON {
		args = append(args, "--json")
	}
	args = append(args, "widget")
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(t.TempDir(), "ledger.jsonl"),
		"AGENT_MEMORY_VAULT="+vaultDir,
		"ESTATE_CORPUS="+corpusPath,
	)
	out, runErr := cmd.CombinedOutput()
	code := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run estate knowledge query: %v\n%s", runErr, out)
		}
	}
	var got staleWithheldJSON
	if asJSON {
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal QueryResult JSON: %v\n%s", err, out)
		}
	}
	return got, string(out), code
}

// TestKnowledgeQueryFreshMajorityWithheldStillAnswers is the first of
// agent-estate#1306 item 3's four required fixtures: majority-withheld
// ALONE, against a FRESH index, must still answer (StateMatchedWithheldMajority,
// exit 0) -- #1052's own deliberate design, unchanged by this issue.
func TestKnowledgeQueryFreshMajorityWithheldStillAnswers(t *testing.T) {
	bin := buildEstateBinary(t)
	idx := filepath.Join(t.TempDir(), "index.json")
	// GeneratedAt an hour in the FUTURE, the same pattern
	// TestKnowledgeQueryJSONCoverageFreshHasNoStaleReason
	// (main_staleness_coverage_test.go) already uses: writeVaultFixture's
	// own os.MkdirAll gives "01 - Notes" a real mtime of "now" regardless
	// of what mtime is set on the fact file inside it (statVaultNotes
	// takes the newest of the directory and every note under it), so
	// "fresh" needs generatedAt to be after "now", not the fixture files
	// backdated before some earlier "now".
	generatedAt := time.Now().UTC().Add(1 * time.Hour)
	writeWithheldFixtureIndexAt(t, idx, generatedAt, 1, 3) // 3 > 1: majority withheld

	vaultDir := writeVaultFixture(t, time.Now())
	corpusPath := writeCorpusFixture(t, time.Now())

	got, raw, code := runStaleWithheldQuery(t, bin, idx, vaultDir, corpusPath, true)
	if got.State != "matched_withheld_majority" {
		t.Fatalf("State = %q, want matched_withheld_majority\nraw: %s", got.State, raw)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (majority-withheld alone still answers)", code)
	}
	if len(got.Matches) == 0 {
		t.Fatalf("Matches is empty, want the 1 public item still returned\nraw: %s", raw)
	}
}

// TestKnowledgeQueryStaleMinorityWithheldStillAnswers is the second
// required fixture: STALE alone, with only a MINORITY withheld, must
// still answer (StateMatched, exit 0) -- staleness alone is "report,
// never repair", not a refusal.
func TestKnowledgeQueryStaleMinorityWithheldStillAnswers(t *testing.T) {
	bin := buildEstateBinary(t)
	idx := filepath.Join(t.TempDir(), "index.json")
	generatedAt := time.Now().UTC().Add(-1 * time.Hour)
	writeWithheldFixtureIndexAt(t, idx, generatedAt, 3, 1) // 1 < 3: minority withheld

	// Vault mtime AFTER generatedAt: stale.
	vaultDir := writeVaultFixture(t, time.Now())
	corpusPath := writeCorpusFixture(t, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))

	got, raw, code := runStaleWithheldQuery(t, bin, idx, vaultDir, corpusPath, true)
	if got.State != "matched" {
		t.Fatalf("State = %q, want matched (stale alone, minority withheld, must still answer)\nraw: %s", got.State, raw)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if len(got.Matches) == 0 {
		t.Fatalf("Matches is empty, want the 3 public items still returned\nraw: %s", raw)
	}
	staleFound := false
	for _, r := range got.Coverage.Reasons {
		if r.State == "stale" {
			staleFound = true
		}
	}
	if !staleFound {
		t.Fatalf("Coverage.Reasons does not name the index as stale, want it to (this fixture backdated the index)\nraw: %s", raw)
	}
}

// TestKnowledgeQueryStaleMajorityWithheldRefuses is the load-bearing third
// fixture agent-estate#1306 item 3 asks for: STALE **and** MAJORITY
// WITHHELD together must REFUSE, not warn-then-answer. Confirmed FAILING
// against the pre-#1306 binary (StateStaleWithheldRefused does not exist;
// see the commit history for the run against unmodified main, which
// returned matched_withheld_majority/exit 0 with the 1 public item
// printed -- exactly the "8 items under four lines of caveat" shape the
// issue names).
func TestKnowledgeQueryStaleMajorityWithheldRefuses(t *testing.T) {
	bin := buildEstateBinary(t)
	idx := filepath.Join(t.TempDir(), "index.json")
	generatedAt := time.Now().UTC().Add(-1 * time.Hour)
	writeWithheldFixtureIndexAt(t, idx, generatedAt, 1, 3) // majority withheld

	// Vault mtime AFTER generatedAt: stale, same as the minority case above.
	vaultDir := writeVaultFixture(t, time.Now())
	corpusPath := writeCorpusFixture(t, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))

	got, raw, code := runStaleWithheldQuery(t, bin, idx, vaultDir, corpusPath, true)
	if got.State != "stale_withheld_refused" {
		t.Fatalf("State = %q, want stale_withheld_refused (stale AND majority-withheld must refuse)\nraw: %s", got.State, raw)
	}
	// "A refusal that exits 0 cannot be detected by a caller" -- item 3's
	// own requirement. 0/1/2/3 are all already claimed by other states
	// (knowledgeQueryExitCode's own doc comment); this must be a NEW,
	// distinct code.
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero -- a refusal that exits 0 cannot be detected by a caller (agent-estate#1306)")
	}
	if code != 4 {
		t.Fatalf("exit code = %d, want 4", code)
	}
	if len(got.Matches) != 0 {
		t.Fatalf("Matches = %+v, want none -- a refusal must not still hand over the withheld-context items\nraw: %s", got.Matches, raw)
	}

	// Prose mode: must not print the match list either, and must show the
	// loud refusal banner, not the ordinary "MOSTLY WITHHELD" one alone.
	_, proseRaw, proseCode := runStaleWithheldQuery(t, bin, idx, vaultDir, corpusPath, false)
	if proseCode != 4 {
		t.Fatalf("prose mode exit code = %d, want 4\n%s", proseCode, proseRaw)
	}
	if !strings.Contains(proseRaw, "*** REFUSING") {
		t.Fatalf("prose output missing a REFUSING banner:\n%s", proseRaw)
	}
	if strings.Contains(proseRaw, "the widget rotates") {
		t.Fatalf("prose output printed a match body despite refusing:\n%s", proseRaw)
	}
}

// TestKnowledgeQueryStaleWithheldRefusalNamesRemedy is the fourth required
// fixture: the refusal itself must state the private-index remedy, not
// merely decline -- "a refusal with no path forward is its own defect"
// (agent-estate#1306). Checked against the SAME wording
// knowledge.PrivateIndexRemedy gives the write-guard-adjacent staleness
// messages, so this and every other site name the identical path.
func TestKnowledgeQueryStaleWithheldRefusalNamesRemedy(t *testing.T) {
	bin := buildEstateBinary(t)
	idx := filepath.Join(t.TempDir(), "index.json")
	generatedAt := time.Now().UTC().Add(-1 * time.Hour)
	writeWithheldFixtureIndexAt(t, idx, generatedAt, 1, 3)

	vaultDir := writeVaultFixture(t, time.Now())
	corpusPath := writeCorpusFixture(t, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))

	got, raw, _ := runStaleWithheldQuery(t, bin, idx, vaultDir, corpusPath, true)
	if got.State != "stale_withheld_refused" {
		t.Fatalf("State = %q, want stale_withheld_refused\nraw: %s", got.State, raw)
	}
	if !strings.Contains(got.Reason, knowledge.KnowledgeIndexEnv) {
		t.Fatalf("Reason does not name %s, the permitted private-index override:\n%s", knowledge.KnowledgeIndexEnv, got.Reason)
	}
	if !strings.Contains(got.Reason, "estate knowledge") {
		t.Fatalf("Reason does not name the `estate knowledge` command to run against that private path:\n%s", got.Reason)
	}
	if strings.Contains(got.Reason, "regenerate with `estate knowledge`") {
		t.Fatalf("Reason still contains the forbidden bare advice this issue exists to remove:\n%s", got.Reason)
	}
}
