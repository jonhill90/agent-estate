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
// independent meaning-preservation check before it is trusted.

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

// applyFixes runs every whitelisted substitution once, whole-word only,
// case-adapted. Returns the result and exactly which fixes fired (empty if
// none did -- the caller decides what an unchanged result means).
func applyFixes(raw string) (string, []string) {
	byFrom := map[string]substitution{}
	for _, s := range allFixes {
		byFrom[s.from] = s
	}
	var applied []string
	out := wordRE.ReplaceAllStringFunc(raw, func(tok string) string {
		lower := strings.ToLower(tok)
		if s, ok := byFrom[lower]; ok {
			applied = append(applied, fmt.Sprintf("%s -> %s (%s)", tok, matchCase(tok, s.to), s.reason))
			return matchCase(tok, s.to)
		}
		return tok
	})
	sort.Strings(applied)
	return out, applied
}

// stopwords are excluded from the content-word preservation check -- function
// words whose presence/absence a spelling-and-grammar pass may legitimately
// shift (an inserted "a", a dropped duplicate "the"), as opposed to content
// words, which must survive intact or the change is not a spelling fix.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(
		"a an the and or but if then so to of in on at for with as is are was were " +
			"be been being do does did have has had i you he she it we they me him her " +
			"us them my your his its our their this that these those not no",
	) {
		stopwords[w] = true
	}
}

var negationWords = map[string]bool{
	"not": true, "no": true, "never": true, "none": true, "nothing": true,
	"cannot": true, "cant": true, "wont": true, "dont": true, "doesnt": true,
	"didnt": true, "isnt": true, "wasnt": true, "werent": true, "wouldnt": true,
	"shouldnt": true, "couldnt": true, "hasnt": true, "havent": true, "nobody": true,
	"neither": true, "nor": true, "n't": true,
}

func tokenize(s string) []string {
	return wordRE.FindAllString(strings.ToLower(s), -1)
}

func negationCount(s string) int {
	n := 0
	for _, w := range tokenize(s) {
		if negationWords[w] || strings.HasSuffix(w, "n't") {
			n++
		}
	}
	return n
}

// knownVariant maps a raw word this file KNOWS how to correct (the `from`
// side of an entry in `allFixes`, lowercase) to its own vetted replacement,
// tokenized. Built from `allFixes` directly -- the SAME table
// `applyFixes` reads -- so this can never drift out of sync with the
// generator: widening the whitelist (adding an entry to `contractionFixes`/
// `typoFixes`/`productNameFixes`) widens what the verifier recognises as a
// legitimate word-level variant in the exact same commit, automatically.
var knownVariant = func() map[string][]string {
	m := map[string][]string{}
	for _, s := range allFixes {
		m[s.from] = tokenize(s.to)
	}
	return m
}()

// alignContentWords is PR #1405's third pass at this check, after two
// rounds both failed on the same underlying shape: set membership standing
// in for position.
//
//  1. round one: `wordSurvives` asked whether SOME word anywhere in clean
//     sat within a generic edit-distance band of w. "ship"->"skip" passed --
//     geometrically indistinguishable from "teh"->"the" to a bare distance
//     threshold.
//  2. round two: anchored to the SPECIFIC vetted word (knownVariant) instead
//     of a distance band, but still asked whether that word's tokens were
//     present anywhere in the clean token SET. "run teh tests then deploy
//     the app" -> "run all tests then deploy the app" passed: "the" is
//     vetted for "teh", "the" appears in "the app", so it vouched for a
//     substitution it had nothing to do with. Any typo whose vetted
//     replacement is a common function word could be swapped for anything
//     and still "survive".
//
// The pattern across both failures is the same: neither check has any
// notion of WHERE in the sentence a word is allowed to have changed, only
// whether an acceptable-looking token exists somewhere. A generic distance
// threshold and a set-membership lookup are both instances of "search the
// whole clean text for something that looks right" -- exactly the shape
// that keeps failing, regardless of what "looks right" means.
//
// The generator (applyFixes) does not have this problem: it walks raw
// left to right and knows, for each token, whether it changed and to
// what. Re-deriving that from two finished strings is solving a strictly
// harder problem than the generator already solved, and throwing away
// information it had. This function does not take an edit list from the
// generator (that would mean changing VerifyMeaningPreserved's signature,
// and every existing and prior-round test that hand-constructs a (raw,
// clean) pair to attack this gate), but it stops SEARCHING the clean text
// and starts WALKING it instead: a single left-to-right pass over raw's
// content words, tracking a cursor into clean's own tokens that only ever
// moves forward. A raw word is accepted only if, starting from where the
// walk currently sits, clean holds it verbatim OR holds its one specific
// vetted replacement (knownVariant), immediately (skipping only stopwords
// clean may have legitimately inserted first). A word that "survives"
// later in the sentence, or that only the WRONG position could explain,
// no longer counts -- there is no set left to search.
//
// This closes the class, not the instance: multiset counting (count(to) >=
// count(from) + pre-existing count(to)) was the other fix on the table and
// is also defeated by a decoy -- pad the sentence with one extra
// coincidental "the" and a count-only check is satisfied again. Position is
// the only thing neither a distance band nor a count can fake.
//
// As a consequence, not a separate feature: this also closes most of what
// PR #1405's second review named as a non-blocking, unchecked limit -- an
// ADDED content word ("commit before continuing" -> "always commit before
// continuing") that the old set-membership check never noticed at all. A
// leading or interior addition breaks the walk the same way a substitution
// does, because everything after it stops finding its match at the
// position the walk expects. A purely TRAILING addition (after every raw
// content word has already matched) is not caught by the walk itself --
// there is nothing left to misalign -- so it is checked separately, once,
// after the walk: any non-stopword token left over in clean past where the
// last match landed is unaccounted-for content, and refuses the same way a
// missing word does. Between the two, no content word may be added,
// dropped, or moved without being named -- applyFixes itself never adds a
// content word at all (a substitution's `to` is a vetted replacement, never
// an insertion), so this costs nothing against the real generator; it only
// closes a gap a hand-constructed or future generator could otherwise slip
// through.
func alignContentWords(rawTokens, cleanTokens []string) (missing []string) {
	j := 0
	for _, w := range rawTokens {
		if stopwords[w] {
			continue
		}
		matched := false
		for j < len(cleanTokens) {
			if cleanTokens[j] == w {
				j++
				matched = true
				break
			}
			if variant, ok := knownVariant[w]; ok && j+len(variant) <= len(cleanTokens) {
				same := true
				for k, part := range variant {
					if cleanTokens[j+k] != part {
						same = false
						break
					}
				}
				if same {
					j += len(variant)
					matched = true
					break
				}
			}
			if !stopwords[cleanTokens[j]] {
				break
			}
			j++ // a stopword clean may have legitimately inserted; keep looking
		}
		if !matched {
			missing = append(missing, w)
		}
	}
	for _, extra := range cleanTokens[j:] {
		if !stopwords[extra] {
			missing = append(missing, fmt.Sprintf("(added) %s", extra))
		}
	}
	return missing
}

