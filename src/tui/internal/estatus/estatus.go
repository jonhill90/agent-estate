// Package estatus reads the state the Go estate actually keeps: the dispatch
// ledger written by src/estate's `estate dispatch`, and the Director's own
// tick log.
//
// WHY THIS EXISTS. Everything else under internal/ in this module reads the
// deleted Python supervisor through MCP. Nothing in the TUI reads the Go
// estate at all, so the viewer describes a backend that no longer exists.
// This is the first reader pointed at the live one.
//
// ABSENCE IS TYPED, and here that is not a style preference. At the time this
// was written no dispatch had ever been recorded and the ledger file did not
// exist. A reader that returned an empty list for that case would render an
// empty screen identical to a working estate with nothing running -- the exact
// failure this codebase names as the one it produces most. So every field that
// can be unavailable says which it is: absent, unreadable, or genuinely empty.
package estatus

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

// Dispatch is one recorded state transition of an agent turn. It mirrors
// src/estate/internal/ledger.Record; the two modules do not share types, so
// this decodes the same JSON rather than importing across the module
// boundary. Only the fields agent-facing views can honestly use are decoded
// here -- PR, Result and HeadSHA exist on the source record too but nothing
// in this module reads them yet; add them when something does, rather than
// carrying fields nobody derives from.
type Dispatch struct {
	ID    string    `json:"id"`
	Issue string    `json:"issue"`
	Lane  string    `json:"lane"`
	// Role is the turn's role at dispatch time -- "author" or "reviewer"
	// (src/estate/internal/ledger.Role's own two constants). Empty on a
	// record written before this field existed; a reader must treat that
	// the same way src/estate's own Record.EffectiveRole does (default to
	// "author"), not as evidence of a third role.
	Role  string    `json:"role,omitempty"`
	State string    `json:"state"`
	At    time.Time `json:"at"`
	// PID is the dispatched subprocess's own process id, recorded the
	// moment src/estate's `estate dispatch` observed it start
	// (agent-estate#944) -- 0 when this record predates that PR or the
	// process failed to start before a pid was ever assigned. A live pid
	// does not by itself prove the process is still running (pids get
	// reused -- see agent-estate#944's own reclaim package for the check that does),
	// so this is identifying information, not a liveness signal.
	PID  int    `json:"pid,omitempty"`
	Note string `json:"note,omitempty"`
}

// worktreeNotePrefix is the exact literal src/estate's dispatch path writes
// into a Dispatched record's Note (main.go: `Note: "worktree " + wt.Path`)
// -- the only place a turn's worktree path is recorded at all; there is no
// dedicated ledger field for it. Matching this one fixed prefix on a
// specific state is reading a value the estate deliberately wrote down, not
// pattern-matching pane content the way this codebase's own "failure mode"
// section warns against -- but it is still free text, not a typed field, so
// Worktree (below) fails closed: any record that does not carry exactly
// this shape reports not-ok rather than guessing.
const worktreeNotePrefix = "worktree "

// Worktree returns the worktree path src/estate recorded for this dispatch,
// and whether one was found. Only ever true for a record whose Note was
// written by the exact `"worktree " + path` convention above -- a record
// with a different Note (a failure reason, a reclaim note, or nothing) is
// reported not-ok, never a guessed or empty path.
func (d Dispatch) Worktree() (string, bool) {
	if !strings.HasPrefix(d.Note, worktreeNotePrefix) {
		return "", false
	}
	path := strings.TrimPrefix(d.Note, worktreeNotePrefix)
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	return path, true
}

// Tick is one entry of the Director's tick log (docs/tick-log.jsonl).
type Tick struct {
	At        string `json:"at"`
	PhaseItem string `json:"phase_item"`
	SrcHead   string `json:"src_head"`
	// Artifact is nil when the tick produced nothing a human can look at.
	Artifact *string `json:"artifact"`
}

// HasArtifact reports whether this tick produced something. An empty string
// is the same absence as null.
func (t Tick) HasArtifact() bool {
	return t.Artifact != nil && strings.TrimSpace(*t.Artifact) != ""
}

