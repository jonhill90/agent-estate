package corpus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A corpus that cannot be read must stop a dispatch, never degrade to
// "no constraints found".
func TestUnreadableCorpusIsAnError(t *testing.T) {
	t.Setenv("ESTATE_CORPUS", filepath.Join(t.TempDir(), "absent.sqlite3"))
	if _, _, err := Hard(); err == nil {
		t.Fatal("Hard() returned nil error for an absent corpus; it must refuse")
	}
}

func TestEmptyCorpusIsRefusedNotTreatedAsNoConstraints(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.sqlite3")
	if err := os.WriteFile(p, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ESTATE_CORPUS", p)
	if _, _, err := Hard(); err == nil {
		t.Fatal("Hard() accepted an unusable corpus; zero parameters must never read as 'no rules'")
	}
}

// The preamble must always state the true total and must always demand the
// agent query the rest -- the task-matched subset is never presented as the
// whole law.
func TestGroundingStatesFullCountAndDemandsIndependentCheck(t *testing.T) {
	ps := []Param{
		{Key: "tooling=cli_first", Body: "Prefer CLI-backed workflows."},
		{Key: "lang=go", Body: "The app is written in Go, never shell or python."},
	}
	g := Grounding("rewrite the shell dispatcher", ps, nil, nil)
	if !strings.Contains(g, "2 binding parameters") {
		t.Fatalf("grounding does not state the true total:\n%s", g)
	}
	if !strings.Contains(g, "NOT the whole law") {
		t.Fatalf("matched subset is not marked as partial:\n%s", g)
	}
	if !strings.Contains(g, "Query the\ncorpus yourself") {
		t.Fatalf("grounding does not require an independent check:\n%s", g)
	}
	if !strings.Contains(g, "lang=go") {
		t.Fatalf("a parameter matching the task was not surfaced:\n%s", g)
	}
}

func TestGroundingStillDemandsCheckWhenNothingMatches(t *testing.T) {
	g := Grounding("zzzz", []Param{{Key: "k", Body: "b"}}, nil, nil)
	if !strings.Contains(g, "Query the\ncorpus yourself") {
		t.Fatal("grounding dropped the independent-check requirement when no parameter matched")
	}
}

// buildFixtureCorpus creates a real sqlite3 database at a temp path with the
// same items table shape as the real corpus, via the sqlite3 CLI directly --
// this mirrors internal/knowledge's own buildFixtureCorpus helper, since both
// packages read the same table without a driver.
func buildFixtureCorpus(t *testing.T, ddl string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.sqlite3")
	cmd := exec.Command("sqlite3", path)
	cmd.Stdin = strings.NewReader(ddl)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 fixture setup failed: %v\n%s", err, out)
	}
	return path
}

// hardRowsDDL is agent-estate#1139's own fixture: one hard row of each of the
// three kinds Hard() must return, plus a hard 'thought' (must stay excluded --
// only parameter/directive/correction are ever law), a retracted 'directive'
// (must stay excluded regardless of kind), a 'dropped' directive and a
// 'needs_review' correction (both must stay excluded by status even though
// weight='hard' and kind qualifies -- this is defect A: Hard() filtered
// weight and kind but never status, injecting retired and unconfirmed
// records as law).
const hardRowsDDL = `
CREATE TABLE items (
  id INTEGER PRIMARY KEY,
  prompt_id INTEGER,
  kind TEXT,
  body TEXT,
  weight TEXT,
  status TEXT,
  status_reason TEXT,
  resolved_to TEXT,
  acked_at TEXT
);
CREATE VIEW live_parameters AS SELECT * FROM items WHERE kind = 'parameter' AND weight != 'retracted';
INSERT INTO items (id, prompt_id, kind, body, weight, status, resolved_to) VALUES
  (1, 101, 'parameter', 'Prefer CLI-backed workflows.', 'hard', 'live', 'tooling=cli_first'),
  (2, 102, 'directive', 'The app is Go, never shell or python.', 'hard', 'acted', NULL),
  (3, 103, 'correction', 'Not X after all -- Y is correct.', 'hard', 'acted', NULL),
  (4, 104, 'thought', 'A stray musing, must not appear even though hard.', 'hard', 'open', NULL),
  (5, 105, 'directive', 'A retracted directive, must not appear.', 'retracted', 'acted', NULL),
  (6, 106, 'directive', 'A dropped directive, must not appear as law.', 'hard', 'dropped', NULL),
  (7, 107, 'correction', 'A needs_review correction, must not appear as law.', 'hard', 'needs_review', NULL);
`

