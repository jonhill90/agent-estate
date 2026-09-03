// Package tick keeps the Director's own tick record and answers whether the
// loop has stalled.
//
// WHY THIS IS CODE AND NOT A HABIT. The Director's brief (docs/director-brief.md
// section 3) defines a stop condition: three consecutive ticks sharing the same
// phase item and the same src head with no artifact means the loop is running
// and producing nothing, and must escalate rather than continue. Every tick is
// a fresh context with no memory of the last one, so a stop condition that
// depends on an agent remembering to append a line -- and remembering to read
// the last three back -- is a sentence, not a guard. The brief said as much
// about itself and then shipped without the mechanism. This is the mechanism.
//
// Absence is typed here, as everywhere in this tree: an artifact that is
// missing serialises as null and compares equal to the empty string, so a tick
// cannot dodge the stop condition by recording "" and calling it output.
package tick

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"time"
)

// DefaultPath is where the brief says the record lives, relative to the repo
// root. ESTATE_TICK_LOG overrides it; Path applies that rule in one place.
const DefaultPath = "docs/tick-log.jsonl"

// Path returns the tick log to use: ESTATE_TICK_LOG when set, else DefaultPath.
func Path() string {
	if p := os.Getenv("ESTATE_TICK_LOG"); p != "" {
		return p
	}
	return DefaultPath
}

// DefaultEscalationPath is where an escalation is recorded, relative to the
// repo root. ESTATE_TICK_ESCALATION_LOG overrides it.
//
// This is a SEPARATE file from the tick log, deliberately. See
// RecordEscalation's doc comment for why an escalation must never share a
// file, or a field, with the artifact record the stop condition reads.
const DefaultEscalationPath = "docs/tick-escalations.jsonl"

// EscalationPath returns the escalation log to use: ESTATE_TICK_ESCALATION_LOG
// when set, else DefaultEscalationPath.
func EscalationPath() string {
	if p := os.Getenv("ESTATE_TICK_ESCALATION_LOG"); p != "" {
		return p
	}
	return DefaultEscalationPath
}

// Entry is one tick, in the shape section 3 of the brief specifies.
type Entry struct {
	At        time.Time `json:"-"`
	AtText    string    `json:"at"`
	PhaseItem string    `json:"phase_item"`
	SrcHead   string    `json:"src_head"`
	// Artifact is what a human can look at as a result of this tick. Empty
	// means there was none, and serialises as null -- the stop condition is
	// written against that spelling.
	Artifact string `json:"artifact"`

	// GapSeconds is the wall-clock gap since the PREVIOUS tick's own `at`,
	// nothing more. It is cron cadence, not work duration: a tick that did
	// nothing and a tick that did everything both show roughly the same
	// gap, because the Director's loop runs on a fixed interval regardless
	// of how much a tick actually did (see docs/director-loop.md). Do not
	// read this as effort, and do not rename it to "duration" -- that name
	// was considered and rejected for exactly this reason (agent-estate#982).
	// Nil on the first tick this log has ever recorded, when there is no
	// previous `at` to measure from -- absent, not zero, the same
	// *float64/*int64 pattern ledger.Record.SpendCostUSD uses and for the
	// same reason: a zero here would read as "no time passed" instead of
	// "nothing to compare against."
	GapSeconds *int64 `json:"gap_seconds,omitempty"`

	// ObservedTurns is how many tasks had their outcome become known
	// (reached a terminal ledger state) in the window between the previous
	// tick and this one (strictly after the previous tick's `at`, no later
	// than this tick's own `at`) -- see spend.WindowedByObservation. It is
	// the denominator ObservedSpendUSD needs: a reader must be able to tell
	// "$0 because nothing finished this window" from "$0 because nothing
	// that finished reports a dollar figure" from "genuinely free." Nil
	// only when there was no previous tick to bound a window against (the
	// first tick ever recorded); zero is a legitimate, distinct value
	// meaning the window is real and nothing finished in it.
	//
	// NAMED "observed", NOT "dispatched" (agent-estate#989, correcting
	// #982's own original naming and keying). A task is windowed by WHEN
	// ITS OUTCOME BECAME KNOWN, not when it was launched: keying on launch
	// time meant a task dispatched in window N that did not finish until a
	// later window had its cost silently and permanently dropped, since its
	// dispatch instant was already behind every later window's own `since`
	// by the time it finished. Keying on the terminal record's own At
	// instead means a task's cost lands in exactly one window, whichever
	// one it actually finished in, however long it ran -- see
	// spend.WindowedByObservation's doc comment for the full reasoning.
	ObservedTurns *int64 `json:"observed_turns,omitempty"`

	// ObservedSpendUSD is observed spend ONLY: the sum of
	// ledger.Record.SpendCostUSD for tasks counted in ObservedTurns, for
	// harnesses that report a dollar figure at all (see
	// docs/spend-observation.md -- claude does, codex as of this writing
	// never does). It is NEVER the cost of this tick and must never be
	// read, printed, or compared as one: the Director itself runs as a cron
	// inside Claude Code, the estate does not dispatch it, and no harness
	// result envelope for the Director's own turn ever reaches the ledger.
	// That gap is structural, not a coverage hole this field will ever
	// close. Nil when ObservedTurns is nil (no window to measure), or when
	// ObservedTurns is non-nil but none of those turns reported a cost --
	// summing zero non-nil dollar figures into $0.00 would read as "this
	// window was free," which #979 already refused for the cross-harness
	// case and this is the same defect. Every caller that prints this field
	// must also print ObservedTurnsWithCost (NOT ObservedTurns -- see that
	// field's own doc comment for why the two are not interchangeable) and
	// name what is excluded -- see spend.WindowedByObservation's doc comment.
	ObservedSpendUSD *float64 `json:"observed_spend_usd,omitempty"`

	// ObservedTurnsWithCost is how many of ObservedTurns actually reported a
	// dollar figure -- spend.WindowedByObservation's own turnsWithCost return,
	// carried onto the entry instead of being discarded after one boolean
	// gate check (agent-estate#995, correcting a defect #989's fix pass
	// introduced: the dollar line printed ObservedTurns -- the window's
	// TOTAL observed turns -- while claiming it was the count "that reported
	// a cost"; only ObservedTurnsWithCost is that count). ObservedTurns and
	// ObservedTurnsWithCost diverge exactly when a window has a mix of
	// harnesses that do and do not report a dollar figure (e.g. one claude
	// turn with cost, one codex turn with none) -- printing ObservedTurns
	// there overstates how many turns the dollar figure actually covers.
	// Every write path here sets this field and ObservedSpendUSD together, so
	// on a log this code wrote, one is nil exactly when the other is. That is
	// a property of the writer, NOT a guarantee a reader may lean on: an
	// entry read back off disk may have been hand-edited, partially migrated,
	// or written by a future path that sets one and forgets the other, and
	// dereferencing this field on the strength of ObservedSpendUSD being
	// non-nil panicked `tick check` when exactly that happened
	// (agent-estate#997). Read these fields through ReadSpend, which
	// classifies every combination including the incoherent ones, rather than
	// testing one pointer and dereferencing the other.
	ObservedTurnsWithCost *int64 `json:"observed_turns_with_cost,omitempty"`
}

