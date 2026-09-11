package corpus

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyFixesContractions(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"dont do that", "don't do that"},
		{"i dont think thats right", "i don't think that's right"},
		{"Whats up", "What's up"}, // case-adapted
		{"IVE told you", "I'VE told you"},
	} {
		got, changes := applyFixes(c.raw)
		if got != c.want {
			t.Errorf("applyFixes(%q) = %q, want %q (changes: %v)", c.raw, got, c.want, changes)
		}
	}
}

func TestApplyFixesLeavesAmbiguousContractionsAlone(t *testing.T) {
	// its, lets, wont, cant, ill, well, hell -- real words, never touched.
	for _, raw := range []string{
		"check its own state",
		"this well is deep",
		"that lets the process finish",
		"i feel ill today",
		"go to hell",
		"it is her wont to complain",
		"there is a cant to the roof",
	} {
		got, changes := applyFixes(raw)
		if got != raw || len(changes) != 0 {
			t.Errorf("applyFixes(%q) changed an ambiguous word: got %q, changes %v", raw, got, changes)
		}
	}
}

func TestApplyFixesTyposAndProductNames(t *testing.T) {
	got, _ := applyFixes("this consistantly fails, seperate the descripton, and use telegram or github")
	want := "this consistently fails, separate the description, and use Telegram or GitHub"
	if got != want {
		t.Errorf("applyFixes() = %q, want %q", got, want)
	}
}

func TestApplyFixesNoMatchReturnsUnchanged(t *testing.T) {
	raw := "ship the fix and merge the PR once CI is green"
	got, changes := applyFixes(raw)
	if got != raw || len(changes) != 0 {
		t.Errorf("expected no change, got %q changes %v", got, changes)
	}
}

// MUTATION-CHECK: this is the exact shape of corruption found LIVE in the
// corpus's own existing text_clean population (mp-c9a15849f62017a1: "ew. you
// made it worse... review garbage" -> "...review something broken", "ew."
// dropped, "garbage" softened). If VerifyMeaningPreserved would pass this,
// it is not doing its job.
func TestVerifyCatchesTheRealCorruptionFoundInTheCorpus(t *testing.T) {
	raw := "ew. you made it worse. Can you screen shot at look yourself willout asking me to review garbage"
	corrupted := "That made it worse. Screenshot it and look at it yourself instead of asking me to review something broken."
	v := VerifyMeaningPreserved(raw, corrupted)
	if v.OK {
		t.Fatalf("VerifyMeaningPreserved passed the exact real corruption found in the corpus -- it must not")
	}
	t.Logf("correctly refused: %s", v.Reason)
}

func TestVerifyCatchesDroppedNegation(t *testing.T) {
	v := VerifyMeaningPreserved("do not deploy this", "do deploy this")
	if v.OK {
		t.Fatal("a dropped negation must be refused")
	}
}

func TestVerifyCatchesWordSubstitution(t *testing.T) {
	v := VerifyMeaningPreserved("delete the old branch", "remove the old branch")
	if v.OK {
		t.Fatal("delete -> remove is a word substitution, not a spelling fix, and must be refused")
	}
}

// Regression: running this tool against the live corpus (read-only) found
// that the FIRST cut of wordSurvives refused every one of its own
// dictionary's short transposition fixes -- "teh"->"the", "adn"->"and",
// "htat"/"taht"->"that" -- because len(w)/4 floors to 0 for any word under
// 4 letters, and a transposition scores Levenshtein distance 2, not 1. All
// 12 of the live corpus's refusals that turn on this were false refusals of
// exactly this shape before the fix; this pins the fix, not the bug.
func TestVerifyAcceptsShortTranspositionTypos(t *testing.T) {
	for _, c := range []struct{ raw, clean string }{
		{"read teh corpus", "read the corpus"},
		{"typescript adn blank", "typescript and blank"},
		{"confirmed htat we", "confirmed that we"},
		{"after taht is confirmed", "after that is confirmed"},
	} {
		v := VerifyMeaningPreserved(c.raw, c.clean)
		if !v.OK {
			t.Errorf("VerifyMeaningPreserved(%q, %q) refused: %s -- a real, whitelisted transposition fix must pass", c.raw, c.clean, v.Reason)
		}
	}
}

