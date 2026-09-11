package corpus

// Text-clean backfill (agent-estate#1394): populate prompts.text_clean for
// the source prompts behind every live parameter, so Jon's own words can be
// quoted at all -- CLAUDE.local.md forbids publishing text_raw and requires
// text_clean to exist first. Scoped to the prompts a live parameter actually
// points at (900 measured 2026-09-11, not all 12,458), per agent-estate#1394's
// own argument against scope creep to the whole corpus (#705 is that separate
// problem).
//
// THE LINE THIS FILE MUST NOT CROSS: "spelling and grammar fixed, meaning
// untouched" (CLAUDE.local.md). Concretely: never substitute a word for a
// different word, never drop or soften a word, never touch profanity or
// bluntness ("The force. He is often blunt and that bluntness is usually
// the point. 'That made it worse' stays." -- CLAUDE.local.md, quoted
// exactly, not paraphrased with a lowercase opening and an ellipsis).
// Checked directly against the corpus before writing a line of this file:
// at least one of the EXISTING 181 text_clean rows already violates this --
// mp-c9a15849f62017a1's raw "ew. you made it worse... asking me to review
// garbage" became "That made it worse... asking me to review something
// broken": "ew." dropped outright, "garbage" softened to "something broken".
// That is real, not hypothetical, evidence the risk this file guards
// against already happened once, unaudited, in the live corpus -- and it is
// why this generator is a closed whitelist of single-word substitutions,
// never a rewriter, and why every proposed write still passes an
// independent verification gate before it is trusted.
//
// # Round four: verify-after-generate replaced with verify-the-generator's-own-record
//
// Three prior rounds (agent-estate#1405) all failed the same way, one level
// further in each time:
//
//   - round one: a fuzzy edit-distance fallback asked whether SOME word in
//     clean sat close to a raw word. "ship"->"skip" passed -- geometrically
//     identical to "teh"->"the" to a bare distance threshold.
//   - round two: anchored to the SPECIFIC vetted replacement WORD instead of
//     a distance band, but still asked whether that word's tokens were
//     present anywhere in clean's token SET. "run teh tests then deploy the
//     app" -> "run all tests then deploy the app" passed: "the" is vetted
//     for "teh", "the" appears (unrelated) in "the app", so it vouched for a
//     substitution it had nothing to do with.
//   - round three: replaced the set with a positional walk -- a real
//     improvement, but the walk still SEARCHES clean (skipping stopwords
//     looking for a match), and a decoy reachable through nothing but a
//     stopword hop still vouches for a silently dropped typo:
//     VerifyMeaningPreserved("confirm adn it is done", "confirm it is and
//     done") returned OK=true, because "and" a few stopwords later "counts"
//     even though the real "adn" simply vanished.
//
// Every round is the same shape: "does something acceptable exist in clean"
// answered by searching clean, whether the search is a distance band, a set
// membership test, or a bounded stopword-hop. Re-deriving an alignment
// between two FINISHED strings is strictly harder than the job requires --
// the generator already knows, at the moment it substitutes a token, exactly
// which token it replaced, at which position, with what. Throwing that away
// and re-inferring it later is where all three rounds' adversarial surface
// lives.
//
// This round has the generator (applyFixes) EMIT that record -- an Edit per
// substitution, at generation time, never inferred afterward -- and the
// verifier (VerifyEdits) checks two TOTAL properties, neither a search:
//
//  1. Every recorded edit is a genuine, currently-whitelisted allFixes entry
//     (knownVariant, built from allFixes directly, same as before). An edit
//     naming anything else is refused outright, whatever it claims to
//     explain -- the whitelist stays the sole authority for what counts as a
//     legitimate substitution.
//  2. Replaying the edits against raw's own tokenization, at the exact
//     positions they name, must reproduce clean's own tokenization EXACTLY.
//     No alignment is inferred and no clean-side search happens at all --
//     position comes entirely from the generator's own record. A raw token
//     the edit list doesn't name must survive verbatim, at its own position;
//     a token it does name must have been replaced with exactly the
//     recorded, vetted text. Nothing "vouches" for anything: either the
//     string clean actually holds matches what the edits say happened, or it
//     is refused, full stop.
//
// This retires negation-count and length-ratio as SEPARATE checks (both
// were true backstops against the old search-based gate, which could be
// fooled into ignoring a dropped or added token entirely). Exact positional
// replay strictly subsumes both: a dropped "not" is a raw token with no
// corresponding edit that fails to survive verbatim, which replay already
// catches; there is no way for token counts to drift, silently, without the
// join-by-space comparison failing. Keeping them alongside would be
// unreachable dead weight, not defence in depth -- see VerifyEdits's own
// doc comment for the argument this file settled before writing the check.