// MarshalJSON writes At as ISO 8601 UTC and an absent Artifact as null.
func (e Entry) MarshalJSON() ([]byte, error) {
	type wire struct {
		At                    string   `json:"at"`
		PhaseItem             string   `json:"phase_item"`
		SrcHead               string   `json:"src_head"`
		Artifact              *string  `json:"artifact"`
		GapSeconds            *int64   `json:"gap_seconds,omitempty"`
		ObservedTurns         *int64   `json:"observed_turns,omitempty"`
		ObservedSpendUSD      *float64 `json:"observed_spend_usd,omitempty"`
		ObservedTurnsWithCost *int64   `json:"observed_turns_with_cost,omitempty"`
	}
	w := wire{
		At:                    e.At.UTC().Format(time.RFC3339),
		PhaseItem:             e.PhaseItem,
		SrcHead:               e.SrcHead,
		GapSeconds:            e.GapSeconds,
		ObservedTurns:         e.ObservedTurns,
		ObservedSpendUSD:      e.ObservedSpendUSD,
		ObservedTurnsWithCost: e.ObservedTurnsWithCost,
	}
	if e.AtText != "" {
		w.At = e.AtText
	}
	if e.Artifact != "" {
		a := e.Artifact
		w.Artifact = &a
	}
	return json.Marshal(w)
}

// Verdict is the answer Check gives. Stalled false with a nil error means the
// loop is moving; an error means we could not tell, which is never the same
// thing as clean.
type Verdict struct {
	Stalled bool
	Reason  string
	// Considered is how many entries the verdict was drawn from, so a caller
	// can say "not stalled, but only one tick on record" rather than implying
	// a healthy history it never saw.
	Considered int
	// Unverifiable is how many artifacts in the window this verdict was
	// drawn from could not be resolved by CheckWithResolver -- a network
	// call failed outright, gh was unreachable, a timeout. It is zero when
	// Check (no resolver) was used, or every artifact in the window resolved
	// one way or the other.
	//
	// This is the typed third state agent-estate#931 asks for. It must never
	// be read as "these artifacts are confirmed real" (that repeats the
	// defect this package exists to fix -- a fabricated artifact accepted
	// because nothing looked) and never as "these artifacts are fake"
	// (that would turn a flaky network into a false report a human has to
	// untangle from a genuine one). An unverifiable artifact does not clear
	// the stall on its own -- see CheckWithResolver's doc comment for why
	// that is the fail-closed direction this repo already takes everywhere
	// else (Validate's produced callback, pressure.Check, cost.Figure.Known).
	Unverifiable int
	// Escalated is true when the CURRENT stall (the same phase item and src
	// head as the most recent tick) has a matching escalation on record,
	// timestamped at or after that tick -- see CheckWithEscalation. It is
	// never true unless Stalled is also true: an escalation acknowledges a
	// stall, it does not create or clear one, and a stall with no matching
	// escalation is Escalated=false however many unrelated escalations sit
	// in the log.
	Escalated bool
	// EscalatedAt is when the most recent acknowledging escalation was
	// recorded. Zero when Escalated is false.
	EscalatedAt time.Time
	// EscalationCount is how many escalations on record match the current
	// stall's (phase item, src head) pair, fresh or not. A loop that
	// escalates the SAME stall repeatedly -- because it is still stuck, not
	// because it moved on -- must be visibly different from one that
	// escalated once and is now waiting; this is that difference. Zero when
	// Escalated is false.
	EscalationCount int
}

// Window is how many consecutive entries the stop condition looks at.
const Window = 3

// Resolution is the answer resolving one artifact token gives.
//
// WHY A THIRD STATE. agent-estate#931: the Director recorded
// ".../pull/926#issuecomment-latest" and, in the same session,
// ".../issues/940#issuecomment-5523579200" -- a plausible 10-digit comment id
// that simply does not exist (the real one was 5523608412). Both passed
// Validate, because a URL is accepted on shape alone (see the "://" branch
// below) and shape cannot tell a fabricated id from a real one. Only asking
// the source -- a real request, a real status -- can. But a real request can
// also fail to complete for reasons that have nothing to do with whether the
// artifact is real: the network is down, gh is unauthenticated, a request
// times out. Collapsing that failure into either Valid or Invalid would
// re-introduce exactly the kind of false signal this package exists to
// remove, in the opposite direction. So it is its own value.
type Resolution int

const (
	// ResolveUnknown means the check itself could not be made. Never coerced
	// to Valid or Invalid -- see the type's doc comment.
	ResolveUnknown Resolution = iota
	// ResolveValid means the artifact was checked and names something real.
	ResolveValid
	// ResolveInvalid means the artifact was checked and does NOT name
	// something real -- a 404, a comment id that does not exist, a path
	// absent at the recorded src_head.
	ResolveInvalid
)