// TestHardReturnsAllThreeKindsExcludingThoughtAndRetracted is
// agent-estate#1139's coverage test: Hard() must widen past live_parameters
// (parameter-only) to also return directive and correction rows, while still
// excluding a hard thought and a retracted directive -- the exact two rows a
// `select *` from items would wrongly include.
func TestHardReturnsAllThreeKindsExcludingThoughtAndRetracted(t *testing.T) {
	path := buildFixtureCorpus(t, hardRowsDDL)
	t.Setenv("ESTATE_CORPUS", path)

	ps, _, err := Hard()
	if err != nil {
		t.Fatalf("Hard() returned an error against a valid fixture: %v", err)
	}
	if len(ps) != 3 {
		t.Fatalf("Hard() returned %d rows, want exactly 3 (one parameter, one directive, one correction); got %+v", len(ps), ps)
	}
	kinds := map[string]bool{}
	for _, p := range ps {
		kinds[p.Kind] = true
		if strings.Contains(p.Body, "stray musing") {
			t.Fatalf("Hard() included a hard 'thought' row, which must stay excluded: %+v", p)
		}
		if strings.Contains(p.Body, "retracted directive") {
			t.Fatalf("Hard() included a retracted row: %+v", p)
		}
	}
	for _, want := range []string{"parameter", "directive", "correction"} {
		if !kinds[want] {
			t.Fatalf("Hard() result %+v is missing a %q row", ps, want)
		}
	}
}

// TestHardExcludesDroppedAndNeedsReviewButReportsThem is agent-estate#1139's
// defect-A regression test. Hard() filtered weight and kind but never
// status, so a 'dropped' (retired) or 'needs_review' (unconfirmed) hard row
// was injected into every dispatch preamble under a heading that says these
// are law. Both must now be excluded from ps, AND the exclusion must be
// visible -- reported back with a count per status -- rather than silently
// dropped, per requirement 4 ("an agent must be able to tell 'there is no
// law about X' from 'the law about X was filtered out'").
func TestHardExcludesDroppedAndNeedsReviewButReportsThem(t *testing.T) {
	path := buildFixtureCorpus(t, hardRowsDDL)
	t.Setenv("ESTATE_CORPUS", path)

	ps, excluded, err := Hard()
	if err != nil {
		t.Fatalf("Hard() returned an error against a valid fixture: %v", err)
	}
	for _, p := range ps {
		if strings.Contains(p.Body, "dropped directive") {
			t.Fatalf("Hard() included a 'dropped' row as law: %+v", p)
		}
		if strings.Contains(p.Body, "needs_review correction") {
			t.Fatalf("Hard() included a 'needs_review' row as law: %+v", p)
		}
	}

	counts := map[string]int{}
	for _, e := range excluded {
		counts[e.Status] = e.Count
	}
	if counts["dropped"] != 1 {
		t.Fatalf("Hard() excluded-report says %d dropped rows, want 1: %+v", counts["dropped"], excluded)
	}
	if counts["needs_review"] != 1 {
		t.Fatalf("Hard() excluded-report says %d needs_review rows, want 1: %+v", counts["needs_review"], excluded)
	}

	g := Grounding("some task", ps, excluded, nil)
	if !strings.Contains(g, "dropped: 1") || !strings.Contains(g, "needs_review: 1") {
		t.Fatalf("Grounding() does not surface the exclusion counts to the agent:\n%s", g)
	}
}