// VerifyResult is the outcome of checking one proposed (raw, clean) pair.
type VerifyResult struct {
	OK     bool
	Reason string // set when OK is false
}

// VerifyMeaningPreserved is the independent gate every proposed clean must
// pass before it is ever written, regardless of how it was generated --
// defence in depth, not a restatement of what applyFixes already does by
// construction. Three checks, any one failing refuses the whole row:
//
//  1. Every content word (non-stopword) in raw survives in clean, at the
//     position the walk has reached, verbatim or as the SPECIFIC, vetted
//     replacement this file's own whitelist names for it -- never a word
//     that merely appears somewhere else in the sentence (see
//     alignContentWords's own doc comment for why two prior shapes of this
//     check both failed exactly that way, and why a positional walk closes
//     the class rather than the instance). This also catches the
//     "garbage" -> "something broken" shape found live in this corpus's
//     own existing text_clean population (see this file's own header
//     comment) -- a word dropped or replaced with something not in the
//     whitelist is refused either way.
//  2. Negation count is unchanged -- a dropped or added "not"/"never"/-n't
//     inverts meaning outright and must never pass silently.
//  3. Length stays within a generous band (0.6x-1.6x by character count) --
//     a cheap backstop against wholesale drops or additions the first two
//     checks were not built to catch directly.
func VerifyMeaningPreserved(raw, clean string) VerifyResult {
	rawTokens := tokenize(raw)
	cleanTokens := tokenize(clean)
	missing := alignContentWords(rawTokens, cleanTokens)
	if len(missing) > 0 {
		return VerifyResult{false, fmt.Sprintf("content word(s) not found in clean text: %s", strings.Join(missing, ", "))}
	}
	if rn, cn := negationCount(raw), negationCount(clean); rn != cn {
		return VerifyResult{false, fmt.Sprintf("negation count changed: raw has %d, clean has %d", rn, cn)}
	}
	if len(raw) > 0 {
		ratio := float64(len(clean)) / float64(len(raw))
		if ratio < 0.6 || ratio > 1.6 {
			return VerifyResult{false, fmt.Sprintf("length ratio %.2f outside 0.6-1.6 band (raw %d chars, clean %d chars)", ratio, len(raw), len(clean))}
		}
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
// returned once VerifyMeaningPreserved has passed against the specific
// (raw, clean) pair actually proposed. ActionIdentity is the "nothing in
// the whitelist matched" case -- raw is copied verbatim (a no-op change
// cannot fail meaning-preservation by construction, and CLAUDE.local.md's
// own rule ("if text_clean is null for a prompt worth quoting, clean it and
// write it back first") reads as "make it quotable", not "guarantee it is
// typo-free" -- a prompt with no whitelisted-typo match is not thereby
// unclean, it is simply outside what this tool can improve).
func ProposeClean(promptID, raw string) CleanProposal {
	clean, changes := applyFixes(raw)
	if len(changes) == 0 {
		return CleanProposal{PromptID: promptID, Raw: raw, Clean: raw, Action: ActionIdentity}
	}
	v := VerifyMeaningPreserved(raw, clean)
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