// String reports the resolution as a word, for log lines and CLI output.
func (r Resolution) String() string {
	switch r {
	case ResolveValid:
		return "valid"
	case ResolveInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// Resolve makes the actual external check for one artifact string, recorded
// against the src_head the tick that wrote it named, and says why in a
// human-readable detail.
//
// It is a function type supplied by the caller -- the same pattern Record's
// produced parameter already uses -- so this package stays a pure reader/
// writer that tests without a network connection, gh, or a git checkout. The
// real implementation (an HTTP request, a `gh api` call, `git cat-file -e`
// against src_head) lives in main.go, next to Record's produced, for the
// same reason produced does: it needs os/exec and the network, and this
// package must not.
type Resolve func(artifact, srcHead string) (Resolution, string)

// Record appends one entry to the log at path, creating it if absent.
//
// produced answers "was this token made since the given time?". Passing nil
// checks the artifact's shape only, without asking.
func Record(path string, e Entry, produced func(tok string, since time.Time) bool) error {
	if e.PhaseItem == "" {
		return errors.New("tick: phase_item is required -- a tick that cannot name what it advanced is the thing this record exists to catch")
	}
	// Refuse the dodge at the point it would be taken, not only when reading
	// the record back. Nothing is written when this fires.
	if err := Validate(e.Artifact, lastTickAt(path), produced); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("tick: encode: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("tick: open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("tick: append to %s: %w", path, err)
	}
	return nil
}

// EscalationEntry records that a stalled tick told a human, without
// pretending that telling counts as output. See RecordEscalation's doc
// comment for why this lives in its own log, never in the tick log itself.
type EscalationEntry struct {
	At        time.Time `json:"-"`
	AtText    string    `json:"at"`
	PhaseItem string    `json:"phase_item"`
	SrcHead   string    `json:"src_head"`
	// Where says who was told, and how -- "telegram", "director-inbox",
	// whatever channel notify.sh used this time. Required: an escalation
	// that names no recipient is not evidence anyone was actually told.
	Where string `json:"where"`
}

// MarshalJSON writes At as ISO 8601 UTC, matching Entry's own convention.
func (e EscalationEntry) MarshalJSON() ([]byte, error) {
	type wire struct {
		At        string `json:"at"`
		PhaseItem string `json:"phase_item"`
		SrcHead   string `json:"src_head"`
		Where     string `json:"where"`
	}
	w := wire{
		At:        e.At.UTC().Format(time.RFC3339),
		PhaseItem: e.PhaseItem,
		SrcHead:   e.SrcHead,
		Where:     e.Where,
	}
	if e.AtText != "" {
		w.At = e.AtText
	}
	return json.Marshal(w)
}

// RecordEscalation appends one escalation to path, creating it if absent.
//
// WHY THIS NEVER TOUCHES THE TICK LOG. agent-estate#923's own workaround --
// and the shape a naive fix would take -- was to write the escalation as a
// tick's "artifact". That is exactly the dodge the artifact rule exists to
// catch: a token containing "://" (a Telegram link, a director-inbox link)
// passes Validate on shape alone, with no recency check at all, so a
// stalled loop could escalate three ticks running and clear the
// repeated-artifact rule -- a seventh way to defeat it, next to the six
// already on record in internal/tick's own package doc. Keeping the
// escalation in its own file, under its own type, with no "artifact" field
// at all, makes that structurally impossible: CheckWithResolver never opens
// this path, and nothing in this package will ever read an EscalationEntry
// as an Entry. Recording one can change what CheckWithEscalation reports
// about an existing stall; it can never, by construction, make Check or
// CheckWithResolver see one that Validate would otherwise have caught.
func RecordEscalation(path string, e EscalationEntry) error {
	if e.PhaseItem == "" {
		return errors.New("tick: escalation phase_item is required -- an escalation that cannot name what it is acknowledging cannot acknowledge anything")
	}
	if strings.TrimSpace(e.Where) == "" {
		return errors.New("tick: escalation must say who was told -- an escalation naming no recipient is not evidence anyone was")
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("tick: encode escalation: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("tick: open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("tick: append to %s: %w", path, err)
	}
	return nil
}

type parsedEscalation struct {
	At        string `json:"at"`
	PhaseItem string `json:"phase_item"`
	SrcHead   string `json:"src_head"`
	Where     string `json:"where"`
}

// readEscalations returns every escalation in path, in file order. A
// missing file is zero entries, not an error -- a loop that has never
// escalated has not failed to.
func readEscalations(path string) ([]parsedEscalation, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("tick: open %s: %w", path, err)
	}
	defer f.Close()
	var out []parsedEscalation
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var p parsedEscalation
		if err := json.Unmarshal([]byte(text), &p); err != nil {
			return nil, fmt.Errorf("tick: %s line %d is not readable: %w", path, n, err)
		}
		out = append(out, p)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("tick: read %s: %w", path, err)
	}
	return out, nil
}

type parsed struct {
	PhaseItem string  `json:"phase_item"`
	SrcHead   string  `json:"src_head"`
	Artifact  *string `json:"artifact"`
}

// placeholders are absence spelled as presence. An independent review showed
// the original check -- "any non-empty string" -- let a spinning loop dodge
// the stop condition forever by writing junk into the artifact field, and the
// agent writing that string is the same one the guard exists to catch. The
// literal "null" is the sharpest case: the record format uses null for
// absent, so a tick writing the STRING "null" is claiming output it does not
// have.
var placeholders = map[string]bool{
	"null": true, "nil": true, "none": true, "n/a": true, "na": true,
	"-": true, "--": true, "tbd": true, "nothing": true, "pending": true,
	"in progress": true, "wip": true, "ongoing": true, "unknown": true,
}

// IsPlaceholder reports whether s is a way of writing "no artifact" that
// would otherwise pass as one.
func IsPlaceholder(s string) bool {
	return placeholders[strings.ToLower(strings.TrimSpace(s))]
}

var (
	shaRE   = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	issueRE = regexp.MustCompile(`#\d+`)
	// A path token: either something containing a slash, or a bare filename
	// with an extension (AGENTS.md, go.mod) -- the latter was wrongly refused
	// when only slashes counted.
	pathRE = regexp.MustCompile(`[\w.-]+(?:/[\w.-]+)+|[\w-]+\.[A-Za-z0-9]{1,8}`)
)

// Candidates returns the tokens in s that could name something real: paths,
// commit shas, and issue or PR numbers.
func Candidates(s string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.Trim(t, ".,;:()[]\"'")
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	for _, m := range pathRE.FindAllString(s, -1) {
		add(m)
	}
	for _, m := range shaRE.FindAllString(s, -1) {
		add(m)
	}
	for _, m := range issueRE.FindAllString(s, -1) {
		add(m)
	}
	return out
}

// Validate decides whether an artifact is evidence that THIS tick produced
// something, using produced to ask whether a token post-dates the previous
// tick.
//
// WHY RECENCY AND NOT EXISTENCE. Four rules were tried and an independent
// reviewer defeated each:
//
//	"any non-empty string"        -> "null"
//	a placeholder list            -> "working on it"
//	a looks-like-a-pointer regex  -> "still going, read/write path unclear"
//	"something must resolve"      -> "AGENTS.md"
//
// The fourth is the instructive one. In a real repository almost everything
// already exists, so a stalled loop does not need prose that resolves to
// something NEW -- one word naming something OLD passes. "Resolves" reads as
// "verified" while meaning only "pre-existing".
//
// The question a tick log has to answer is not "does this name a real thing"
// but "did this tick make something". So the bar is recency: a token counts
// only if it post-dates the previous tick. A file modified since then, a
// commit made since then, a pull request opened since then. Everything that
// was already there proves nothing about the last three minutes.
//
// since is the previous tick's timestamp; a zero value means this is the
// first tick and any resolving token is accepted.
func Validate(artifact string, since time.Time, produced func(tok string, since time.Time) bool) error {
	a := strings.TrimSpace(artifact)
	if a == "" {
		return nil // absent is legitimate; the caller records null
	}
	if strings.Contains(a, "://") {
		return nil
	}
	cands := Candidates(a)
	if len(cands) == 0 {
		// Only now is a placeholder check meaningful. Applying it to the
		// PREFIX of any sentence refused real artifacts like "pending PR
		// #907 merge, see docs/phase-plan.md" before they were ever looked
		// at -- found in the same review.
		if isPaddedPlaceholder(a) {
			return fmt.Errorf("tick: %q is a way of writing \"no artifact\" -- omit it instead", artifact)
		}
		return fmt.Errorf("tick: %q names nothing a human can open -- an artifact must contain a path, a commit sha, an issue or PR number, or a URL. "+
			"If this tick produced no artifact, omit it; saying so is a legitimate tick result", artifact)
	}
	if produced == nil {
		return nil
	}
	for _, c := range cands {
		if produced(c, since) {
			return nil
		}
	}
	if since.IsZero() {
		return fmt.Errorf("tick: nothing in %q could be found (looked at: %s)", artifact, strings.Join(cands, ", "))
	}
	return fmt.Errorf("tick: nothing in %q was produced since the last tick at %s (looked at: %s) -- "+
		"naming something that already existed is not evidence this tick did anything. Omit the artifact instead",
		artifact, since.UTC().Format(time.RFC3339), strings.Join(cands, ", "))
}

// isPaddedPlaceholder catches a placeholder with words stapled on, which
// defeated the exact-match list ("n/a for now", "TBD later").
func isPaddedPlaceholder(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	if placeholders[low] {
		return true
	}
	for p := range placeholders {
		if low == p || strings.HasPrefix(low, p+" ") {
			return true
		}
	}
	for _, lead := range []string{"none", "nothing", "no artifact", "not yet", "still"} {
		if strings.HasPrefix(low, lead+" ") {
			return true
		}
	}
	return false
}

// hasArtifact reports whether this tick produced something a human can look
// at. A null artifact, an empty-string artifact and a placeholder are the
// same absence.
// hasArtifact reports whether this tick produced something a human can look
// at.
//
// It applies the SAME shape rule the writer applies, because Record's
// validation only protects entries written through the CLI, and entries
// arrive by other routes -- a hand-edit, a merge, or a probe run without
// ESTATE_TICK_LOG, which has already put one line into the production log.
// An entry naming something no reader could open is absence however it got
// here.
//
// Recency is NOT re-checked here: it needs a per-entry resolver and would
// mean git and network calls on every read. That is a real limit and it is
// stated in Check's own output.
func (p parsed) hasArtifact() bool {
	if p.Artifact == nil {
		return false
	}
	a := strings.TrimSpace(*p.Artifact)
	if a == "" || isPaddedPlaceholder(a) {
		return false
	}
	if strings.Contains(a, "://") {
		return true
	}
	return len(Candidates(a)) > 0
}

// Check reads the log and reports whether the last Window entries share a
// phase item and a src head while producing no artifact.
//
// A log that does not exist yet is not a stall -- it is a loop that has not
// ticked. A log that exists and cannot be parsed is an error: "could not
// measure" must never read as clean.
//
// This is the shape-only check the package has always done: an artifact
// counts as evidence if it looks like it names something a human could open.
// See CheckWithResolver for the version that actually asks.
func Check(path string) (Verdict, error) {
	return checkImpl(path, nil)
}

// CheckWithResolver is Check, plus resolving every artifact in the window
// against resolve rather than trusting its shape.
//
// WHERE THIS BELONGS, AND WHY NOT Record. agent-estate#931 lists three
// places verification could live: inside Record (fails closed on a flaky
// connection, and Record's whole job is to keep writing the Director's own
// history -- a recording step that refuses because the network hiccupped
// stops the loop for a reason that has nothing to do with the work, which
// the issue explicitly rules out); a separate `estate tick verify` sweep
// (finds a fabrication only after it has already sat in the log clearing
// stalls, possibly for a while); or here, in Check -- which already gates
// the loop every tick, already fails closed on a corrupt log or a truncated
// one, and already re-derives write-time rules at read time (AuditWindow) on
// the grounds that Record only protects entries written through the CLI.
// Resolving the window here catches a fabricated artifact at the next tick,
// the same tick boundary the stop condition itself operates on, without
// touching Record's own contract.
//
// An artifact that resolves ResolveInvalid does not clear the stall -- it is
// treated exactly as if it were absent, which is what the evidence says it
// is. An artifact that resolves ResolveUnknown ALSO does not clear the
// stall, but is counted separately in Verdict.Unverifiable and named in the
// reason, rather than silently folded into either bucket: this repo already
// treats "could not measure" as "refuse" everywhere else it appears
// (pressure.Check, Validate's produced callback, CheckAgainstCommitted's
// negative committed count), and a network hiccup producing a spurious stall
// report is a human glancing at one extra escalation, not a fabricated
// artifact quietly passing as real.
func CheckWithResolver(path string, resolve Resolve) (Verdict, error) {
	return checkImpl(path, resolve)
}

func checkImpl(path string, resolve Resolve) (Verdict, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Verdict{Reason: "no tick log yet"}, nil
	}
	if err != nil {
		return Verdict{}, fmt.Errorf("tick: open %s: %w", path, err)
	}
	defer f.Close()

	var entries []parsed
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var p parsed
		if err := json.Unmarshal([]byte(text), &p); err != nil {
			return Verdict{}, fmt.Errorf("tick: %s line %d is not readable, so the stop condition cannot be evaluated: %w", path, n, err)
		}
		entries = append(entries, p)
	}
	if err := sc.Err(); err != nil {
		return Verdict{}, fmt.Errorf("tick: read %s: %w", path, err)
	}

	if len(entries) < Window {
		return Verdict{
			Considered: len(entries),
			Reason:     fmt.Sprintf("%d tick(s) on record; %d are needed to establish a stall", len(entries), Window),
		}, nil
	}

	// THE RULE: three consecutive ticks that produced nothing a human can
	// look at. Nothing else clears it.
	//
	// The brief's section 3 words this as "the same phase_item and the same
	// src_head with artifact: null", and this deliberately departs from that
	// literal wording, because an independent review demonstrated the literal
	// form does not catch what section 3 says it is for -- "a loop that is
	// running and producing nothing":
	//
	//   - A loop bouncing phase-0, phase-1, phase-0 forever, producing
	//     nothing, never has three consecutive entries sharing phase_item, so
	//     the equality test cleared it every time.
	//   - src_head is read as `git log -1 -- src/`, the whole tree. An
	//     unrelated commit anywhere under src/ moved it and cleared the stall
	//     for a phase item that had not advanced at all.
	//
	// Both are the SAME mistake: treating a signal that merely CHANGED as
	// evidence that THIS work advanced. Only the artifact is that evidence,
	// so only the artifact clears the stall. phase_item and src_head are kept
	// in the record and named in the reason -- they say what was stuck and
	// where -- but they no longer excuse a stall.
	//
	// This is strictly stronger: every log the old rule flagged, this flags.
	last := entries[len(entries)-Window:]

	// Judge each entry's artifact once: does it count as evidence this tick
	// produced something. With no resolver this is exactly the old
	// shape-only hasArtifact(). With one, ResolveInvalid is treated as no
	// artifact and ResolveUnknown is neither -- see CheckWithResolver's doc
	// comment for why an unresolvable artifact must not clear a stall.
	counted := make([]string, len(last)) // "" means does not count
	unverifiable := 0
	for i, e := range last {
		if !e.hasArtifact() {
			continue
		}
		a := strings.TrimSpace(*e.Artifact)
		if resolve == nil {
			counted[i] = a
			continue
		}
		switch res, _ := resolve(a, e.SrcHead); res {
		case ResolveValid:
			counted[i] = a
		case ResolveUnknown:
			unverifiable++
		case ResolveInvalid:
			// Resolved, and it is not real: counts as absent, same as if
			// the field had been empty.
		}
	}

	// Repeating ONE artifact across the window is not new output. A loop that
	// keeps pointing at something it produced three ticks ago is producing
	// nothing now, and naming it again must not clear the stall.
	distinct := map[string]bool{}
	producing := 0
	for _, a := range counted {
		if a != "" {
			distinct[a] = true
			producing++
		}
	}
	if len(distinct) == 1 && Window > 1 && producing == Window {
		only := ""
		for a := range distinct {
			only = a
		}
		return Verdict{
			Stalled:      true,
			Considered:   Window,
			Unverifiable: unverifiable,
			Reason: fmt.Sprintf("the last %d ticks all named the same artifact (%q) -- that is one piece of output, not %d",
				Window, only, Window),
		}, nil
	}

	if producing > 0 {
		return Verdict{
			Considered:   Window,
			Unverifiable: unverifiable,
			Reason:       fmt.Sprintf("the last %d ticks include one that produced an artifact", Window),
		}, nil
	}

	items, heads := map[string]bool{}, map[string]bool{}
	for _, e := range last {
		items[e.PhaseItem] = true
		heads[e.SrcHead] = true
	}
	where := fmt.Sprintf("phase item %q", last[0].PhaseItem)
	if len(items) > 1 {
		where = fmt.Sprintf("%d different phase items", len(items))
	}
	at := "src head " + last[0].SrcHead
	if len(heads) > 1 {
		at = "src head moving, which is not evidence this work advanced"
	}
	reason := fmt.Sprintf("the last %d ticks produced no artifact (%s, %s)", Window, where, at)
	if unverifiable > 0 {
		reason += fmt.Sprintf("; %d artifact(s) in the window could not be checked (network or tooling failure) and are counted as neither real nor fabricated", unverifiable)
	}
	return Verdict{
		Stalled:      true,
		Considered:   Window,
		Unverifiable: unverifiable,
		Reason:       reason,
	}, nil
}