// Availability distinguishes the three ways a source can fail to give data.
type Availability int

const (
	// Present: we read the source and these are its contents, empty or not.
	Present Availability = iota
	// Absent: the source does not exist. For a ledger that means no dispatch
	// has ever been recorded -- a true first-run state, not a fault.
	Absent
	// Unreadable: the source exists and we could not read it. Never render
	// this as empty.
	Unreadable
)

func (a Availability) String() string {
	switch a {
	case Present:
		return "present"
	case Absent:
		return "absent"
	default:
		return "unreadable"
	}
}

// Status is the estate's own state as this module can see it.
type Status struct {
	LedgerPath string
	Ledger     Availability
	LedgerErr  error
	Dispatches []Dispatch // latest record per task id, newest first
	InFlight   []Dispatch // those still occupying a slot

	TickPath string
	Ticks    Availability
	TickErr  error
	LastTick *Tick
	TickRuns int
}

// inFlight are the states that still occupy a slot. "unknown" is deliberately
// among them: src/estate records a turn it could not observe as unknown, which
// is not failed and must not free a slot.
var inFlightStates = map[string]bool{"dispatched": true, "unknown": true}

// Read gathers the estate's state from the two files it keeps.
//
// It never returns an error: a caller rendering a dashboard needs to draw
// something for every source, and which source failed is part of what it must
// draw. Failures land in the per-source Availability and Err fields.
func Read(ledgerPath, tickPath string) Status {
	s := Status{LedgerPath: ledgerPath, TickPath: tickPath}
	s.Ledger, s.LedgerErr, s.Dispatches = readDispatches(ledgerPath)
	for _, d := range s.Dispatches {
		if inFlightStates[d.State] {
			s.InFlight = append(s.InFlight, d)
		}
	}
	s.Ticks, s.TickErr, s.LastTick, s.TickRuns = readTicks(tickPath)
	return s
}

// ReadLedger is Read narrowed to the ledger half only, for a caller that has
// no tick log to report against (agent-estate#930: the Dashboard/Agents/
// rail/Chat panes describe dispatched turns, never the Director's own tick
// cadence -- Read's tick-log reporting has no meaning for them, and passing
// it an empty tickPath would misreport Ticks as Unreadable for a question
// nobody asked). Returns exactly the two fields Read would have set from
// the ledger side: availability plus the newest-per-id records, in the
// same InFlight subset Status.InFlight already keys off.
func ReadLedger(ledgerPath string) (Availability, error, []Dispatch, []Dispatch) {
	avail, err, dispatches := readDispatches(ledgerPath)
	var inFlight []Dispatch
	for _, d := range dispatches {
		if inFlightStates[d.State] {
			inFlight = append(inFlight, d)
		}
	}
	return avail, err, dispatches, inFlight
}

// ReadWithTickErr is Read for a caller that already tried to resolve the
// tick log's path itself and failed -- e.g. cmd/estate's resolveTickLogPath
// cannot derive the repository root under a `go build -trimpath` binary,
// where runtime.Caller(0) yields a module-relative path instead of an
// absolute one.
//
// tickResolveErr carries that failure through the seam AS A VALUE, the same
// discipline this package's doc comment names for absence generally
// (cost.Figure.Known and session.Worktree.Clean elsewhere in this repo are
// the same pattern). The alternative -- resolveTickLogPath returning a
// path deliberately built not to exist, so this package's own file-open
// fails and reports Absent -- was the actual bug: Absent renders "the loop
// has not recorded a tick, or is not running", a claim about the Director,
// when the true situation is that THIS INSTRUMENT could not even work out
// where to look. That is Unreadable's claim, not Absent's, and the only way
// to land on Unreadable honestly here is to be told the resolution failed
// rather than to infer it from a file that predictably isn't there.
//
// When tickResolveErr is nil this is exactly Read; tickPath is read
// normally. When it is non-nil, the tick side is reported Unreadable with
// tickResolveErr as the reason and the file at tickPath (if a caller
// supplied one anyway) is never opened -- ledger reading is unaffected.
func ReadWithTickErr(ledgerPath, tickPath string, tickResolveErr error) Status {
	s := Read(ledgerPath, tickPath)
	if tickResolveErr != nil {
		s.Ticks = Unreadable
		s.TickErr = tickResolveErr
		s.LastTick = nil
		s.TickRuns = 0
	}
	return s
}

