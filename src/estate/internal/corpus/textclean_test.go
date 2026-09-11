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
		got, _, changes := applyFixes(c.raw)
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
		got, edits, changes := applyFixes(raw)
		if got != raw || len(changes) != 0 || len(edits) != 0 {
			t.Errorf("applyFixes(%q) changed an ambiguous word: got %q, changes %v, edits %v", raw, got, changes, edits)
		}
	}
}

func TestApplyFixesTyposAndProductNames(t *testing.T) {
	got, edits, _ := applyFixes("this consistantly fails, seperate the descripton, and use telegram or github")
	want := "this consistently fails, separate the description, and use Telegram or GitHub"
	if got != want {
		t.Errorf("applyFixes() = %q, want %q", got, want)
	}
	if len(edits) != 5 {
		t.Errorf("expected 5 edits (consistantly, seperate, descripton, telegram, github), got %d: %+v", len(edits), edits)
	}
}

func TestApplyFixesNoMatchReturnsUnchanged(t *testing.T) {
	raw := "ship the fix and merge the PR once CI is green"
	got, edits, changes := applyFixes(raw)
	if got != raw || len(changes) != 0 || len(edits) != 0 {
		t.Errorf("expected no change, got %q changes %v edits %v", got, changes, edits)
	}
}

// applyFixes's own TokenIndex bookkeeping must agree with tokenize(raw)'s
// own indexing -- VerifyEdits trusts this correspondence completely and
// never re-derives it by searching, so a drift here would be silent and
// total.
func TestApplyFixesTokenIndexMatchesTokenize(t *testing.T) {
	raw := "run teh tests then deploy the app, and adn fix taht bug"
	_, edits, _ := applyFixes(raw)
	rawTokens := tokenize(raw)
	if len(edits) != 3 {
		t.Fatalf("expected 3 edits (teh, adn, taht), got %d: %+v", len(edits), edits)
	}
	for _, e := range edits {
		if rawTokens[e.TokenIndex] != e.From {
			t.Errorf("edit claims index %d is %q, but tokenize(raw)[%d] = %q", e.TokenIndex, e.From, e.TokenIndex, rawTokens[e.TokenIndex])
		}
	}
}

// --- The real corruption already found live in the corpus, and the three
// hand-authored word-substitution shapes -- none of these are whitelisted,
// so the generator would never emit an edit for them. The honest
// reconstruction under the edit-list design is a generator that claims
// ZERO edits explain the transformation: replay then expects clean to equal
// raw, token for token, and any of these must fail that immediately. ---

// MUTATION-CHECK: this is the exact shape of corruption found LIVE in the
// corpus's own existing text_clean population (mp-c9a15849f62017a1: "ew. you
// made it worse... review garbage" -> "...review something broken", "ew."
// dropped, "garbage" softened). No edit explains this transformation --
// nothing in the whitelist touches any of the changed words -- so a
// generator claiming zero edits, replayed against this raw/clean pair, must
// be refused.
func TestVerifyCatchesTheRealCorruptionFoundInTheCorpus(t *testing.T) {
	raw := "ew. you made it worse. Can you screen shot at look yourself willout asking me to review garbage"
	corrupted := "That made it worse. Screenshot it and look at it yourself instead of asking me to review something broken."
	v := VerifyEdits(raw, nil, corrupted)
	if v.OK {
		t.Fatalf("VerifyEdits passed the exact real corruption found in the corpus -- it must not")
	}
	t.Logf("correctly refused: %s", v.Reason)
}

func TestVerifyCatchesDroppedNegation(t *testing.T) {
	v := VerifyEdits("do not deploy this", nil, "do deploy this")
	if v.OK {
		t.Fatal("a dropped negation, claimed by zero edits, must be refused")
	}
}

func TestVerifyCatchesWordSubstitution(t *testing.T) {
	v := VerifyEdits("delete the old branch", nil, "remove the old branch")
	if v.OK {
		t.Fatal("delete -> remove is a word substitution, not a spelling fix, and claiming zero edits for it must be refused")
	}
}

