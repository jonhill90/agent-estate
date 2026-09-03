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
}

// MarshalJSON writes At as ISO 8601 UTC and an absent Artifact as null.
func (e Entry) MarshalJSON() ([]byte, error) {
	type wire struct {
		At        string  `json:"at"`
		PhaseItem string  `json:"phase_item"`
		SrcHead   string  `json:"src_head"`
		Artifact  *string `json:"artifact"`
	}
	w := wire{
		At:        e.At.UTC().Format(time.RFC3339),
		PhaseItem: e.PhaseItem,
		SrcHead:   e.SrcHead,
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
}

// Window is how many consecutive entries the stop condition looks at.
const Window = 3

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
func (p parsed) hasArtifact() bool {
	if p.Artifact == nil {
		return false
	}
	a := strings.TrimSpace(*p.Artifact)
	return a != "" && !isPaddedPlaceholder(a)
}

// Check reads the log and reports whether the last Window entries share a
// phase item and a src head while producing no artifact.
//
// A log that does not exist yet is not a stall -- it is a loop that has not
// ticked. A log that exists and cannot be parsed is an error: "could not
// measure" must never read as clean.
func Check(path string) (Verdict, error) {
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

	// Repeating ONE artifact across the window is not new output. A loop that
	// keeps pointing at something it produced three ticks ago is producing
	// nothing now, and naming it again must not clear the stall.
	distinct := map[string]bool{}
	for _, e := range last {
		if e.hasArtifact() {
			distinct[strings.TrimSpace(*e.Artifact)] = true
		}
	}
	if len(distinct) == 1 && Window > 1 {
		only := ""
		for a := range distinct {
			only = a
		}
		producing := 0
		for _, e := range last {
			if e.hasArtifact() {
				producing++
			}
		}
		if producing == Window {
			return Verdict{
				Stalled:    true,
				Considered: Window,
				Reason: fmt.Sprintf("the last %d ticks all named the same artifact (%q) -- that is one piece of output, not %d",
					Window, only, Window),
			}, nil
		}
	}

	for _, e := range last {
		if e.hasArtifact() {
			return Verdict{
				Considered: Window,
				Reason:     fmt.Sprintf("the last %d ticks include one that produced an artifact", Window),
			}, nil
		}
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
	return Verdict{
		Stalled:    true,
		Considered: Window,
		Reason:     fmt.Sprintf("the last %d ticks produced no artifact (%s, %s)", Window, where, at),
	}, nil
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
