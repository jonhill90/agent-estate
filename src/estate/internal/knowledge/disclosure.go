package knowledge

// Provenance-disclosure state -- agent-estate#1061.
//
// `get` has printed a bare prompt_id for every corpus-sourced item since
// #1031, and that reads as "provenance is available" for every one of
// them. Measured against the real corpus (2026-09-04): 1,174 distinct
// prompts stand behind a hard item, and only 141 of those (12%) carry a
// non-empty text_clean -- the ONLY field the operator's rules permit
// quoting (text_raw must never be published, see corpus.go's own doc
// comment). Following a prompt_id therefore leads to nothing quotable
// 88% of the time, and a bare id cannot tell a caller which case it is in.
//
// #1061's own comment thread rejected building a walker over this --
// "a polished reader over content absent 88% of the time industrialises a
// dead end" -- and asked instead for the state to be made explicit, the
// same shape #1058's Coverage taxonomy already uses for a different
// absence. ResolveDisclosure is that state: it reports whether a caller
// COULD open inspectable evidence for one item's prompt_id, never the
// evidence itself.
//
// NEVER THE TEXT. This file has no code path that returns text_clean (or
// text_raw) to a caller -- only whether text_clean exists, is reachable
// under classify.go's own publish boundary, or the lineage that would
// carry it is broken. See Disclosure's own doc comment for the four
// states and DisclosureState's doc comments for what evidence puts an
// item in each one.
import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// DisclosureState is the taxonomy #1061 asks for -- distinct from
// Coverage (query.go), which is about the compiled index as a whole;
// this is about ONE item's own prompt_id.
type DisclosureState string

const (
	// DisclosureAvailableClean means the corpus's own prompts row for
	// this item's PromptID exists, carries a non-empty text_clean, and
	// the item itself is Publishable -- a caller MAY open clean source
	// text for this item. This package never opens it FOR the caller;
	// see this file's own "never the text" note.
	DisclosureAvailableClean DisclosureState = "available_clean"
	// DisclosureUnavailable means the prompts row exists but its
	// text_clean is empty -- the far more common case (88% of hard
	// items' prompts, measured 2026-09-04). No clean source text exists
	// to open; this is a legitimate, honest answer, not a failure.
	DisclosureUnavailable DisclosureState = "unavailable"
	// DisclosureRestricted means the prompts row exists -- and may even
	// carry a non-empty text_clean -- but this item's own Publishable is
	// false AND this call did not unlock private material, so this
	// caller/scope may not receive it. Wired through `get`'s own
	// includePrivate flag, mirroring exactly the boundary Get and Query
	// already enforce (agent-estate#1033) rather than inventing a second
	// one: EVERY corpus-* source defaults to private (classify.go's
	// default branch -- no corpus source is in its explicit-public
	// table), so today's `get` never even reaches this function without
	// --private (Get refuses the whole item first, see query.go's Get) --
	// this state is what that refusal would have been, made available to
	// a caller that resolves disclosure directly, with its own scope,
	// rather than through Get's blanket item-level gate.
	DisclosureRestricted DisclosureState = "restricted"
	// DisclosureSourceMissing means this item's own PromptID does not
	// resolve to any row in the corpus's prompts table at all -- a
	// broken lineage, not an absence of cleaning. Distinct from
	// DisclosureUnavailable on purpose: one says "the source exists and
	// was never cleaned", the other says "there is no source to find".
	DisclosureSourceMissing DisclosureState = "source_missing"
)

// Disclosure is ResolveDisclosure's typed answer -- State plus one
// human-readable Detail naming the evidence behind it. Never carries
// text_clean or text_raw; see this file's own "never the text" note.
type Disclosure struct {
	State  DisclosureState `json:"state"`
	Detail string          `json:"detail"`
}

// promptIDPattern is the corpus's own prompts.id shape -- a two-letter
// prefix ("hp"/"mp" observed against the real corpus, 2026-09-04),
// a hyphen, then 16 lowercase hex characters. Checked before promptID is
// ever interpolated into a SQL string below, the same "validate the fixed
// shape before embedding" discipline corpus.go's corpusKinds comment
// already documents for its own literal-in-query case -- promptID here
// comes from this package's own compiled index rather than a literal, so
// it is checked rather than assumed.
var promptIDPattern = regexp.MustCompile(`^[a-z]{2}-[0-9a-f]{16}$`)