// TestHardCountMatchesLiveCorpusAcrossAllThreeKinds is §4(a)'s live-corpus
// check: the widened read must return exactly as many rows as a direct count
// of weight='hard' rows across all three kinds. Skips when no live corpus is
// present (e.g. CI), since this is a check against the operator's own data,
// not a fixture invariant.
func TestHardCountMatchesLiveCorpusAcrossAllThreeKinds(t *testing.T) {
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no live corpus at %s -- skipping live-count cross-check: %v", p, err)
	}
	out, err := exec.Command("sqlite3", "file:"+p+"?mode=ro&immutable=1",
		"select count(*) from items where weight='hard' and kind in ('parameter','directive','correction');").Output()
	if err != nil {
		t.Fatalf("counting live corpus: %v", err)
	}
	want := strings.TrimSpace(string(out))

	ps, excluded, err := Hard()
	if err != nil {
		t.Fatalf("Hard() against the live corpus: %v", err)
	}
	excludedTotal := 0
	for _, e := range excluded {
		excludedTotal += e.Count
	}
	got := fmt.Sprintf("%d", len(ps)+excludedTotal)
	if got != want {
		t.Fatalf("Hard() returned %d live + %d excluded = %s rows, live sqlite count says %s -- the two must agree", len(ps), excludedTotal, got, want)
	}
	t.Logf("Hard() live+excluded count == live sqlite count == %s hard rows (parameter+directive+correction)", want)
}

// TestGroundingRenderedPreambleStaysUnderByteCeiling is §4(c)'s first half:
// even against a live-corpus-scale pool of long rows, the rendered preamble
// must stay under maxPreambleBytes.
func TestGroundingRenderedPreambleStaysUnderByteCeiling(t *testing.T) {
	ps := manyMatchingParams(2500, 2000)
	g := Grounding("widen dispatch grounding to include directives and corrections", ps, nil, nil)
	if len(g) > maxPreambleBytes {
		t.Fatalf("rendered preamble is %d bytes, want <= %d (maxPreambleBytes)", len(g), maxPreambleBytes)
	}
	if !strings.Contains(g, "shown -- NOT the whole law") {
		t.Fatalf("preamble does not state how many of the matched rows were actually shown:\n%s", g[:400])
	}
	t.Logf("rendered preamble: %d bytes (cap %d)", len(g), maxPreambleBytes)
}

// TestGroundingCapIsLoadBearing is §4(c)'s second half: the byte ceiling must
// be doing real work, not merely sitting in a comment. Disabling it (raising
// maxPreambleBytes, maxItemBytes and maxMatches far past anything realistic)
// against the same input must produce a render that exceeds the real cap --
// proving the enabled cap is actually bounding something, not vacuously true
// because the input was already small.
func TestGroundingCapIsLoadBearing(t *testing.T) {
	ps := manyMatchingParams(2500, 2000)
	task := "widen dispatch grounding to include directives and corrections"

	enabled := len(Grounding(task, ps, nil, nil))

	origBytes, origItem, origMatches := maxPreambleBytes, maxItemBytes, maxMatches
	maxPreambleBytes = 100 * 1024 * 1024
	maxItemBytes = 100 * 1024
	maxMatches = len(ps)
	disabled := len(Grounding(task, ps, nil, nil))
	maxPreambleBytes, maxItemBytes, maxMatches = origBytes, origItem, origMatches

	if enabled > origBytes {
		t.Fatalf("cap enabled: rendered %d bytes, want <= %d", enabled, origBytes)
	}
	if disabled <= origBytes {
		t.Fatalf("cap disabled: rendered %d bytes, want > %d (cap must be load-bearing, not vacuous)", disabled, origBytes)
	}
	t.Logf("cap enabled: %d bytes (<=%d); cap disabled: %d bytes (>%d)", enabled, origBytes, disabled, origBytes)
}

// manyMatchingParams builds n Params, each with a body of roughly bodyLen
// bytes that overlaps the task words used by the two cap tests above, so
// every one of them is a match candidate.
func manyMatchingParams(n, bodyLen int) []Param {
	filler := strings.Repeat("directives corrections grounding dispatch widen filler-word-padding ", 1+bodyLen/70)
	ps := make([]Param, n)
	kinds := []string{"parameter", "directive", "correction"}
	for i := range ps {
		ps[i] = Param{
			Key:  fmt.Sprintf("fixture-key-%d", i),
			Kind: kinds[i%len(kinds)],
			Body: fmt.Sprintf("row %d: %s", i, filler),
		}
	}
	return ps
}