// CheckWithEscalation is CheckWithResolver, plus recognising a recorded
// escalation as ACKNOWLEDGMENT of the current stall rather than as evidence
// that clears it.
//
// WHY A STALL CAN BE ACKNOWLEDGED BUT NEVER CLEARED BY IT. agent-estate#923:
// the stop condition has no way to represent "stalled, escalated, awaiting a
// response" -- a real state the brief itself names (docs/director-brief.md
// section 3: "the clock does not run while you are blocked on operator
// review") -- so a genuinely escalated loop reads identically to one that
// never noticed its own stall, and the loop's own next tick is STALLED
// again, forever, because a stalled tick correctly records nothing and the
// window therefore never changes. The only recorded escape was a human
// overriding the rule in the open, which is exactly the judgement call this
// guard exists to replace.
//
// This function never lets recording an escalation turn into a manufactured
// artifact: EscalationEntry has no artifact field, lives in its own log (see
// RecordEscalation), and this function does not touch Validate, hasArtifact,
// or the tick log at all -- it only reads the escalation log and layers a
// second judgement on top of Verdict.Stalled. checkImpl's own verdict is
// computed first, unmodified; an escalation can only ADD Escalated /
// EscalatedAt / EscalationCount to an already-stalled verdict, never flip
// Stalled itself.
//
// An escalation acknowledges ONLY the tick that provoked it: it must name
// the same phase item and src head as the most recently recorded tick, and
// be timestamped at or after it. An escalation for a stall that has since
// moved (a new src head from real work, a new phase item) does not carry
// forward -- the loop must escalate again to acknowledge the NEW stall, and
// a genuinely-escalated loop that goes on to record real work moves the
// window exactly the way checkImpl already lets any artifact move it: this
// function adds nothing that would prevent that recovery.
//
// Every escalation matching the current (phase item, src head) is counted
// in EscalationCount, fresh or not, so a loop escalating the SAME stall
// repeatedly -- because it is still stuck, not because it moved on -- stays
// visibly distinguishable from a loop that escalated once and is waiting.
// Nothing here ever reports a repeatedly-escalating loop as merely
// "escalated"; the reason string and EscalationCount always carry the
// repeat count.
func CheckWithEscalation(tickPath, escalationPath string, resolve Resolve) (Verdict, error) {
	v, err := checkImpl(tickPath, resolve)
	if err != nil || !v.Stalled {
		return v, err
	}

	tail, ok, err := lastTickEntry(tickPath)
	if err != nil {
		return Verdict{}, err
	}
	if !ok {
		// Unreachable in practice -- Stalled true implies entries exist --
		// but an unmeasurable state must never read as acknowledged.
		return v, nil
	}
	lastAt, _ := time.Parse(time.RFC3339, tail.At)

	escalations, err := readEscalations(escalationPath)
	if err != nil {
		return Verdict{}, err
	}

	var (
		matches  int
		freshest time.Time
		freshWho string
		acked    bool
	)
	for _, e := range escalations {
		if e.PhaseItem != tail.PhaseItem || e.SrcHead != tail.SrcHead {
			continue
		}
		matches++
		eAt, perr := time.Parse(time.RFC3339, e.At)
		if perr != nil {
			continue
		}
		if !lastAt.IsZero() && !eAt.Before(lastAt) {
			acked = true
			if eAt.After(freshest) {
				freshest, freshWho = eAt, e.Where
			}
		}
	}

	if !acked {
		return v, nil
	}
	v.Escalated = true
	v.EscalatedAt = freshest
	v.EscalationCount = matches
	if matches > 1 {
		v.Reason = fmt.Sprintf("%s -- escalated %d time(s) for this same stall, most recently to %s at %s: the underlying stall has not changed",
			v.Reason, matches, freshWho, freshest.UTC().Format(time.RFC3339))
	} else {
		v.Reason = fmt.Sprintf("%s -- escalated to %s at %s", v.Reason, freshWho, freshest.UTC().Format(time.RFC3339))
	}
	return v, nil
}