// ResolveDisclosure reports which of the four DisclosureState values
// applies to one item's own PromptID, by querying the corpus's prompts
// table for that id's text_clean -- never reading text_raw, and never
// returning any prompt text to the caller (see this file's own doc
// comment). includePrivate is the same flag Query/Get already take
// (agent-estate#1033): DisclosureRestricted only applies when the item is
// private AND this call did not unlock private material -- a caller that
// already unlocked it (e.g. `get --private`, the only way today's `get`
// ever reaches an item whose own corpus-* source defaults to private,
// see classify.go) gets the real content-based answer instead. err is
// non-nil only when the check itself could not be performed (no corpus
// path configured, corpus unreadable, or the query failed) -- a caller
// reporting err must say "could not check", never invent one of the four
// states from an incomplete read, the same "report, never guess" rule
// CoverageUnknownFreshness already follows for a different unreadable
// source.
//
// Callers should only call this when item.PromptID != "" -- the same
// gate main.go's own prompt_id print line already applies (PromptID is
// empty for the four non-corpus sources, which have no prompt lineage to
// resolve at all).
func ResolveDisclosure(dbPath string, item Item, includePrivate bool) (Disclosure, error) {
	if item.PromptID == "" {
		return Disclosure{}, fmt.Errorf("item %s carries no prompt_id -- disclosure does not apply", item.ID)
	}
	found, textClean, err := queryPromptClean(dbPath, item.PromptID)
	if err != nil {
		return Disclosure{}, err
	}
	if !found {
		return Disclosure{
			State:  DisclosureSourceMissing,
			Detail: fmt.Sprintf("prompt_id %s does not resolve to any row in the corpus's prompts table", item.PromptID),
		}, nil
	}
	// Scope is checked before content: an item this call has not
	// unlocked answers "restricted" regardless of whether text_clean
	// happens to be populated -- the caller may not receive it either
	// way, and DisclosureRestricted's own doc comment is what should
	// tell them why.
	if !item.Publishable && !includePrivate {
		return Disclosure{
			State:  DisclosureRestricted,
			Detail: fmt.Sprintf("item is private (%s) and this call did not include --private -- clean source text is not disclosed", item.PublishBasis),
		}, nil
	}
	if strings.TrimSpace(textClean) == "" {
		return Disclosure{
			State:  DisclosureUnavailable,
			Detail: "the prompt behind this item has no text_clean recorded -- the source exists but has never been cleaned for quoting",
		}, nil
	}
	return Disclosure{
		State:  DisclosureAvailableClean,
		Detail: "text_clean exists for this item's prompt -- not printed by this call",
	}, nil
}

// queryPromptClean looks up one prompts row by id, returning found=false
// (not an error) when no such row exists -- the corpus itself is the
// source of truth for "does this id resolve to anything", not this
// function inventing a distinction between "empty" and "absent" beyond
// what sqlite3 actually reports. Opened read-only/immutable, the same
// mode corpus.go's own corpusSource already uses.
func queryPromptClean(dbPath, promptID string) (found bool, textClean string, err error) {
	if dbPath == "" {
		return false, "", fmt.Errorf("no corpus path configured")
	}
	if _, err := os.Stat(dbPath); err != nil {
		return false, "", fmt.Errorf("corpus unreadable at %s: %w", dbPath, err)
	}
	if !promptIDPattern.MatchString(promptID) {
		return false, "", fmt.Errorf("malformed prompt id %q", promptID)
	}
	q := fmt.Sprintf(`select coalesce(text_clean,'') from prompts where id='%s'`, promptID)
	cmd := exec.Command("sqlite3", "file:"+dbPath+"?mode=ro&immutable=1", q)
	out, err := cmd.Output()
	if err != nil {
		return false, "", fmt.Errorf("corpus query failed: %w", err)
	}
	if len(out) == 0 {
		return false, "", nil
	}
	// sqlite3's default list mode prints exactly one line per row, with a
	// trailing newline -- a matched row (even one whose text_clean is
	// coalesced to "") still produces that one line, so trimming it and
	// nothing else preserves the "found" signal that len(out) == 0 above
	// already keys off of.
	return true, strings.TrimRight(string(out), "\n"), nil
}
