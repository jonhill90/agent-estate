// Package navwalk is the structural fix for agent-b3.md's own finding:
// a single hand-maintained testdata/vhs/full-nav-walk-report.md, edited by
// every lane that drives any nav destination, is a merge-conflict
// generator -- two lanes measuring two DIFFERENT routes still collide on
// the same file, and the losing side's resolution can silently discard a
// real measurement someone else took (exactly what happened across PR
// agent-tui#97/agent-tui#98/agent-tui#99: three lanes, one shared table, three near-simultaneous
// conflicts).
//
// The fix: one JSONL file per nav destination
// (testdata/vhs/nav-walk/observations/<route-id>.jsonl), append-only.
// A lane that re-drives one route appends ONE line to ONE file -- it
// never touches any other route's storage, so two lanes measuring two
// different routes touch two different files and cannot conflict at the
// git level at all (TestTwoRoutesNeverConflict, observation_test.go,
// demonstrates this with a real git merge, not an assertion that it
// should work). Every Observation carries its own Date and Source, so
// "which is newer" is decidable by reading the file, never by memory or
// by which side git happened to keep.
//
// The human-readable table still exists -- Generate (render.go) rebuilds
// testdata/vhs/full-nav-walk-report.md mechanically from every route's
// latest Observation plus manifest.json's display order/labels. That
// generated file is never hand-edited; nothing about a lane's job changes
// except WHERE the measurement is written (cmd/navwalk's own doc comment
// has the exact two-command workflow).
package navwalk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Verdict is the same closed vocabulary every nav walk report before this
// one used -- kept as a plain string, not an enum, so a JSONL file already
// on disk never fails to parse just because a new verdict was added here.
type Verdict string

const (
	VerdictRenders         Verdict = "RENDERS"
	VerdictStub            Verdict = "STUB"
	VerdictEmpty           Verdict = "EMPTY"
	VerdictBroken          Verdict = "BROKEN"
	VerdictRemoved         Verdict = "REMOVED"
	VerdictCouldNotMeasure Verdict = "could not measure"
)

// Observation is one lane's own recorded measurement of one nav
// destination, at one point in time. Date and Source are what make
// "newest" decidable from the file itself (agent-b3.md's own
// requirement) -- Date in YYYY-MM-DD form so plain string comparison
// orders correctly, Source naming the PR or run that produced it (e.g.
// "PR agent-tui#97 (agent-b3.md)") so a reader never has to guess who measured
// this or trust an unlabelled claim.
type Observation struct {
	Date    string  `json:"date"`
	Source  string  `json:"source"`
	Verdict Verdict `json:"verdict"`
	Notes   string  `json:"notes"`
}

// AppendObservation appends one Observation to path as a single JSON
// line -- the only write this package ever performs against a per-route
// file, and it never rewrites or reorders a line already there (append-only,
// per this package's own doc comment). path's parent directory is
// created if missing so a brand-new route's first observation does not
// require a human to `mkdir` first.
func AppendObservation(path string, obs Observation) error {
	line, err := json.Marshal(obs)
	if err != nil {
		return fmt.Errorf("navwalk: encode observation: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("navwalk: create %s: %w", dir, err)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("navwalk: open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("navwalk: append %s: %w", path, err)
	}
	return nil
}

// ReadObservations parses every line of path as an Observation. A missing
// file is not an error -- a route nobody has ever measured yet returns an
// empty slice, the same "absence is a typed value" discipline this
// module already follows elsewhere (AGENTS.md), not a crash.
func ReadObservations(path string) ([]Observation, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("navwalk: open %s: %w", path, err)
	}
	defer f.Close()

	var out []Observation
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var obs Observation
		if err := json.Unmarshal(line, &obs); err != nil {
			return nil, fmt.Errorf("navwalk: decode %s: %w", path, err)
		}
		out = append(out, obs)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("navwalk: scan %s: %w", path, err)
	}
	return out, nil
}

// Latest returns the most recently DATED Observation in obs, not simply
// the last line in the file -- two lanes appending near-simultaneously
// can land in either order once git merges their two branches, so
// trusting file order alone would let a genuinely older measurement win
// a race it should have lost. Ties (same Date) keep file order, on the
// assumption that within one calendar day, later-appended is
// later-observed. ok is false for an empty slice -- "never measured," a
// distinct, typed state from any real Observation.
func Latest(obs []Observation) (Observation, bool) {
	if len(obs) == 0 {
		return Observation{}, false
	}
	sorted := make([]Observation, len(obs))
	copy(sorted, obs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date < sorted[j].Date })
	return sorted[len(sorted)-1], true
}