// readDispatches returns the LATEST record per task id, newest first. The
// ledger is append-only: a task appears once per state transition, and the
// last line for an id is its current state.
func readDispatches(path string) (Availability, error, []Dispatch) {
	lines, avail, err := readLines(path)
	if avail != Present {
		return avail, err, nil
	}
	latest := map[string]Dispatch{}
	order := []string{}
	for n, line := range lines {
		var d Dispatch
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			return Unreadable, fmt.Errorf("%s line %d: %w", path, n+1, err), nil
		}
		if _, seen := latest[d.ID]; !seen {
			order = append(order, d.ID)
		}
		latest[d.ID] = d
	}
	out := make([]Dispatch, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		out = append(out, latest[order[i]])
	}
	return Present, nil, out
}

func readTicks(path string) (Availability, error, *Tick, int) {
	lines, avail, err := readLines(path)
	if avail != Present {
		return avail, err, nil, 0
	}
	var last *Tick
	for n, line := range lines {
		var t Tick
		if err := json.Unmarshal([]byte(line), &t); err != nil {
			return Unreadable, fmt.Errorf("%s line %d: %w", path, n+1, err), nil, 0
		}
		cp := t
		last = &cp
	}
	return Present, nil, last, len(lines)
}

func readLines(path string) ([]string, Availability, error) {
	if strings.TrimSpace(path) == "" {
		return nil, Unreadable, errors.New("no path configured")
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Absent, nil
	}
	if err != nil {
		return nil, Unreadable, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			lines = append(lines, t)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, Unreadable, err
	}
	return lines, Present, nil
}

// Lines renders the status as plain text for a caller to place. Rendering
// lives here, not in the shell, so it can be tested without a terminal.
//
// Every branch names WHICH of the three availabilities it is drawing. A pane
// that prints "0 dispatches" for an absent ledger and for a working idle one
// is the instrument failure this package's doc comment describes.
func Lines(s Status) []string {
	out := []string{"The Estate", ""}

	switch s.Ledger {
	case Absent:
		out = append(out,
			"Dispatches: none recorded yet.",
			"  "+s.LedgerPath+" does not exist -- no turn has ever been dispatched.",
			"  That is a first-run state, not a fault, and not an idle estate.")
	case Unreadable:
		out = append(out,
			"Dispatches: UNREADABLE -- this is not zero.",
			"  "+errText(s.LedgerErr))
	default:
		out = append(out, fmt.Sprintf("Dispatches: %d task(s), %d still holding a slot.", len(s.Dispatches), len(s.InFlight)))
		for i, d := range s.InFlight {
			if i == 5 {
				out = append(out, fmt.Sprintf("  ... and %d more", len(s.InFlight)-5))
				break
			}
			out = append(out, fmt.Sprintf("  %-24s %-11s %s", d.ID, d.State, d.Issue))
		}
	}

	out = append(out, "")

	switch s.Ticks {
	case Absent:
		out = append(out,
			"Director: no tick log at "+s.TickPath+".",
			"  The loop has not recorded a tick, or is not running.")
	case Unreadable:
		out = append(out,
			"Director: tick log UNREADABLE -- the stop condition cannot be evaluated.",
			"  "+errText(s.TickErr))
	default:
		if s.LastTick == nil {
			out = append(out, "Director: tick log is empty -- no tick recorded yet.")
			break
		}
		art := "no artifact"
		if s.LastTick.HasArtifact() {
			art = *s.LastTick.Artifact
		}
		out = append(out,
			fmt.Sprintf("Director: %d tick(s); last on %s", s.TickRuns, s.LastTick.PhaseItem),
			"  "+s.LastTick.At+"  src "+short(s.LastTick.SrcHead)+"  -> "+art)
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return "no reason recorded"
	}
	return err.Error()
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	if sha == "" {
		return "(none)"
	}
	return sha
}
