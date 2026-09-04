// Package toolusage answers, for a dispatched turn, which tools its harness
// transcript shows it invoking and how many times.
//
// WHY THIS EXISTS (agent-estate#1096). Everything the knowledge system does
// (#1049, #1080, #1092) is unfalsifiable in production without this: the
// golden set measures whether retrieval CAN find things, and nothing measures
// whether any lane ASKS. The mirror log (internal/mirror) cannot answer this
// -- a live lane's transcript there is progress lines and a final result
// blob, zero tool-call records, confirmed at issue time. The harness's own
// transcript (~/.claude/projects/*/*.jsonl for Claude Code) does carry every
// tool_use block, and agent-estate#990 already put the join key
// (ledger.Record.SessionID) in place to reach it from a turn id. This
// package is the missing consumer -- the same mechanism-without-a-caller gap
// as reclaim (#1029), teardown (#1009) and the corpus links table (#1095).
//
// THE TRAP THIS PACKAGE WAS BUILT TO AVOID. #1096's own history is three
// failed attempts: a regex over raw transcript text counted prose and pasted
// tool OUTPUT as if they were invocations (53 "knowledge query" hits for a
// turn that may have run none); a naive split on `"name":"Bash"` returned 0
// for lanes known to have queried; a third attempt never finished because it
// joined on the ledger's Issue field, whose type the author never checked.
// Parse below walks message.content[].type=="tool_use" blocks structurally
// -- the shape Claude Code's own transcript format uses for every tool call
// -- never a regex over the whole line. The two regexes that do exist here
// (knowledgeQueryPattern, sourceScopePattern) are applied ONLY inside a
// Bash tool_use block's own `command` field, once that block is already
// known to be a real invocation, not over transcript prose.
//
// WHAT THIS PACKAGE NEVER RETURNS. Counts is names and integers only -- no
// command text, no tool output, no transcript excerpt. A turn's transcript
// may contain operator material; emitting anything beyond a count would leak
// it into logs, tests or a PR body. Every exported function here is
// read-only: no write to the ledger, the corpus, or any transcript.
package toolusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

// Counts is one transcript's tool-invocation tally.
type Counts struct {
	// Tools is invocation count per tool name ("Bash", "Read", "Agent", ...),
	// one increment per tool_use content block actually found.
	Tools map[string]int
	// KnowledgeQuery is how many Bash invocations ran a `knowledge query`
	// command. The harness has no dedicated "knowledge" tool -- this is
	// always a Bash call under the hood -- so this is scoped to the Bash
	// tool_use block's own `command` field, never to any other content.
	KnowledgeQuery int
	// KnowledgeQueryScoped is the subset of KnowledgeQuery whose command also
	// carried `source:` scoping (agent-estate#1092's grounding sentence).
	// This is the number that tells us whether #1092 changed behaviour.
	KnowledgeQueryScoped int
	// Lines is how many non-blank JSONL lines were read.
	Lines int
	// Malformed is how many of those lines could not be parsed as JSON.
	// Not fatal -- counted so a caller can tell "zero tool calls found" from
	// "half the file was unreadable".
	Malformed int
}

func newCounts() Counts { return Counts{Tools: map[string]int{}} }

// knowledgeQueryPattern matches a `knowledge query` invocation inside a Bash
// tool_use block's own command field. See the package doc for why this is
// never applied to raw transcript text.
var knowledgeQueryPattern = regexp.MustCompile(`\bknowledge\s+query\b`)

// sourceScopePattern matches the `source:<name>` scoping syntax #1092 named,
// applied to the same already-matched command field.
var sourceScopePattern = regexp.MustCompile(`\bsource:\S`)