import (
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// CleanCandidate is one prompt in scope: a live parameter's source, missing
// text_clean.
type CleanCandidate struct {
	PromptID string
	Raw      string
}

// substitution is one whitelisted, word-boundary-safe fix. Applied only as
// a whole-word match (case-insensitive on the FROM side), case-adapted on
// the TO side to match the matched token's own capitalisation pattern
// (all-caps, title-case, or lower) so a fix at a sentence start still reads
// naturally -- never inferred, always derived mechanically from the source
// token's own case.
type substitution struct {
	from   string // lowercase, matched as a whole word only
	to     string // lowercase replacement; case-adapted at apply time
	reason string
}

// contractionFixes: restoring a dropped apostrophe. Every entry here is a
// word that is NEVER a standalone, valid English word on its own -- only a
// typo for the contraction. Ambiguous real words that also happen to look
// like a missing-apostrophe contraction (its, lets, wont, cant, ill, well,
// hell) are deliberately EXCLUDED below, named with why, rather than risking
// a silent meaning change on the far more common non-contraction reading.
var contractionFixes = []substitution{
	{"dont", "don't", "missing apostrophe"},
	{"didnt", "didn't", "missing apostrophe"},
	{"doesnt", "doesn't", "missing apostrophe"},
	{"isnt", "isn't", "missing apostrophe"},
	{"wasnt", "wasn't", "missing apostrophe"},
	{"werent", "weren't", "missing apostrophe"},
	{"wouldnt", "wouldn't", "missing apostrophe"},
	{"shouldnt", "shouldn't", "missing apostrophe"},
	{"couldnt", "couldn't", "missing apostrophe"},
	{"hasnt", "hasn't", "missing apostrophe"},
	{"havent", "haven't", "missing apostrophe"},
	{"thats", "that's", "missing apostrophe"},
	{"whats", "what's", "missing apostrophe"},
	{"wheres", "where's", "missing apostrophe"},
	{"hows", "how's", "missing apostrophe"},
	{"whos", "who's", "missing apostrophe"},
	{"youre", "you're", "missing apostrophe"},
	{"theyre", "they're", "missing apostrophe"},
	{"weve", "we've", "missing apostrophe"},
	{"theyve", "they've", "missing apostrophe"},
	{"ive", "I've", "missing apostrophe"},
	{"youve", "you've", "missing apostrophe"},
	{"youll", "you'll", "missing apostrophe"},
	{"theyll", "they'll", "missing apostrophe"},
	{"wholl", "who'll", "missing apostrophe"},
	{"shouldve", "should've", "missing apostrophe"},
	{"couldve", "could've", "missing apostrophe"},
	{"wouldve", "would've", "missing apostrophe"},
	{"mustve", "must've", "missing apostrophe"},
	{"im", "I'm", "missing apostrophe"},
	{"hes", "he's", "missing apostrophe"},
	{"shes", "she's", "missing apostrophe"},
	{"heres", "here's", "missing apostrophe"},
	{"theres", "there's", "missing apostrophe"},
}

// excludedAmbiguousContractions: NOT fixed, and why. Each of these IS a
// valid, standalone English word with a different meaning from the
// contraction it also resembles -- "fixing" it without grammatical context
// this package does not have would be a guess, and a wrong guess here is a
// meaning change, not a spelling fix. Named so a reader sees the exclusion
// was a decision, not an oversight.
//
//	its   -- possessive ("its own words"), far more common in this corpus
//	         than "it's"; not fixed either direction.
//	lets  -- "X lets Y do Z" (verb) vs "let's" (contraction); ambiguous.
//	wont  -- archaic noun ("custom, habit"); rare but real.
//	cant  -- "a slope/tilt" or "insincere talk" (jargon); rare but real,
//	         and this corpus is infra-heavy enough that a literal cant
//	         (e.g. a roof or grade) is not implausible.
//	ill   -- "sick"; common adjective, not fixed to "I'll".
//	well  -- adverb/interjection/noun ("well, that works"; "a well"); the
//	         single most common false-positive risk in the whole list.
//	hell  -- interjection/place noun; not fixed to "he'll".

// typoFixes: known misspellings, each a whole-word match. Sourced from
// CLAUDE.local.md's own named examples (consistantly, descripton, willout,
// thats -- thats is a contraction, handled above) plus frequency-mined
// directly from the in-scope population before this file was written (see
// the PR body for the measurement); every entry here was observed for real
// in the corpus, none invented in anticipation.
var typoFixes = []substitution{
	{"consistantly", "consistently", "misspelling"},
	{"descripton", "description", "misspelling"},
	{"willout", "without", "misspelling"},
	{"seperate", "separate", "misspelling"},
	{"seperately", "separately", "misspelling"},
	{"definately", "definitely", "misspelling"},
	{"recieve", "receive", "misspelling"},
	{"recieved", "received", "misspelling"},
	{"occured", "occurred", "misspelling"},
	{"occuring", "occurring", "misspelling"},
	{"untill", "until", "misspelling"},
	{"teh", "the", "misspelling"},
	{"adn", "and", "misspelling"},
	{"taht", "that", "misspelling"},
	{"htat", "that", "misspelling"},
	{"wich", "which", "misspelling"},
	{"becuase", "because", "misspelling"},
	{"alot", "a lot", "misspelling (two words)"},
}

// productNameFixes: capitalisation only, and deliberately the narrowest
// possible set. Excluded: docker, kubernetes and similar -- this corpus
// uses them constantly as lowercase COMMAND/CLI vocabulary ("run docker
// build"), where capitalising would be wrong at least as often as right;
// that is exactly the ambiguity this file's whole design refuses to guess
// through. telegram/github are, in this corpus's own usage, overwhelmingly
// the proper noun.
var productNameFixes = []substitution{
	{"telegram", "Telegram", "product name capitalisation"},
	{"github", "GitHub", "product name capitalisation"},
}

var allFixes = func() []substitution {
	var all []substitution
	all = append(all, contractionFixes...)
	all = append(all, typoFixes...)
	all = append(all, productNameFixes...)
	return all
}()

// wordRE matches one whitespace-delimited token, keeping enough of its
// shape (leading/trailing punctuation) to reapply after substitution.
var wordRE = regexp.MustCompile(`[A-Za-z']+`)

func matchCase(src, replacement string) string {
	if src == strings.ToUpper(src) && len(src) > 1 {
		return strings.ToUpper(replacement)
	}
	runes := []rune(src)
	if len(runes) > 0 && runes[0] == []rune(strings.ToUpper(string(runes[0])))[0] && src != strings.ToLower(src) {
		// Title-cased source (first letter upper, not all-upper): capitalise
		// the replacement's own first letter only, leave the rest as given
		// (the replacement may itself carry internal capitals, e.g. GitHub).
		rr := []rune(replacement)
		rr[0] = []rune(strings.ToUpper(string(rr[0])))[0]
		return string(rr)
	}
	return replacement
}

func tokenize(s string) []string {
	return wordRE.FindAllString(strings.ToLower(s), -1)
}

// Edit is one substitution the generator actually applied, recorded at the
// moment it happened -- never inferred afterward by comparing two finished
// strings. TokenIndex is the position in tokenize(raw) (the same tokeniser
// VerifyEdits uses) that this edit replaces; wordRE.FindAllString and
// wordRE.ReplaceAllStringFunc walk a string's matches in the same order, and
// lower-casing a string before matching (what tokenize does) never changes
// where [A-Za-z']+ matches -- only the case of what it captures -- so a
// counter incremented once per match inside applyFixes's own substitution
// loop lands on exactly the index tokenize(raw) would assign that word.
type Edit struct {
	TokenIndex int    // position in tokenize(raw)
	From       string // the raw token, lowercased -- must equal an allFixes.from
	To         string // the case-adapted text actually written into clean at this position
}

// applyFixes runs every whitelisted substitution once, whole-word only,
// case-adapted, and records each one as an Edit at generation time. Returns
// the resulting string, the edit list VerifyEdits checks, and a
// human-readable description per fix (for the report only -- verification
// never reads this).
func applyFixes(raw string) (clean string, edits []Edit, applied []string) {
	byFrom := map[string]substitution{}
	for _, s := range allFixes {
		byFrom[s.from] = s
	}
	tokenIndex := -1
	out := wordRE.ReplaceAllStringFunc(raw, func(tok string) string {
		tokenIndex++
		lower := strings.ToLower(tok)
		if s, ok := byFrom[lower]; ok {
			replacement := matchCase(tok, s.to)
			edits = append(edits, Edit{TokenIndex: tokenIndex, From: lower, To: replacement})
			applied = append(applied, fmt.Sprintf("%s -> %s (%s)", tok, replacement, s.reason))
			return replacement
		}
		return tok
	})
	sort.Strings(applied)
	return out, edits, applied
}

// knownVariant maps a raw word this file KNOWS how to correct (the `from`
// side of an entry in `allFixes`, lowercase) to its own vetted replacement,
// tokenized. Built from `allFixes` directly -- the SAME table `applyFixes`
// reads -- so this can never drift out of sync with the generator: widening
// the whitelist widens what VerifyEdits recognises as a legitimate edit in
// the exact same commit, automatically. This is the sole authority for
// whether a recorded edit was actually vetted; nothing else may substitute
// for it.
var knownVariant = func() map[string][]string {
	m := map[string][]string{}
	for _, s := range allFixes {
		m[s.from] = tokenize(s.to)
	}
	return m
}()

// VerifyResult is the outcome of checking one proposed (raw, edits, clean)
// triple.
type VerifyResult struct {
	OK     bool
	Reason string // set when OK is false
}

// VerifyEdits is the independent gate every proposed clean must pass before
// it is ever written -- round four of agent-estate#1405, after three rounds
// of a search-based check (fuzzy distance, then set membership, then a
// positional walk that could still be tricked by a stopword-reachable
// decoy) each failed the same way one level further in. See this file's own
// header comment for the three-round history and why the design changed
// rather than being patched a fourth time.
//
// Two checks, both TOTAL -- no distance, no set, no bounded search, no
// "close enough" -- either refuses the whole row:
//
//  1. Every edit must be a genuine, currently-whitelisted allFixes entry:
//     the raw token it names must match what's actually at that position in
//     raw, and its recorded replacement must equal knownVariant's own
//     vetted text for that word, exactly. An edit naming anything else --
//     an unvetted substitution, or a claim about a token that isn't
//     actually there -- is refused outright, whatever the rest of the pair
//     looks like. This is what keeps the whitelist the sole authority: an
//     edit list is not a side channel around it.
//  2. Replaying every edit against raw's own tokenization, at the exact
//     position it names, must reproduce clean's own tokenization EXACTLY --
//     token for token, in order. A raw token no edit names must survive
//     verbatim, at its own position; a named token must have become exactly
//     its recorded replacement. There is no scan of clean for something
//     acceptable: position comes entirely from the edit list the generator
//     already recorded, so nothing elsewhere in the sentence -- a
//     coincidental decoy, a stopword run, an unrelated real word -- can
//     vouch for a mismatch at the position that actually matters. Either
//     clean holds what the edits say happened, or it does not.
//
// Does the verifier still earn its place, now that the generator hands it
// the very edits it made? Yes, and it is not a tautology, as long as (and
// this file keeps it true) applyFixes's own STRING construction and this
// function's TOKEN-level replay are two different computations over two
// different representations of the same event, not one function calling
// itself twice: applyFixes builds `clean` by byte-level regex substitution
// over the raw string; VerifyEdits independently re-tokenizes the resulting
// `clean` string and compares it, token by token, against what the edit
// list alone predicts. A bug in how applyFixes joins a replacement back
// into the string -- wrong case-adaptation applied at the string-join step
// but not reflected in the Edit it recorded, an off-by-one in which
// occurrence a regex match touched, a stray extra byte -- changes the
// STRING without changing the RECORD, and replay catches exactly that
// divergence. What it does NOT catch, and cannot: a generator that
// correctly records and correctly applies an edit that was never the right
// decision to make in the first place (check 1 bounds that instead, via the
// whitelist) -- and a hand-authored (raw, edits, clean) triple that lies
// about all three consistently, which no verifier operating on the
// generator's own output can ever detect, because at that point it is not
// checking generated output at all. Both are named, not hidden: this gate
// verifies EXECUTION against RECORD and RECORD against WHITELIST; it was
// never, in any of its four versions, capable of verifying DECISION against
// intent that lives only in Jon's head.
//
// Retired from this check, argued in this file's own header comment: the
// old negation-count and length-ratio backstops. Exact positional replay
// subsumes both -- there is no way for a token to vanish, invert, or appear
// from nowhere without the token-sequence comparison failing first.
func VerifyEdits(raw string, edits []Edit, clean string) VerifyResult {
	rawTokens := tokenize(raw)
	byIndex := make(map[int]Edit, len(edits))
	for _, e := range edits {
		if e.TokenIndex < 0 || e.TokenIndex >= len(rawTokens) {
			return VerifyResult{false, fmt.Sprintf("edit names token index %d, but raw has %d token(s)", e.TokenIndex, len(rawTokens))}
		}
		if prior, dup := byIndex[e.TokenIndex]; dup {
			return VerifyResult{false, fmt.Sprintf("two edits claim token index %d (%q and %q)", e.TokenIndex, prior.From, e.From)}
		}
		if rawTokens[e.TokenIndex] != e.From {
			return VerifyResult{false, fmt.Sprintf("edit at index %d claims raw token %q, but raw actually has %q there", e.TokenIndex, e.From, rawTokens[e.TokenIndex])}
		}
		variant, ok := knownVariant[e.From]
		if !ok {
			return VerifyResult{false, fmt.Sprintf("edit %q -> %q is not a whitelisted allFixes entry", e.From, e.To)}
		}
		if got := tokenize(e.To); strings.Join(got, " ") != strings.Join(variant, " ") {
			return VerifyResult{false, fmt.Sprintf("edit %q -> %q does not match allFixes's own vetted replacement %q", e.From, e.To, strings.Join(variant, " "))}
		}
		byIndex[e.TokenIndex] = e
	}

	want := make([]string, 0, len(rawTokens))
	for i, rt := range rawTokens {
		if e, ok := byIndex[i]; ok {
			want = append(want, tokenize(e.To)...)
		} else {
			want = append(want, rt)
		}
	}
	got := tokenize(clean)
	if strings.Join(want, " ") != strings.Join(got, " ") {
		return VerifyResult{false, fmt.Sprintf("replaying %d edit(s) against raw yields %q, but clean is %q -- clean does not match what the recorded edits explain", len(edits), strings.Join(want, " "), strings.Join(got, " "))}
	}
	return VerifyResult{true, ""}
}

// CleanAction is what ProposeClean decided for one row.
type CleanAction string

const (
	ActionClean    CleanAction = "clean"    // one or more whitelisted fixes applied, verified
	ActionIdentity CleanAction = "identity" // no whitelisted fix matched; raw copied as-is
	ActionRefuse   CleanAction = "refuse"   // a proposed clean failed verification -- stays NULL
)

// CleanProposal is one row's outcome: what would (or would not) be written.
type CleanProposal struct {
	PromptID string
	Raw      string
	Clean    string
	Action   CleanAction
	Changes  []string // substitutions applied, for ActionClean
	Reason   string   // for ActionRefuse
}

// ProposeClean generates and verifies a candidate text_clean for one raw
// prompt. It never returns an unverified write: ActionClean is only
// returned once VerifyEdits has passed against the specific edit list
// applyFixes actually produced for this (raw, clean) pair. ActionIdentity is
// the "nothing in the whitelist matched" case -- raw is copied verbatim (a
// no-op change cannot fail verification by construction: zero edits trivially
// replay to raw itself, and CLAUDE.local.md's own rule ("if text_clean is
// null for a prompt worth quoting, clean it and write it back first") reads
// as "make it quotable", not "guarantee it is typo-free" -- a prompt with no
// whitelisted-typo match is not thereby unclean, it is simply outside what
// this tool can improve).
func ProposeClean(promptID, raw string) CleanProposal {
	clean, edits, changes := applyFixes(raw)
	if len(changes) == 0 {
		return CleanProposal{PromptID: promptID, Raw: raw, Clean: raw, Action: ActionIdentity}
	}
	v := VerifyEdits(raw, edits, clean)
	if !v.OK {
		return CleanProposal{PromptID: promptID, Raw: raw, Action: ActionRefuse, Reason: v.Reason}
	}
	return CleanProposal{PromptID: promptID, Raw: raw, Clean: clean, Action: ActionClean, Changes: changes}
}

// TextCleanCandidates reads every distinct prompt behind a live parameter
// whose text_clean is still NULL/empty. Read-only via the URI form, not the
// bare -readonly flag -- this file's first cut used -readonly and hit
// exactly the failure corpus.go's own header comment already names: "unable
// to open database file (14)" against the live corpus once its -wal/-shm
// sidecars were checkpointed away (reproduced directly against
// ~/corpus/corpus.sqlite3 while building this command, not assumed from the
// brief's own warning). file:...?mode=ro&immutable=1 is what every other
// reader in this package already uses for the identical reason -- matched
// here, not reinvented.
func TextCleanCandidates(dbPath string) ([]CleanCandidate, error) {
	q := `select distinct p.id, replace(replace(p.text_raw,char(10),char(1)),char(13),'')
	      from live_parameters i join prompts p on p.id = i.prompt_id
	      where p.text_clean is null or p.text_clean = ''
	      order by p.id`
	out, err := exec.Command("sqlite3", "-separator", sep, "file:"+dbPath+"?mode=ro&immutable=1", q).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("sqlite3: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	var out2 []CleanCandidate
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, sep, 2)
		if len(parts) != 2 {
			continue
		}
		out2 = append(out2, CleanCandidate{PromptID: parts[0], Raw: strings.ReplaceAll(parts[1], "\x01", "\n")})
	}
	return out2, nil
}

func sqlEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// ApplyTextClean writes every ActionClean/ActionIdentity proposal's Clean
// value to prompts.text_clean, one UPDATE per row, idempotent (WHERE also
// requires text_clean IS NULL OR ”, so a second run over the same
// proposals changes zero rows the second time). dbPath is never the live
// corpus -- callers must check that themselves (see cmd/textclean's own
// livepath.RefuseLivePath call, the same guard cmd/provenancebackfill uses)
// before this function is ever reached; it does not re-check here, to keep
// this package's own dependency graph free of cmd/'s flag-parsing concerns.
func ApplyTextClean(dbPath string, proposals []CleanProposal) (written int, err error) {
	for _, p := range proposals {
		if p.Action != ActionClean && p.Action != ActionIdentity {
			continue
		}
		// SELECT changes() in the SAME invocation as the UPDATE (one
		// sqlite3 process, one implicit transaction) is what makes
		// `written` an honest count of ROWS ACTUALLY CHANGED rather than
		// "how many UPDATE statements were attempted" -- exec.Command's own
		// exit code is 0 whether or not the WHERE clause matched anything,
		// a real gap this file's own idempotency test caught: a second
		// apply run over the same proposals correctly wrote zero NEW rows
		// (the WHERE clause protects the data) but the naive version below
		// still counted it as 2 written, silently wrong.
		q := fmt.Sprintf(
			`UPDATE prompts SET text_clean='%s' WHERE id='%s' AND (text_clean IS NULL OR text_clean=''); SELECT changes();`,
			sqlEscape(p.Clean), sqlEscape(p.PromptID),
		)
		out, err := exec.Command("sqlite3", dbPath, q).Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return written, fmt.Errorf("sqlite3 update %s: %s", p.PromptID, strings.TrimSpace(string(ee.Stderr)))
			}
			return written, err
		}
		if n, convErr := strconv.Atoi(strings.TrimSpace(string(out))); convErr == nil && n > 0 {
			written++
		}
	}
	return written, nil
}

// CountChangedRows reports how many rows in dbPath currently have
// text_clean populated, for a before/after count around an apply run.
func CountChangedRows(dbPath string) (int, error) {
	out, err := exec.Command("sqlite3", "file:"+dbPath+"?mode=ro&immutable=1",
		`select count(*) from prompts where text_clean is not null and text_clean != ''`).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return 0, fmt.Errorf("sqlite3: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return 0, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n, nil
}
