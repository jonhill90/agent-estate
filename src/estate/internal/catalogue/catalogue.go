// Package catalogue is the durable answer to "what sources does this estate
// ingest from, and what is known about each?" (agent-estate#1139 gate 5).
//
// A Source is a record, not a store: name, harness, root path, which fields
// identify one of its units, a current typed health state, and the unit
// count last observed together with the instant it was observed at -- the
// sources this package reads are the operator's own LIVE, growing
// conversation history, so any count is a snapshot, never a fact that holds
// after the moment it was taken.
//
// This package never writes to a source. Every read is a directory listing,
// an os.Stat, or (for Codex rollouts specifically) the same read-only
// internal/rollout parse cmd/capturehealth already uses -- os.Open only,
// never O_RDWR, never os.Create, never os.Remove, and nothing here calls
// os.Chtimes. It also never opens ~/corpus/ledger.sqlite3: a catalogue of
// sources is not itself a corpus query.
//
// Two sources are seeded because two are real: Codex rollout JSONL under
// ~/.codex/sessions, and Claude Code's own session transcripts under
// ~/.claude/projects/*/*.jsonl. Neither existing on this machine is not this
// package's problem to paper over -- a missing root is recorded as
// HealthMissing with the exact path looked for, never silently omitted.
package catalogue

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/rollout"
)

// HealthState is the four-state vocabulary this package reports every
// source's health in. It is deliberately not a bare bool or a zero value:
// a source this package could not even find is a different fact from one it
// found but could not read, which is a different fact again from one it
// read successfully and found to hold nothing.
//
// This vocabulary was not yet on main (github.com/jonhill90/agent-estate
// @f480032) when this package was written -- cmd/capturehealth and
// internal/codexrollout, the two sibling K2 slices, report counts but do not
// classify a source into named health states. This is this package's own
// typed enum, not a reuse of an existing one; if a shared vocabulary lands
// on main later, migrate this package onto it rather than keeping two.
type HealthState int

const (
	// HealthMissing means the root path does not exist at all.
	HealthMissing HealthState = iota
	// HealthUnreadable means the root path exists but this package could not
	// list or read it (e.g. permission denied).
	HealthUnreadable
	// HealthEmpty means the root path exists, was read successfully, and
	// contains zero units.
	HealthEmpty
	// HealthPopulated means the root path exists, was read successfully, and
	// contains at least one unit.
	HealthPopulated
)

// String renders the health state the same way in JSON (via MarshalJSON)
// and in any human-facing output, so the two never drift apart.
func (h HealthState) String() string {
	switch h {
	case HealthMissing:
		return "Missing"
	case HealthUnreadable:
		return "Unreadable"
	case HealthEmpty:
		return "Empty"
	case HealthPopulated:
		return "Populated"
	default:
		return "Unknown"
	}
}

// MarshalJSON emits the health state as its name, never its underlying int
// -- a bare 0/1/2/3 in the catalogue JSON would silently break the moment
// this list is reordered.
func (h HealthState) MarshalJSON() ([]byte, error) {
	return []byte(`"` + h.String() + `"`), nil
}

// Source is one durable record: everything this package knows about one
// ingestion source, as of ObservedAt.
type Source struct {
	// Name is a short, stable identifier for this source, e.g. "codex-rollouts".
	Name string `json:"name"`
	// Harness is the harness this source belongs to -- "claude" or "codex".
	Harness string `json:"harness"`
	// RootPath is the directory this source's units live under.
	RootPath string `json:"root_path"`
	// IdentityFields names which fields, together, identify one unit of this
	// source -- e.g. which JSON field(s) a caller must key on to tell two
	// units apart or recognize the same unit seen twice.
	IdentityFields []string `json:"identity_fields"`
	// Health is this source's current typed health state.
	Health HealthState `json:"health"`
	// UnitCount is how many units were observed the last time this source
	// was read. Meaningless without ObservedAt alongside it -- see that
	// field's own comment.
	UnitCount int `json:"unit_count"`
	// ObservedAt is the instant UnitCount was measured. The sources this
	// package reads are live and growing; a UnitCount with no attached
	// instant is a claim with no expiry, which is worse than no claim at
	// all. Zero value (time.Time{}) means this source was never
	// successfully read (Health is Missing or Unreadable) and UnitCount is
	// not meaningful.
	ObservedAt time.Time `json:"observed_at"`
	// Detail carries a short, human-readable note -- why a source is
	// Missing or Unreadable, or what "unit" means for a Populated one. Never
	// omitted just because a source is healthy: a Populated source's Detail
	// says what was counted, so a reader does not have to infer it from
	// IdentityFields alone.
	Detail string `json:"detail"`
}

// Catalogue is every known source, as of the moment Build ran.
type Catalogue struct {
	Sources []Source `json:"sources"`
}

// DefaultCodexRoot is ~/.codex/sessions, the root cmd/capturehealth already
// reads.
func DefaultCodexRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

// DefaultClaudeRoot is ~/.claude/projects: one subdirectory per project, one
// *.jsonl file per session, named `<sessionId>.jsonl`.
func DefaultClaudeRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// Build returns the catalogue seeded with the estate's two real sources,
// each read once, read-only, right now.
func Build() Catalogue {
	return Catalogue{
		Sources: []Source{
			BuildCodexSource(DefaultCodexRoot()),
			BuildClaudeSource(DefaultClaudeRoot()),
		},
	}
}