// transcriptLine is the subset of one Claude Code transcript JSONL record
// this package reads. Every other field on the real record (role, uuid,
// timestamps, tool_result payloads, ...) is intentionally left unparsed.
type transcriptLine struct {
	Message *struct {
		Content []contentBlock `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type bashInput struct {
	Command string `json:"command"`
}

// Parse walks one transcript file structurally and returns its tool tally.
// A line that fails to parse as JSON is counted in Malformed and skipped --
// it is not treated as fatal, since a truncated or partially-written
// transcript should still report what it could read.
func Parse(path string) (Counts, error) {
	f, err := os.Open(path)
	if err != nil {
		return Counts{}, err
	}
	defer f.Close()

	c := newCounts()
	r := newLineReader(f)
	for {
		line, readErr := r.next()
		if len(line) > 0 {
			c.Lines++
			var tl transcriptLine
			if err := json.Unmarshal(line, &tl); err != nil {
				c.Malformed++
			} else if tl.Message != nil {
				for _, block := range tl.Message.Content {
					if block.Type != "tool_use" || block.Name == "" {
						continue
					}
					c.Tools[block.Name]++
					if block.Name == "Bash" {
						var bi bashInput
						if err := json.Unmarshal(block.Input, &bi); err == nil {
							if knowledgeQueryPattern.MatchString(bi.Command) {
								c.KnowledgeQuery++
								if sourceScopePattern.MatchString(bi.Command) {
									c.KnowledgeQueryScoped++
								}
							}
						}
					}
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	return c, nil
}

// Merge combines several transcripts' counts into one aggregate -- used when
// reporting across a recent window of turns rather than a single one.
func Merge(all []Counts) Counts {
	m := newCounts()
	for _, c := range all {
		for name, n := range c.Tools {
			m.Tools[name] += n
		}
		m.KnowledgeQuery += c.KnowledgeQuery
		m.KnowledgeQueryScoped += c.KnowledgeQueryScoped
		m.Lines += c.Lines
		m.Malformed += c.Malformed
	}
	return m
}

// lineReader reads arbitrarily long lines without the fixed token-size cap
// bufio.Scanner imposes -- a transcript line can carry a large pasted tool
// result and there is no bound this package can safely assume in advance.
type lineReader struct {
	r *bufio.Reader
}

func newLineReader(f *os.File) *lineReader { return &lineReader{r: bufio.NewReader(f)} }

// next returns the next trimmed, non-terminator-suffixed line and an error
// (io.EOF included) exactly as bufio.Reader.ReadBytes reports it -- callers
// must still use a returned line even when err != nil, since the final line
// of a file with no trailing newline arrives that way.
func (l *lineReader) next() ([]byte, error) {
	line, err := l.r.ReadBytes('\n')
	return bytes.TrimSpace(line), err
}

// FindTranscript locates the harness transcript for a session id under root
// (Claude Code's own project directory tree, e.g. ~/.claude/projects) -- one
// file per session, named "<session-id>.jsonl", nested under a
// per-working-directory folder whose name this package never needs to
// decode. Returns an error if none or more than one is found; more than one
// would mean two different working directories produced the same handle,
// which should never happen and is worth refusing on rather than guessing.
func FindTranscript(root, sessionID string) (string, error) {
	if sessionID == "" {
		return "", errors.New("toolusage: empty session id")
	}
	want := sessionID + ".jsonl"
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is not fatal to the rest of the walk --
			// skip it rather than failing the whole lookup.
			return nil
		}
		if !d.IsDir() && d.Name() == want {
			found = append(found, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("toolusage: no transcript found for session %s under %s", sessionID, root)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("toolusage: %d transcripts found for session %s under %s -- ambiguous, refusing to guess", len(found), sessionID, root)
	}
}

// DefaultTranscriptsRoot is Claude Code's own transcript directory. Codex
// transcripts, if this is ever extended to that harness, live elsewhere and
// are out of scope for this package today (agent-estate#1096 only verified
// the Claude Code shape).
func DefaultTranscriptsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// Resolve finds a turn's ledger record by its dispatch id. This joins on
// ledger.Record.ID, a plain string on every version of this schema -- the
// join-key type hazard #1096 flagged (its third attempt broke joining on
// Issue) does not apply here because ID, not Issue, is what identifies a
// turn.
func Resolve(records []ledger.Record, turnID string) (ledger.Record, error) {
	for _, r := range records {
		if r.ID == turnID {
			return r, nil
		}
	}
	return ledger.Record{}, fmt.Errorf("toolusage: no ledger record for turn %q", turnID)
}

// RecentWithSession returns up to n of the most recently completed
// (State.Terminal()) records that carry a recorded, non-empty SessionID,
// most recent first. records is assumed sorted oldest-first, the order
// ledger.Ledger.Current() returns.
func RecentWithSession(records []ledger.Record, n int) []ledger.Record {
	var out []ledger.Record
	for i := len(records) - 1; i >= 0 && len(out) < n; i-- {
		r := records[i]
		if r.State.Terminal() && r.SessionID != nil && *r.SessionID != "" {
			out = append(out, r)
		}
	}
	return out
}