// lastTickEntry returns the most recently recorded tick's own timestamp,
// phase item and src head, so CheckWithEscalation can decide which stall an
// escalation is claiming to acknowledge. ok is false when the log has no
// entries at all.
func lastTickEntry(path string) (struct{ At, PhaseItem, SrcHead string }, bool, error) {
	var out struct{ At, PhaseItem, SrcHead string }
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return out, false, nil
	}
	if err != nil {
		return out, false, fmt.Errorf("tick: open %s: %w", path, err)
	}
	defer f.Close()
	var lastLine string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			lastLine = t
		}
	}
	if err := sc.Err(); err != nil {
		return out, false, err
	}
	if lastLine == "" {
		return out, false, nil
	}
	var e struct {
		At        string `json:"at"`
		PhaseItem string `json:"phase_item"`
		SrcHead   string `json:"src_head"`
	}
	if err := json.Unmarshal([]byte(lastLine), &e); err != nil {
		return out, false, fmt.Errorf("tick: %s last line is not readable: %w", path, err)
	}
	out.At, out.PhaseItem, out.SrcHead = e.At, e.PhaseItem, e.SrcHead
	return out, true, nil
}

// LastAt is lastTickAt, exported so a caller building the NEXT entry (main.go's
// `tick record`) can bound an observed-spend window against it before that
// entry exists -- see spend.WindowedByObservation and Entry.ObservedTurns/
// ObservedSpendUSD's own doc comments for why "no previous tick" (the zero
// value returned here) means no window, not a zero-length one.
func LastAt(path string) time.Time { return lastTickAt(path) }