// BuildCodexSource inspects one Codex rollout root and returns its Source
// record. root is a parameter (not hardcoded to defaultCodexRoot) so a
// caller -- test or CLI flag -- can point this at a fixture tree.
//
// Unit = one genuine operator turn (internal/rollout.GenuineOperatorTurn:
// role=="user" AND content[0].type=="input_text"), the same predicate
// cmd/capturehealth already reports on. Identity = the session it belongs
// to (session_meta.payload.id) plus the line position within its file --
// internal/rollout's own doc comment names why that pair, not payload.id
// alone, is what this format actually supports: no record type after
// session_meta carries an explicit session id of its own.
func BuildCodexSource(root string) Source {
	src := Source{
		Name:           "codex-rollouts",
		Harness:        "codex",
		RootPath:       root,
		IdentityFields: []string{"session_meta.payload.id (session)", "line position within file (turn ordinal)"},
	}

	if root == "" {
		src.Health = HealthMissing
		src.Detail = "could not resolve a home directory to build the default root"
		return src
	}

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			src.Health = HealthMissing
			src.Detail = "root path does not exist: " + root
			return src
		}
		src.Health = HealthUnreadable
		src.Detail = "os.Stat failed: " + err.Error()
		return src
	}
	if !info.IsDir() {
		src.Health = HealthUnreadable
		src.Detail = "root path exists but is not a directory: " + root
		return src
	}

	files, err := rollout.WalkRolloutFiles(root)
	if err != nil {
		src.Health = HealthUnreadable
		src.Detail = "could not walk root: " + err.Error()
		return src
	}
	if len(files) == 0 {
		src.Health = HealthEmpty
		src.ObservedAt = time.Now()
		src.Detail = "root exists and is readable; zero *.jsonl rollout files found"
		return src
	}

	total := 0
	unreadable := 0
	for _, path := range files {
		fa, err := rollout.AnalyzeFile(path)
		if err != nil {
			unreadable++
			continue
		}
		total += fa.OperatorTurns
	}
	src.ObservedAt = time.Now()
	if total == 0 && unreadable == len(files) {
		src.Health = HealthUnreadable
		src.Detail = "root exists; every rollout file failed to parse"
		return src
	}
	if total == 0 {
		src.Health = HealthEmpty
		src.Detail = "root exists and is readable; zero genuine operator turns found across files"
		return src
	}
	src.Health = HealthPopulated
	src.UnitCount = total
	src.Detail = "counted genuine operator turns (role=user, content[0].type=input_text) across " +
		strconv.Itoa(len(files)) + " rollout files"
	if unreadable > 0 {
		src.Detail += "; " + strconv.Itoa(unreadable) + " file(s) failed to parse and were excluded from the count"
	}
	return src
}

// BuildClaudeSource inspects one Claude Code transcript root and returns its
// Source record. root is a parameter for the same reason BuildCodexSource's
// is.
//
// Unit = one session transcript file. Observed on the live tree: a project
// subdirectory holds one *.jsonl file per session, named `<sessionId>.jsonl`
// -- e.g. ~/.claude/projects/-private-tmp/0aca25a9-...-e0e.jsonl -- and the
// file's own records repeat that same id in a "sessionId" field on nearly
// every line. Counting files rather than parsing each one's contents keeps
// this a directory listing, not a read of the operator's actual words, and
// needs no os.Open at all: only os.Stat and directory reads, so there is
// nothing here to leave an mtime changed.
func BuildClaudeSource(root string) Source {
	src := Source{
		Name:           "claude-transcripts",
		Harness:        "claude",
		RootPath:       root,
		IdentityFields: []string{"sessionId (also the file's own name, <sessionId>.jsonl)"},
	}

	if root == "" {
		src.Health = HealthMissing
		src.Detail = "could not resolve a home directory to build the default root"
		return src
	}

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			src.Health = HealthMissing
			src.Detail = "root path does not exist: " + root
			return src
		}
		src.Health = HealthUnreadable
		src.Detail = "os.Stat failed: " + err.Error()
		return src
	}
	if !info.IsDir() {
		src.Health = HealthUnreadable
		src.Detail = "root path exists but is not a directory: " + root
		return src
	}

	projects, err := os.ReadDir(root)
	if err != nil {
		src.Health = HealthUnreadable
		src.Detail = "could not list root: " + err.Error()
		return src
	}

	count := 0
	for _, proj := range projects {
		if !proj.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, proj.Name()))
		if err != nil {
			// One unreadable project subdirectory does not make the whole
			// source Unreadable -- report what could be read, same as
			// BuildCodexSource excludes individual unparseable files rather
			// than failing the whole source.
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".jsonl" {
				count++
			}
		}
	}

	src.ObservedAt = time.Now()
	if count == 0 {
		src.Health = HealthEmpty
		src.Detail = "root exists and is readable; zero *.jsonl session files found under any project subdirectory"
		return src
	}
	src.Health = HealthPopulated
	src.UnitCount = count
	src.Detail = "counted *.jsonl session transcript files across " + strconv.Itoa(len(projects)) + " project subdirectories"
	return src
}
