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
func Record(path string, e Entry) error {
	if e.PhaseItem == "" {
		return errors.New("tick: phase_item is required -- a tick that cannot name what it advanced is the thing this record exists to catch")
	}
	// Refuse the dodge at the point it would be taken, not only when reading
	// the record back. Nothing is written when this fires.
	if e.Artifact != "" && !Locatable(e.Artifact) {
		return fmt.Errorf("tick: %q names nothing a human can open -- an artifact must contain a path, a commit sha, an issue or PR number, or a URL. "+
			"If this tick produced no artifact, omit it; saying so is a legitimate tick result", e.Artifact)
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
	pathRE  = regexp.MustCompile(`(^|[\s(])[\w.-]+/[\w./-]+`)
)

// Locatable reports whether s names something a reader could actually go and
// open: a path, a commit sha, an issue or pull request number, or a URL.
//
// WHY A SYNTACTIC TEST AND NOT A JUDGEMENT. A placeholder list catches
// "null" and "tbd", but an independent review defeated that immediately with
// "working on it" and "still going" -- plausible prose that names no output.
// No string inspection can tell a real accomplishment from a convincing
// sentence about one. What it CAN tell is whether the text points at
// something. "What a human can look at" means locatable, so that is the bar:
// an artifact must contain a pointer, and prose alone is refused.
//
// This is deliberately loose about whether the target EXISTS -- that is the
// caller's job, since only it knows the working directory. Here it is only
// about shape.
func Locatable(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || IsPlaceholder(s) {
		return false
	}
	if strings.Contains(s, "://") {
		return true
	}
	return issueRE.MatchString(s) || pathRE.MatchString(s) || shaRE.MatchString(s)
}

// hasArtifact reports whether this tick produced something a human can look
// at. A null artifact, an empty-string artifact and a placeholder are the
// same absence.
func (p parsed) hasArtifact() bool {
	if p.Artifact == nil {
		return false
	}
	a := strings.TrimSpace(*p.Artifact)
	return a != "" && !IsPlaceholder(a)
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