// LastRecorded is what the most recently written tick entry itself recorded
// for the gap/observed-spend fields -- what `tick check` surfaces without
// re-deriving them (that derivation belongs to whoever wrote the entry, at
// the moment it was written; re-computing it later against a NOW that has
// moved on would silently change a number that already happened).
type LastRecorded struct {
	At                    string
	PhaseItem             string
	GapSeconds            *int64
	ObservedTurns         *int64
	ObservedSpendUSD      *float64
	ObservedTurnsWithCost *int64
}

// LastEntry returns the most recently recorded tick's own gap and
// observed-spend figures. ok is false when the log has no entries at all --
// never confuse that with "recorded, and both fields nil."
func LastEntry(path string) (LastRecorded, bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return LastRecorded{}, false, nil
	}
	if err != nil {
		return LastRecorded{}, false, fmt.Errorf("tick: open %s: %w", path, err)
	}
	defer f.Close()
	var lastLine string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			lastLine = t
		}
	}
	if err := sc.Err(); err != nil {
		return LastRecorded{}, false, err
	}
	if lastLine == "" {
		return LastRecorded{}, false, nil
	}
	var e struct {
		At                    string   `json:"at"`
		PhaseItem             string   `json:"phase_item"`
		GapSeconds            *int64   `json:"gap_seconds"`
		ObservedTurns         *int64   `json:"observed_turns"`
		ObservedSpendUSD      *float64 `json:"observed_spend_usd"`
		ObservedTurnsWithCost *int64   `json:"observed_turns_with_cost"`
	}
	if err := json.Unmarshal([]byte(lastLine), &e); err != nil {
		return LastRecorded{}, false, fmt.Errorf("tick: %s last line is not readable: %w", path, err)
	}
	return LastRecorded{
		At:                    e.At,
		PhaseItem:             e.PhaseItem,
		GapSeconds:            e.GapSeconds,
		ObservedTurns:         e.ObservedTurns,
		ObservedSpendUSD:      e.ObservedSpendUSD,
		ObservedTurnsWithCost: e.ObservedTurnsWithCost,
	}, true, nil
}

