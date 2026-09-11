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
//
// # Round five: the replay itself compared TOKENS, not the BYTES the design named
//
// Round four's design was right -- position from the generator's own
// record, not a search -- but "reproduce clean's own tokenization exactly"
// is not what the Director's design says. The design says "replaying
// exactly those edits on text_raw reproduces text_clean byte-for-byte."
// tokenize() runs on both sides of round four's final comparison, and
// tokenize keeps only [A-Za-z']+ runs, lowercased. Case, punctuation,
// digits, symbols, whitespace and every non-ASCII byte were invisible to
// that comparison: "do NOT merge this" vs "do not merge this", "is it
// done?" vs "is it done.", "wait 10 minutes" vs "wait 100 minutes", all
// verified OK, because none of those differences survives tokenize(). The
// same held for an edit's own To field -- checked against the whitelist
// only after tokenize(To), so To="don't 100" or To="DON'T!!!" verified for
// a vetted "don't", smuggled bytes a token-level check cannot see.
//
// The fix is not a new alignment strategy, a threshold, or a search --
// round four's actual design (position from the record, nothing inferred)
// was already correct; only the two comparisons inside it were checking
// the wrong representation. Fixed by making both of them exact over BYTES:
// an edit's To must equal, byte for byte, matchCase(this position's own
// original-case raw token, this fix's vetted replacement) -- the identical
// computation applyFixes performs at generation time, recomputed here
// independently rather than trusted; and the final replay walks raw's own
// wordRE matches (the same walk applyFixes performs, same order, same
// counter) substituting each edit's exact To text and leaving every other
// byte of raw -- including all the punctuation, digits, case and
// whitespace tokenize discarded -- untouched, then compares the result to
// clean with plain string equality. No tokenize() runs anywhere in either
// comparison now. See VerifyEdits's own doc comment for why this makes the
// "does the verifier still earn its place" argument stronger, not weaker.
//
// # Round six: two walks over raw, asserted identical, were not
//
// Round five's byte replay still indexed the whitelist check with
// tokenize(raw) and the replay with wordRE.FindAllString(raw, -1),
// asserting the two were "positionally identical". They were not: raw
// text containing U+0130 (İ) or U+212A (the Kelvin sign) makes
// tokenize(raw) (which lowers the whole string, then matches) invent a
// token neither character alone would match, drifting every later index
// by one -- so the whitelist check could validate a different token than
// the one the replay actually substitutes. Fixed by making it structural
// rather than asserted: one wordRE.FindAllString(raw, -1) call, with the
// lowercase form derived per already-matched element (never by lowering
// the whole string first) -- see Edit's and VerifyEdits's own doc comments.

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
// strings. TokenIndex is the position in wordRE.FindAllString(raw, -1) --
// NOT tokenize(raw) -- that this edit replaces. applyFixes's own
// substitution loop (wordRE.ReplaceAllStringFunc over raw, counter
// incremented once per match, each matched token lowered individually only
// to look up the whitelist) and VerifyEdits both walk this exact match set,
// so TokenIndex means the same position in both; this is the ONLY walk
// either of them performs over raw. It is deliberately not tokenize(raw):
// tokenize lowers the whole string before matching, and strings.ToLower can
// expand a single non-ASCII character (U+0130 "İ" into ASCII "i" plus a
// combining mark; U+212A, the Kelvin sign, into ASCII "k") into a token
// [A-Za-z']+ would not have matched in the original casing -- a real
// divergence found in review (agent-estate#1405 round six) between
// tokenize(raw)'s index space and this one.
type Edit struct {
	TokenIndex int    // position in wordRE.FindAllString(raw, -1)
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

// fixByFrom maps a whitelisted raw word to its own substitution entry --
// same table, same single source as knownVariant and applyFixes's own
// per-call byFrom, built once here so VerifyEdits can recompute the exact
// case-adapted replacement text a position should hold (round five,
// agent-estate#1405: knownVariant alone only proves the replacement's
// WORDS are vetted, tokenized; it cannot check the exact BYTES a To field
// claims, which is what the byte-level check below needs).
var fixByFrom = func() map[string]substitution {
	m := map[string]substitution{}
	for _, s := range allFixes {
		m[s.from] = s
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
// it is ever written -- round five of agent-estate#1405. Rounds one through
// three each failed the same way, one level further in (fuzzy distance,
// then set membership, then a positional walk still trickable by a
// stopword-reachable decoy); round four fixed all three by moving position
// off of clean-side search entirely, onto the generator's own record -- the
// right design, confirmed by round five holding it unchanged -- but its two
// checks compared TOKENS (tokenize(), [A-Za-z']+ only, lowercased) where
// the design names BYTES. Round five did not change what is being checked,
// only the representation the checking happens in. See this file's own
// header comment for the full round-by-round history.
//
// Two checks, both TOTAL -- no distance, no set, no bounded search, no
// tokenize(), no "close enough" -- either refuses the whole row:
//
//  1. Every edit must be a genuine, currently-whitelisted allFixes entry,
//     BYTE-exact: the raw token it names must match what's actually at that
//     position in raw, and its recorded replacement must equal, byte for
//     byte, matchCase(this position's own original-case raw token, this
//     fix's vetted text) -- the exact computation applyFixes performs when
//     it generates a real edit, independently recomputed here rather than
//     trusted. An edit naming anything else -- an unvetted substitution, a
//     claim about a token that isn't actually there, or a To field carrying
//     even one extra or different byte the whitelist never vetted (a
//     smuggled digit, an extra punctuation mark, different case) -- is
//     refused outright, whatever the rest of the pair looks like. This is
//     what keeps the whitelist the sole authority over every byte written,
//     not just its letters: an edit list is not a side channel around it.
//  2. Replaying every edit against raw's own bytes, at the exact position
//     it names, must reproduce clean EXACTLY, byte for byte -- no
//     tokenize() on either side of this comparison. A raw byte-run no edit
//     names must survive verbatim, including its own case, punctuation and
//     surrounding whitespace; a named token must have become exactly its
//     recorded replacement. There is no scan of clean for something
//     acceptable: position comes entirely from the edit list the generator
//     already recorded, so nothing elsewhere in the sentence -- a
//     coincidental decoy, a stopword run, an unrelated real word, a
//     de-shouted ALL-CAPS run, a changed digit, a flipped punctuation mark
//     -- can vouch for a mismatch at the position that actually matters.
//     Either clean holds what the edits say happened, byte for byte, or it
//     does not.
//
// Does the verifier still earn its place, now that the generator hands it
// the very edits it made? Yes, and with byte-level replay the argument gets
// STRONGER, not weaker: the check becomes "the edit list alone reconstructs
// clean from raw", which catches a generator writing anything it did not
// record. Not tautological either way, because applyFixes and VerifyEdits
// share the edit list but not the string: applyFixes builds `clean` in one
// pass, substituting as it walks; VerifyEdits takes only the resulting Edit
// list plus raw and independently reconstructs what clean must be, then
// compares that reconstruction to the actual `clean` string with `==`. A
// bug in how applyFixes joins a replacement back into the string -- wrong
// case-adaptation applied at the string-join step but not reflected in the
// Edit it recorded, an off-by-one in which occurrence a regex match
// touched, a stray extra byte -- changes the STRING without changing the
// RECORD, and byte-level replay catches exactly that divergence, more
// completely than token-level replay could (nothing non-alphabetic was ever
// invisible to it in the first place). What it does NOT catch, and cannot:
// a generator that correctly records and correctly applies an edit that was
// never the right decision to make in the first place (check 1 bounds that
// instead, via the whitelist) -- and a hand-authored (raw, edits, clean)
// triple that lies about all three consistently, which no verifier
// operating on the generator's own output can ever detect, because at that
// point it is not checking generated output at all. Both are named, not
// hidden: this gate verifies EXECUTION against RECORD and RECORD against
// WHITELIST; it was never, in any of its five versions, capable of
// verifying DECISION against intent that lives only in Jon's head.
//
// Retired from this check, argued in this file's own header comment: the
// old negation-count and length-ratio backstops. Exact byte-level replay
// subsumes both, more completely than round four's token-level version did
// -- there is no way for a token to vanish, invert, or appear from nowhere,
// and no way for a byte outside [A-Za-z'] to change unnoticed either,
// without the string-equality comparison failing first.
func VerifyEdits(raw string, edits []Edit, clean string) VerifyResult {
	// ONE walk, ONE index space: wordRE.FindAllString(raw, -1) is the single
	// match set both checks below index into -- rawTokens is derived from
	// it (one strings.ToLower per already-matched element), never from a
	// second, independent match over a separately-lowered string. Round
	// five's own tokenize(raw) (which lowers the WHOLE string, then
	// matches) could disagree with this exact walk: strings.ToLower
	// expands U+0130 (İ) into ASCII "i" + a combining mark and maps U+212A
	// (the Kelvin sign) to ASCII "k" -- neither character matches
	// [A-Za-z']+ on its own, so lowering the whole string before matching
	// invents a token ("i" or "k") that was never there in the original,
	// and every index after it drifts by one. Lowering each element of
	// rawTokensOriginal individually cannot do this: wordRE only ever
	// matches ASCII a-z/A-Z/', and strings.ToLower is a 1:1, non-expanding
	// map for every ASCII letter -- there is no character that can appear
	// *inside* an already-matched token and still expand under lowering.
	// So the property this needs (the whitelist check and the replay agree
	// on where every token is) is structural, not asserted: there is only
	// one regex walk over raw anywhere in this function.
	rawTokensOriginal := wordRE.FindAllString(raw, -1)
	rawTokens := make([]string, len(rawTokensOriginal))
	for i, t := range rawTokensOriginal {
		rawTokens[i] = strings.ToLower(t)
	}
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
		sub, ok := fixByFrom[e.From]
		if !ok {
			return VerifyResult{false, fmt.Sprintf("edit %q -> %q is not a whitelisted allFixes entry", e.From, e.To)}
		}
		// BYTE-exact, not token-exact: the whitelist's authority is over
		// every byte of the replacement text actually written, not just its
		// letters. tokenize(e.To) == tokenize(vetted) (the old check) strips
		// digits, punctuation and everything non-[A-Za-z'] BEFORE comparing,
		// so an edit could claim To="don't 100" or To="DON'T!!!" for a
		// vetted "don't" and pass -- smuggled bytes that never went near the
		// whitelist. Recomputing the exact case-adapted string this word's
		// own position should have produced, and requiring To to equal it
		// exactly, closes that: nothing can ride into clean on an edit's To
		// field that matchCase(this raw token, this fix's vetted text) did
		// not itself produce.
		expected := matchCase(rawTokensOriginal[e.TokenIndex], sub.to)
		if e.To != expected {
			return VerifyResult{false, fmt.Sprintf("edit %q -> %q does not match allFixes's own case-adapted replacement %q for this position's casing", e.From, e.To, expected)}
		}
		byIndex[e.TokenIndex] = e
	}

	// BYTE-level replay: walk raw's own wordRE matches in the SAME order
	// applyFixes walks them at generation time (wordRE.ReplaceAllStringFunc,
	// one counter increment per match -- the exact correspondence this
	// file's Edit doc comment establishes), substituting each recorded
	// edit's exact To bytes at its recorded index and leaving every other
	// token exactly as raw wrote it. Every non-word byte -- whitespace,
	// punctuation, digits, symbols, non-ASCII -- is never touched by
	// ReplaceAllStringFunc outside a match, so it round-trips automatically;
	// there is no tokenize() anywhere in this comparison, so nothing outside
	// [A-Za-z'] can be invisible to it the way it was through round four's
	// token-sequence comparison.
	idx := -1
	want := wordRE.ReplaceAllStringFunc(raw, func(tok string) string {
		idx++
		if e, ok := byIndex[idx]; ok {
			return e.To
		}
		return tok
	})
	if want != clean {
		return VerifyResult{false, fmt.Sprintf("replaying %d edit(s) against raw's own bytes yields %q, but clean is %q -- clean does not match what the recorded edits explain", len(edits), want, clean)}
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
