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

// hasArtifact reports whether this tick produced something a human can look
// at. A null artifact and an empty-string artifact are the same absence.
func (p parsed) hasArtifact() bool {
	return p.Artifact != nil && strings.TrimSpace(*p.Artifact) != ""
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

	last := entries[len(entries)-Window:]
	first := last[0]
	for _, e := range last {
		if e.hasArtifact() {
			return Verdict{Considered: Window, Reason: "the last " + fmt.Sprint(Window) + " ticks include one that produced an artifact"}, nil
		}
		if e.PhaseItem != first.PhaseItem {
			return Verdict{Considered: Window, Reason: "the phase item changed within the last " + fmt.Sprint(Window) + " ticks"}, nil
		}
		if e.SrcHead != first.SrcHead {
			return Verdict{Considered: Window, Reason: "src head moved within the last " + fmt.Sprint(Window) + " ticks"}, nil
		}
	}
	return Verdict{
		Stalled:    true,
		Considered: Window,
		Reason: fmt.Sprintf("the last %d ticks all sat on phase item %q at src head %s and produced no artifact",
			Window, first.PhaseItem, first.SrcHead),
	}, nil
}