// SpendKind is what an entry's observed-spend fields can honestly be read as.
//
// It exists because the pairing between ObservedSpendUSD and
// ObservedTurnsWithCost is kept only by whoever WRITES an entry, and the
// commands that print those figures do not write them: `tick check` reads
// back whatever JSON is on the last line of a log that may have been
// hand-edited, partially migrated, or appended to by a future write path that
// sets one field of the pair and forgets the other. That is not hypothetical
// -- agent-estate#995 exists because a fix pass wired exactly one field of a
// pair -- and an invariant kept only by the current writer is a habit, not an
// invariant. So the reader classifies the fields it was handed instead of
// trusting them, and hands back values that are safe to print without
// dereferencing anything.
type SpendKind int

const (
	// SpendNoWindow: no observed-turn count at all, so there was no window to
	// measure (the first tick ever recorded, or an entry predating
	// agent-estate#982).
	SpendNoWindow SpendKind = iota
	// SpendNoTurns: a real window in which nothing reached a terminal state.
	SpendNoTurns
	// SpendNoneReported: turns finished, none of them reported a dollar
	// figure (e.g. every one was a codex turn).
	SpendNoneReported
	// SpendReported: a dollar figure AND the count of turns that produced it,
	// both present and mutually consistent -- the only reading from which a
	// dollar line may be printed.
	SpendReported
	// SpendUnreadable: the fields contradict each other, so no honest dollar
	// line can be built from them. Why names which pairing broke. This is
	// deliberately not repaired into a plausible number: a count invented to
	// stand beside a real dollar figure is indistinguishable from a measured
	// one, which is the failure this whole field pair was added to prevent.
	SpendUnreadable
)

// SpendReading is a dereference-safe reading of an entry's observed-spend
// fields. Only the fields its Kind names are meaningful; the rest are zero
// because they were absent or untrustworthy, never because they were
// measured as zero.
type SpendReading struct {
	Kind SpendKind
	// USD is meaningful only when Kind is SpendReported.
	USD float64
	// Turns is meaningful when Kind is SpendNoTurns or SpendNoneReported.
	Turns int64
	// TurnsWithCost is meaningful only when Kind is SpendReported.
	TurnsWithCost int64
	// Why is set only when Kind is SpendUnreadable, naming the exact
	// contradiction so a reader can tell a broken entry from a quiet window.
	Why string
}

// ReadSpend classifies the three observed-spend pointers of an entry --
// Entry's own fields when recording, LastRecorded's when reading a log back
// -- into the one thing that can be said about them without inventing a
// number. Every combination is classified: anything that is not one of the
// four coherent shapes is SpendUnreadable with a reason, never a silent
// dereference. See SpendKind.
func ReadSpend(turns *int64, usd *float64, turnsWithCost *int64) SpendReading {
	switch {
	case turns == nil:
		// No window. Spend fields alongside a missing turn count are not a
		// window that went unmeasured -- they are an entry that contradicts
		// itself, and saying "not measured" would hide a real figure.
		if usd != nil || turnsWithCost != nil {
			return SpendReading{Kind: SpendUnreadable, Why: "the entry carries observed-spend figures but no observed-turn count to bound them"}
		}
		return SpendReading{Kind: SpendNoWindow}

	case usd != nil && turnsWithCost == nil:
		// The reviewer's reproduction on agent-estate#997: a dollar figure
		// whose denominator is missing. Printing it "across 0 turns" would be
		// a fabricated count; printing it with no count at all would restate
		// the #995 defect in a new form.
		return SpendReading{Kind: SpendUnreadable, Why: "the entry carries a dollar figure with no count of the turns that reported it"}

	case usd == nil && turnsWithCost != nil:
		return SpendReading{Kind: SpendUnreadable, Why: "the entry carries a count of turns that reported a cost, but no dollar figure they add up to"}

	case usd != nil && turnsWithCost != nil:
		switch {
		case *turnsWithCost <= 0:
			// A non-nil total requires at least one turn to have contributed
			// it, so a zero or negative count cannot have produced this sum.
			return SpendReading{Kind: SpendUnreadable, Why: "the entry claims a dollar figure produced by no turn at all"}
		case *turnsWithCost > *turns:
			return SpendReading{Kind: SpendUnreadable, Why: "the entry claims more turns reported a cost than finished in the window"}
		}
		return SpendReading{Kind: SpendReported, USD: *usd, Turns: *turns, TurnsWithCost: *turnsWithCost}

	case *turns > 0:
		return SpendReading{Kind: SpendNoneReported, Turns: *turns}

	default:
		return SpendReading{Kind: SpendNoTurns}
	}
}

// Spend classifies the entry's own observed-spend fields. See ReadSpend.
func (e Entry) Spend() SpendReading {
	return ReadSpend(e.ObservedTurns, e.ObservedSpendUSD, e.ObservedTurnsWithCost)
}

// Spend classifies the read-back entry's observed-spend fields. See
// ReadSpend -- this is the side that matters most, since these values came
// off disk rather than from the code that computed them.
func (l LastRecorded) Spend() SpendReading {
	return ReadSpend(l.ObservedTurns, l.ObservedSpendUSD, l.ObservedTurnsWithCost)
}

// lastTickAt returns the timestamp of the most recent entry, or the zero time
// when there is none. A tick's artifact is judged against the moment the
// previous tick ended, which is the window this tick is accountable for.
func lastTickAt(path string) time.Time {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	var last string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			last = t
		}
	}
	if last == "" {
		return time.Time{}
	}
	var e struct {
		At string `json:"at"`
	}
	if json.Unmarshal([]byte(last), &e) != nil {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339, e.At)
	if err != nil {
		return time.Time{}
	}
	return at
}

