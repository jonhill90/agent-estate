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

// Role is what a dispatched turn was sent to do, fixed at dispatch time.
// The merge gate must derive authorship and independence from this field --
// not from the issue a turn happens to share with the work it reviews. A
// review turn is dispatched against the same issue as the work it reviews
// (the bug this package exists to fix: "the merge gate cannot tell a
// reviewer from an author"), so Issue alone can never distinguish them.
type Role string

const (
	// RoleAuthor is the default: a turn dispatched to do or fix work on an
	// issue. Empty Role on an old record is read as RoleAuthor for backward
	// compatibility with records written before this field existed -- see
	// Record.EffectiveRole.
	RoleAuthor Role = "author"
	// RoleReviewer is a turn dispatched specifically to review a pull
	// request. It carries PR (below), which RoleAuthor turns do not: a
	// reviewer's independence is checked against a specific PR, not an
	// issue.
	RoleReviewer Role = "reviewer"
)

type Record struct {
	ID     string    `json:"id"`
	Issue  string    `json:"issue"`
	Lane   string    `json:"lane"`
	Role   Role      `json:"role,omitempty"`
	PR     int       `json:"pr,omitempty"`
	State  State     `json:"state"`
	At     time.Time `json:"at"`
	PID    int       `json:"pid,omitempty"`
	Note   string    `json:"note,omitempty"`
	Result string    `json:"result,omitempty"`
	// HeadSHA is the dispatched worktree's own HEAD commit, read directly by
	// the estate (main.go, via internal/isolate.Worktree.Head) the moment a
	// role=author turn's subprocess exits -- never anything the subprocess's
	// own output claimed. It is what agent-estate#940's follow-up review
	// found missing from the head-ref join: a branch NAME the estate wrote
	// once (at Create time) but never re-checked, so any actor with push
	// access could rename a branch to `dispatch/<real-id>` and push
	// different content under it. HeadSHA is the estate's own
	// post-completion observation of WHICH commit that worktree actually
	// produced; the gate requires the PR's headRefOid to equal this exact
	// value, not merely that the branch name matches. See internal/gate's
	// package doc for what this does and does not establish.
	HeadSHA string `json:"head_sha,omitempty"`
	// Base is the commit the dispatched worktree started FROM -- for a
	// fresh dispatch, internal/isolate.Worktree.Base (the tip of the
	// caller's checkout at Create time); for a fix pass
	// (internal/isolate.CreateOnBranch), the tip of the pull request's own
	// branch as fetched fresh from origin, before this turn's own commits.
	// Recorded for every role=author turn, same estate-observed-not-agent-
	// claimed discipline as HeadSHA. It is what agent-estate#940's "does not
	// survive a fix pass" follow-up needs: a fix pass's HeadSHA alone proves
	// what that ONE turn produced, but says nothing about whether the code
	// it started from was itself legitimate. Base is not yet consulted by
	// internal/gate's join -- see that package's doc comment for exactly
	// what is and is not checked -- but is recorded now so it exists to
	// check against.
	Base string `json:"base,omitempty"`
}

// EffectiveRole returns the record's Role, defaulting an unset field to
// RoleAuthor. This exists ONLY to keep records written before this field
// existed readable as what they always were -- an authoring turn. A record
// written after this field existed must set Role explicitly; the gate
// package does not use this default for anything dispatched going forward.
func (r Record) EffectiveRole() Role {
	if r.Role == "" {
		return RoleAuthor
	}
	return r.Role
}

type Ledger struct {
	path string
	// explicit records that a caller named this path. A missing file at a
	// DEFAULT path is a first run and legitimately empty; a missing file at a
	// path someone configured is a typo or a wiped state dir, and reporting it
	// as "zero lanes in flight" tells the cap the host is free while agents
	// are running. That direction is fail-open, so it errors instead.
	explicit bool
}

func Open(path string) (*Ledger, error) {
	explicit := path != ""
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
	return &Ledger{path: path, explicit: explicit}, nil
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
		// A missing file is ambiguous: a first run, or a typo/wiped state dir
		// that would report zero lanes in flight while agents are running.
		// The directory settles it mechanically -- if the parent exists, this
		// is a ledger not yet written; if it does not, the path is wrong and
		// reporting "no work in flight" would be fail-open.
		if dir := filepath.Dir(l.path); dir != "" {
			if _, statErr := os.Stat(dir); statErr != nil {
				return nil, fmt.Errorf("ledger: directory for %s does not exist -- refusing to report zero tasks in flight from a path that cannot be right", l.path)
			}
		}
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