// Round one's own finding (fuzzy edit-distance let a meaning-inverting
// substitution through because two short, unrelated real words sat close in
// edit distance). Reconstructed as a generator FALSELY CLAIMING the
// substitution is a vetted edit -- none of these five pairs is in allFixes,
// so the whitelist check must refuse every one regardless of what the
// string itself shows.
func TestVerifyRefusesUnvettedEditsShipSkipAndRoundOnesFour(t *testing.T) {
	for _, c := range []struct{ raw, clean, from, to, label string }{
		{"ship the release tonight", "skip the release tonight", "ship", "skip", "ship/skip -- opposite instructions"},
		{"that lets the process finish", "that lots the process finish", "lets", "lots", "lets/lots -- nonsense substitution"},
		{"deploy from main", "deploy form main", "from", "form", "from/form"},
		{"push the fix now", "push the fix new", "now", "new", "now/new"},
		{"read the file", "reed the file", "read", "reed", "read/reed"},
	} {
		rawTokens := tokenize(c.raw)
		idx := -1
		for i, tok := range rawTokens {
			if tok == c.from {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("%s: %q not found in tokenize(%q)", c.label, c.from, c.raw)
		}
		v := VerifyEdits(c.raw, []Edit{{TokenIndex: idx, From: c.from, To: c.to}}, c.clean)
		if v.OK {
			t.Fatalf("%s: VerifyEdits claimed %q->%q as an edit and it was accepted -- neither is a whitelisted allFixes entry", c.label, c.from, c.to)
		}
		t.Logf("%s: correctly refused: %s", c.label, v.Reason)
	}
}

// Round two's and round three's own findings, reconstructed under the
// edit-list design as what they actually were: a generator correctly
// RECORDING a genuine, vetted edit (teh->the, adn->and, taht->that -- all
// real allFixes entries), but the resulting clean STRING not actually
// holding that replacement at the position the edit claims -- exactly the
// shape that let a decoy elsewhere (a "the" that happened to already be in
// "the app", a stopword-reachable coincidental "and") vouch for a
// substitution it had nothing to do with, in every prior round's
// search-based check. Replay has no search to fool: either clean holds the
// recorded replacement at the recorded position, or it is refused, and
// whatever ELSE clean contains is irrelevant to that comparison.
func TestVerifyEditsRefusesWhenCleanDoesNotActuallyHoldTheRecordedReplacement(t *testing.T) {
	for _, c := range []struct {
		raw, clean, from, to, label string
	}{
		{"run teh tests then deploy the app", "run all tests then deploy the app", "teh", "the",
			"teh recorded as ->the, but clean has ->all at that position (round two's exact bug)"},
		{"fix teh bug in the module", "fix bug in the module", "teh", "the",
			"teh recorded as ->the, but clean simply dropped it -- an unrelated \"the\" already present must not vouch"},
		{"stop adn revert, and report", "stop now revert, and report", "adn", "and",
			"adn recorded as ->and, but clean has ->now at that position"},
		{"i think taht is wrong, that one", "i think this is wrong, that one", "taht", "that",
			"taht recorded as ->that (its real vetted form), but clean has ->this -- an unrelated \"that\" later must not vouch"},
		{"confirm adn it is done", "confirm it is and done", "adn", "and",
			"round three's own finding: adn recorded as ->and at its real position, but clean dropped it and a stopword-reachable \"and\" later must not vouch"},
	} {
		rawTokens := tokenize(c.raw)
		idx := -1
		for i, tok := range rawTokens {
			if tok == c.from {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("%s: %q not found in tokenize(%q)", c.label, c.from, c.raw)
		}
		v := VerifyEdits(c.raw, []Edit{{TokenIndex: idx, From: c.from, To: c.to}}, c.clean)
		if v.OK {
			t.Fatalf("%s: VerifyEdits(%q, edit %s->%s @%d, %q) returned OK -- clean does not actually hold the recorded replacement at the recorded position", c.label, c.raw, c.from, c.to, idx, c.clean)
		}
		t.Logf("%s: correctly refused: %s", c.label, v.Reason)
	}
}

// Bonus closure, carried from round three: an added content word with no
// edit behind it. All three shapes -- leading, interior, trailing -- are
// now caught by the exact same mechanism (a token-count mismatch in the
// final replay comparison), not three separate cases: the edit list
// claims zero edits, so replay expects clean's tokens to equal raw's
// tokens exactly, and any extra token anywhere fails that immediately,
// regardless of where it was inserted. Simpler than round three's own
// walk-plus-leftover-check split, and covers the same ground.
func TestVerifyCatchesAddedContentWords(t *testing.T) {
	for _, c := range []struct{ raw, clean, label string }{
		{"commit before continuing", "always commit before continuing", "leading addition"},
		{"commit before continuing", "commit always before continuing", "interior addition"},
		{"commit before continuing", "commit before continuing always", "trailing addition"},
	} {
		v := VerifyEdits(c.raw, nil, c.clean)
		if v.OK {
			t.Fatalf("%s: VerifyEdits(%q, nil, %q) returned OK -- an added content word with no edit behind it must be refused", c.label, c.raw, c.clean)
		}
		t.Logf("%s: correctly refused: %s", c.label, v.Reason)
	}
}

// The real generator path: applyFixes's own edits, replayed by VerifyEdits,
// must pass for every whitelisted fix it actually makes -- including the
// short transpositions (teh, adn, htat, taht) that broke round one's first
// cut of the fuzzy-distance check for an unrelated reason (a distance
// threshold that floored to 0 for short words). That specific bug cannot
// recur here -- there is no distance threshold left in this design at all.
func TestVerifyAcceptsRealGeneratorEdits(t *testing.T) {
	for _, raw := range []string{
		"read teh corpus",
		"typescript adn blank",
		"confirmed htat we",
		"after taht is confirmed",
		"this consistantly fails and the descripton is wrong",
		"use telegram or github for updates",
	} {
		clean, edits, _ := applyFixes(raw)
		v := VerifyEdits(raw, edits, clean)
		if !v.OK {
			t.Errorf("VerifyEdits(%q, applyFixes's own edits, %q) refused: %s -- a real generator edit must always replay clean", raw, clean, v.Reason)
		}
	}
}

func TestVerifyPassesRealSpellingFixes(t *testing.T) {
	raw := "this consistantly fails and the descripton is wrong"
	clean, edits, _ := applyFixes(raw)
	v := VerifyEdits(raw, edits, clean)
	if !v.OK {
		t.Fatalf("a real, whitelisted spelling fix must pass verification, got refused: %s", v.Reason)
	}
}

func TestVerifyPreservesProfanityAndBluntness(t *testing.T) {
	// CLAUDE.local.md: "The force. He is often blunt and that bluntness is
	// usually the point. 'That made it worse' stays." text_clean
	// must carry profanity/bluntness unchanged -- this is a quoting-time
	// filter, never a cleaning-time one (see the PR body for the argument).
	raw := "this is fucking broken and you made it worse, fix it now"
	clean, edits, _ := applyFixes(raw) // no whitelisted word appears -- identity, zero edits
	if clean != raw || len(edits) != 0 {
		t.Fatalf("no fix should have applied to this sentence, got %q edits %v", clean, edits)
	}
	v := VerifyEdits(raw, edits, clean)
	if !v.OK {
		t.Fatalf("identity clean of a profane sentence must pass: %s", v.Reason)
	}
	// A hypothetical censored version, with no edit behind it, must be REFUSED.
	censored := "this is [redacted] broken and you made it worse, fix it now"
	v2 := VerifyEdits(raw, nil, censored)
	if v2.OK {
		t.Fatal("censoring profanity is a meaning/tone change with no edit behind it and must be refused, not silently accepted")
	}
}

// --- Edit-list integrity checks, new to round four: these have no round
// one/two/three analogue because there was no edit list before now, but
// they are exactly the shape a hand-authored or future-buggy edit list
// could take, and the whitelist/replay split above must catch each. ---

func TestVerifyEditsRefusesOutOfRangeTokenIndex(t *testing.T) {
	v := VerifyEdits("fix teh bug", []Edit{{TokenIndex: 99, From: "teh", To: "the"}}, "fix the bug")
	if v.OK {
		t.Fatal("an edit naming a token index past the end of raw must be refused")
	}
}

func TestVerifyEditsRefusesTwoEditsClaimingTheSameIndex(t *testing.T) {
	v := VerifyEdits("teh teh", []Edit{
		{TokenIndex: 0, From: "teh", To: "the"},
		{TokenIndex: 0, From: "teh", To: "the"},
	}, "the the")
	if v.OK {
		t.Fatal("two edits claiming the same token index must be refused, even if individually valid")
	}
}

func TestVerifyEditsRefusesAnEditThatMisnamesTheRawToken(t *testing.T) {
	// The edit claims index 1 is "teh", but raw's token 1 is actually "adn".
	v := VerifyEdits("fix adn bug", []Edit{{TokenIndex: 1, From: "teh", To: "the"}}, "fix the bug")
	if v.OK {
		t.Fatal("an edit that misnames the raw token at its own claimed index must be refused")
	}
}

func TestVerifyEditsRefusesAnEditWhoseToDoesNotMatchTheWhitelist(t *testing.T) {
	// "teh" is real and at the right position, but the recorded replacement
	// is not allFixes's own vetted form for it.
	v := VerifyEdits("fix teh bug", []Edit{{TokenIndex: 1, From: "teh", To: "there"}}, "fix there bug")
	if v.OK {
		t.Fatal("an edit whose recorded replacement is not the whitelist's own vetted text for that word must be refused")
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

// knownVariant is built from allFixes directly (see its own doc comment):
// every from/to pair the generator can produce must be recognised as a
// legitimate variant, and nothing else should be -- this is the load-bearing
// property the whitelist-membership check in VerifyEdits depends on
// completely, now that there is no fuzzy fallback of any kind left to fall
// back to.
func TestKnownVariantCoversEveryWhitelistEntryAndNothingElse(t *testing.T) {
	for _, s := range allFixes {
		variant, ok := knownVariant[s.from]
		if !ok {
			t.Errorf("knownVariant missing an entry for whitelisted word %q", s.from)
			continue
		}
		want := tokenize(s.to)
		if len(variant) != len(want) {
			t.Errorf("knownVariant[%q] = %v, want %v", s.from, variant, want)
		}
	}
	for _, w := range []string{"ship", "lets", "delete", "garbage"} {
		if _, ok := knownVariant[w]; ok {
			t.Errorf("knownVariant unexpectedly recognises %q -- it is not in any whitelist table", w)
		}
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