// Entries counts the records in the log. A missing log is zero, not an error:
// the caller decides whether zero is legitimate.
func Entries(path string) (int, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n, sc.Err()
}

// CheckAgainstCommitted refuses when the working copy of the log has fewer
// records than the copy committed to git.
//
// WHY. The stop condition lived entirely in a local file that the agent it
// constrains can delete. An independent reviewer showed the whole guarantee
// evaporates with one `rm`: the log goes missing, lastTickAt returns zero,
// every artifact is accepted again and a genuine three-tick stall reads as
// "no tick log yet". A guard whose evidence its subject can destroy is not a
// guard.
//
// The log is tracked in git, so the committed copy is evidence the Director
// does not solely own. It can still be beaten -- by committing a truncated
// log -- but that leaves a visible commit, which is the point: the cost of
// hiding a stall becomes an artifact a human can see.
//
// committed is the record count in the committed copy; a negative value means
// it could not be read, which is "could not measure" and never clean.
func CheckAgainstCommitted(path string, committed int) error {
	if committed < 0 {
		return fmt.Errorf("tick: cannot read the committed copy of %s, so the record cannot be trusted", path)
	}
	have, err := Entries(path)
	if err != nil {
		return fmt.Errorf("tick: cannot count records in %s: %w", path, err)
	}
	if have < committed {
		return fmt.Errorf("tick: %s holds %d record(s) but the committed copy holds %d -- "+
			"the record has been truncated or replaced, and a stop condition whose evidence went missing must not read as healthy",
			path, have, committed)
	}
	return nil
}

// phaseRE matches a phase heading in the plan: "## Phase 3 — ...".
var phaseRE = regexp.MustCompile(`(?m)^##\s+Phase\s+(\d+)\b`)

// KnownPhases returns the phase items the plan actually names, as the ids a
// tick records ("phase-0", "phase-1", …).
//
// WHY THE LOG ANSWERS TO THE PLAN. A tick's phase item was free text, so
// anything could be written into the record: a typo, a label invented on the
// spot, or -- as happened -- a throwaway probe run without ESTATE_TICK_LOG
// set, which wrote `{"phase_item":"ph","artifact":"AGENTS.md"}` straight into
// the production log and was then read back as a healthy tick.
//
// Requiring the phase item to exist in the plan makes two things true: a
// stray write cannot masquerade as a tick, and work that does not map to any
// phase forces the plan to be updated rather than the log to be fudged.
func KnownPhases(planPath string) ([]string, error) {
	b, err := os.ReadFile(planPath)
	if err != nil {
		return nil, fmt.Errorf("tick: cannot read %s to learn which phases exist: %w", planPath, err)
	}
	var out []string
	for _, m := range phaseRE.FindAllStringSubmatch(string(b), -1) {
		out = append(out, "phase-"+m[1])
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tick: %s names no phases, so no phase item can be checked against it", planPath)
	}
	return out, nil
}

// CheckPhaseItem refuses a phase item the plan does not name.
func CheckPhaseItem(item string, known []string) error {
	for _, k := range known {
		if item == k {
			return nil
		}
	}
	return fmt.Errorf("tick: %q is not a phase in the plan (it names: %s) -- "+
		"if this work is real, give it a phase there rather than a label here",
		item, strings.Join(known, ", "))
}

// AuditWindow refuses when an entry in the window the stop condition reads
// names a phase the plan does not have.
//
// WHY THE WINDOW AND NOT THE WHOLE LOG. The log is append-only history and
// already contains one polluted line -- a probe written before phase items
// were checked. Refusing on history would wedge the loop over a line that is
// deliberately preserved. The last Window entries are what the verdict is
// computed from, so those are what must be well-formed.
func AuditWindow(path string, known []string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("tick: cannot audit %s: %w", path, err)
	}
	defer f.Close()

	var items []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		t := strings.TrimSpace(sc.Text())
		if t == "" {
			continue
		}
		var e struct {
			PhaseItem string `json:"phase_item"`
		}
		if json.Unmarshal([]byte(t), &e) != nil {
			return fmt.Errorf("tick: %s holds an unreadable record, so the verdict cannot be computed", path)
		}
		items = append(items, e.PhaseItem)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if len(items) > Window {
		items = items[len(items)-Window:]
	}
	for _, it := range items {
		if err := CheckPhaseItem(it, known); err != nil {
			return fmt.Errorf("tick: the last %d records include one the writer would have refused: %w", Window, err)
		}
	}
	return nil
}

// VerifiedEntry is one log entry's artifact together with what resolving it
// found, for `estate tick verify`'s report -- the whole log, not just the
// window Check reads, so a human can see how much of the history is
// evidence and how much is not.
type VerifiedEntry struct {
	At         string
	PhaseItem  string
	Artifact   string
	Resolution Resolution
	Detail     string
}

// VerifyAll resolves the artifact of every entry in the log that has one,
// using resolve to make the real check. Entries with no artifact are
// skipped -- there is nothing there to resolve, and that is a legitimate
// tick result, not a gap in the report.
func VerifyAll(path string, resolve Resolve) ([]VerifiedEntry, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("tick: open %s: %w", path, err)
	}
	defer f.Close()

	var out []VerifiedEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var p parsed
		var at struct {
			At string `json:"at"`
		}
		if err := json.Unmarshal([]byte(text), &p); err != nil {
			return nil, fmt.Errorf("tick: %s line %d is not readable: %w", path, n, err)
		}
		json.Unmarshal([]byte(text), &at) //nolint:errcheck -- best-effort for the report's timestamp column
		if !p.hasArtifact() {
			continue
		}
		a := strings.TrimSpace(*p.Artifact)
		res, detail := resolve(a, p.SrcHead)
		out = append(out, VerifiedEntry{
			At:         at.At,
			PhaseItem:  p.PhaseItem,
			Artifact:   a,
			Resolution: res,
			Detail:     detail,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("tick: read %s: %w", path, err)
	}
	return out, nil
}