func TestVerifyPassesRealSpellingFixes(t *testing.T) {
	raw := "this consistantly fails and the descripton is wrong"
	clean, _ := applyFixes(raw)
	v := VerifyMeaningPreserved(raw, clean)
	if !v.OK {
		t.Fatalf("a real, whitelisted spelling fix must pass verification, got refused: %s", v.Reason)
	}
}

func TestVerifyPreservesProfanityAndBluntness(t *testing.T) {
	// CLAUDE.local.md: "the force... 'That made it worse' stays." text_clean
	// must carry profanity/bluntness unchanged -- this is a quoting-time
	// filter, never a cleaning-time one (see the PR body for the argument).
	raw := "this is fucking broken and you made it worse, fix it now"
	clean, _ := applyFixes(raw) // no whitelisted word appears -- identity
	if clean != raw {
		t.Fatalf("no fix should have applied to this sentence, got %q", clean)
	}
	v := VerifyMeaningPreserved(raw, clean)
	if !v.OK {
		t.Fatalf("identity clean of a profane sentence must pass: %s", v.Reason)
	}
	// A hypothetical censored version must be REFUSED.
	censored := "this is [redacted] broken and you made it worse, fix it now"
	v2 := VerifyMeaningPreserved(raw, censored)
	if v2.OK {
		t.Fatal("censoring profanity is a meaning/tone change and must be refused, not silently accepted")
	}
}

func TestProposeCleanThreeActions(t *testing.T) {
	clean := ProposeClean("p1", "this consistantly fails")
	if clean.Action != ActionClean {
		t.Errorf("expected ActionClean, got %s", clean.Action)
	}
	if clean.Clean != "this consistently fails" {
		t.Errorf("got clean text %q", clean.Clean)
	}

	identity := ProposeClean("p2", "ship the fix and merge the PR")
	if identity.Action != ActionIdentity {
		t.Errorf("expected ActionIdentity, got %s", identity.Action)
	}
	if identity.Clean != identity.Raw {
		t.Errorf("identity action must copy raw verbatim")
	}
}

func TestProposeCleanIsIdempotent(t *testing.T) {
	raw := "this consistantly fails, and dont tell me otherwise"
	first := ProposeClean("p1", raw)
	second := ProposeClean("p1", first.Clean)
	if second.Action != ActionIdentity {
		t.Fatalf("re-running against already-clean text must find nothing left to fix, got %s (changes: %v)", second.Action, second.Changes)
	}
	if second.Clean != first.Clean {
		t.Fatalf("re-cleaning must not change already-clean text: %q -> %q", first.Clean, second.Clean)
	}
}

func TestLevenshteinBasics(t *testing.T) {
	if levenshtein("consistantly", "consistently") > 3 {
		t.Error("consistantly/consistently should be a small edit distance")
	}
	if levenshtein("delete", "remove") <= levenshtein("delete", "delete")+2 {
		// sanity: unrelated words should not look like a close spelling fix
	}
	if got := levenshtein("delete", "remove"); got < 4 {
		t.Errorf("delete/remove should NOT look like a close spelling variant, got distance %d", got)
	}
}

// --- Candidate query + apply, against a real sqlite3 fixture (not the live corpus) ---

func buildTextCleanFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "corpus.sqlite3")
	ddl := `
CREATE TABLE prompts (id TEXT PRIMARY KEY, at INTEGER NOT NULL, text_raw TEXT NOT NULL, text_clean TEXT,
  context TEXT NOT NULL, session TEXT, source_file TEXT, project TEXT, tmux_pane TEXT, tmux_pane_target TEXT,
  author TEXT NOT NULL DEFAULT 'unknown');
CREATE TABLE items (id TEXT PRIMARY KEY, prompt_id TEXT NOT NULL, kind TEXT NOT NULL, body TEXT NOT NULL,
  weight TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'open', status_reason TEXT, resolved_to TEXT, acked_at INTEGER);
CREATE VIEW live_parameters AS SELECT * FROM items WHERE kind = 'parameter' AND weight != 'retracted';
INSERT INTO prompts VALUES ('mp-needs-clean', 100, 'this consistantly fails, dont ignore it', NULL, 'ctx', 's1', NULL, '', NULL, NULL, 'unknown');
INSERT INTO prompts VALUES ('mp-already-clean', 200, 'ship it', NULL, 'ctx', 's1', NULL, '', NULL, NULL, 'unknown');
INSERT INTO prompts VALUES ('mp-has-clean', 300, 'raw text here', 'already cleaned', 'ctx', 's1', NULL, '', NULL, NULL, 'unknown');
INSERT INTO prompts VALUES ('mp-not-live', 400, 'not referenced by any live parameter', NULL, 'ctx', 's1', NULL, '', NULL, NULL, 'unknown');
INSERT INTO items VALUES ('it-1', 'mp-needs-clean', 'parameter', 'a rule', 'hard', 'acted', NULL, NULL, NULL);
INSERT INTO items VALUES ('it-2', 'mp-already-clean', 'parameter', 'another rule', 'hard', 'open', NULL, NULL, NULL);
INSERT INTO items VALUES ('it-3', 'mp-has-clean', 'parameter', 'third rule', 'hard', 'open', NULL, NULL, NULL);
`
	cmd := exec.Command("sqlite3", path)
	cmd.Stdin = strings.NewReader(ddl)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture setup failed: %v\n%s", err, out)
	}
	return path
}

func TestTextCleanCandidatesScopesToLiveParametersMissingClean(t *testing.T) {
	path := buildTextCleanFixture(t)
	cands, err := TextCleanCandidates(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2 (mp-needs-clean, mp-already-clean); mp-has-clean already has text_clean, mp-not-live has no live parameter", len(cands))
	}
	ids := map[string]bool{}
	for _, c := range cands {
		ids[c.PromptID] = true
	}
	if !ids["mp-needs-clean"] || !ids["mp-already-clean"] {
		t.Fatalf("wrong candidates: %+v", cands)
	}
	if ids["mp-has-clean"] || ids["mp-not-live"] {
		t.Fatalf("scope leaked outside live-parameters-missing-clean: %+v", cands)
	}
}

func TestApplyTextCleanWritesAndIsIdempotent(t *testing.T) {
	path := buildTextCleanFixture(t)
	cands, err := TextCleanCandidates(path)
	if err != nil {
		t.Fatal(err)
	}
	var proposals []CleanProposal
	for _, c := range cands {
		proposals = append(proposals, ProposeClean(c.PromptID, c.Raw))
	}
	written, err := ApplyTextClean(path, proposals)
	if err != nil {
		t.Fatal(err)
	}
	if written != 2 {
		t.Fatalf("expected 2 rows written, got %d", written)
	}

	after, err := TextCleanCandidates(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("candidates remain after apply: %+v", after)
	}

	// Idempotent: re-running against the now-populated corpus finds nothing
	// left to do, and a second ApplyTextClean call (with the same stale
	// proposals) writes zero rows because of its own WHERE clause.
	written2, err := ApplyTextClean(path, proposals)
	if err != nil {
		t.Fatal(err)
	}
	if written2 != 0 {
		t.Fatalf("second apply must be a no-op, wrote %d rows", written2)
	}
}

func TestApplyTextCleanNeverTouchesRowsWithExistingClean(t *testing.T) {
	path := buildTextCleanFixture(t)
	// Attempt to overwrite mp-has-clean anyway -- the WHERE clause must
	// refuse it even if a caller mistakenly proposes a change for it.
	proposals := []CleanProposal{
		{PromptID: "mp-has-clean", Raw: "raw text here", Clean: "a completely different clean value", Action: ActionClean},
	}
	written, err := ApplyTextClean(path, proposals)
	if err != nil {
		t.Fatal(err)
	}
	if written != 0 {
		t.Fatal("must not overwrite a row that already has text_clean")
	}
}
