// Package ledger is the durable record of dispatched work.
//
// Append-only JSON lines. A task is written once when dispatched and once
// again for every state change; the current state of a task is its last
// record. Append-only means a crash mid-write loses at most the record being
// written, never the history, and it means authorship can never be destroyed
// by a cancel -- the old supervisor lost a review's authorship exactly that
// way and approved a lane to review its own PR.
package ledger

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type State string

const (
	Dispatched State = "dispatched" // process launched
	Complete   State = "complete"   // process exited 0 and produced a result
	Failed     State = "failed"     // process exited non-zero
	Unknown    State = "unknown"    // timed out or could not be observed
)

// Terminal reports whether a state means the slot is free.
// Unknown is deliberately NOT terminal: a turn we could not observe may still
// be running, and treating it as finished is how a cap fails open.
func (s State) Terminal() bool { return s == Complete || s == Failed }

type Record struct {
	ID     string    `json:"id"`
	Issue  string    `json:"issue"`
	Lane   string    `json:"lane"`
	State  State     `json:"state"`
	At     time.Time `json:"at"`
	PID    int       `json:"pid,omitempty"`
	Note   string    `json:"note,omitempty"`
	Result string    `json:"result,omitempty"`
}

type Ledger struct{ path string }

func Open(path string) (*Ledger, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".local", "state", "estate", "ledger.jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &Ledger{path: path}, nil
}

func (l *Ledger) Append(r Record) error {
	if r.ID == "" {
		return errors.New("ledger: record has no id")
	}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// Current returns the latest record per task id, oldest first.
func (l *Ledger) Current() ([]Record, error) {
	f, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	latest := map[string]Record{}
	order := []string{}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for s.Scan() {
		var r Record
		if err := json.Unmarshal(s.Bytes(), &r); err != nil {
			// A malformed line is corruption, not absence. Say so rather than
			// silently returning a short list that reads as "less work".
			return nil, fmt.Errorf("ledger: malformed record: %w", err)
		}
		if _, seen := latest[r.ID]; !seen {
			order = append(order, r.ID)
		}
		latest[r.ID] = r
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(latest))
	for _, id := range order {
		out = append(out, latest[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// InFlight returns tasks whose latest state is not terminal.
func (l *Ledger) InFlight() ([]Record, error) {
	cur, err := l.Current()
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, r := range cur {
		if !r.State.Terminal() {
			out = append(out, r)
		}
	}
	return out, nil
}
