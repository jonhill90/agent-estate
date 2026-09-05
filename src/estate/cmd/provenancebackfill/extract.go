// Command provenancebackfill is agent-estate#1139's A3 (revised): attribute
// EXISTING Claude rows in the corpus's prompts table with the merged
// provenance identity internal/provenance.UnitProvenance already declares
// (harness, source file, session id, record index, content hash). This file
// is the read-only extraction half: walk a Claude transcript root, apply a
// fixed watermark, and produce candidate units. It never opens the corpus
// database -- attribute.go does that, and only against the copy the caller
// names on the command line.
//
// # Identifying a "Claude row" with no harness column
//
// prompts has no harness column (checked against the live schema before
// writing this: `select sql from sqlite_master where name='prompts'` carries
// no such field). A row's source_file is only ever a transcript's own
// basename (e.g. "c5aa6462-...-4e26d.jsonl"), never a full path, so a Claude
// row cannot be told apart from a Codex row by column value alone. This tool
// identifies a Claude row structurally instead: it walks the real Claude
// transcript root (~/.claude/projects/**/*.jsonl) once, builds a
// basename -> full path map, and treats a prompts row a "Claude candidate"
// exactly when its source_file matches a key in that map. A basename that
// resolves to more than one real file (two projects producing colliding
// session ids -- not observed, but not provably impossible) is excluded
// outright and reported, rather than guessed at.
//
// # Positive filter (mirrors internal/rollout's discipline for Codex)
//
// A genuine operator turn in a Claude transcript line is:
//
//	type == "user" AND message.role == "user" AND message.content is a
//	JSON STRING (never an array -- an array shape is Claude's own
//	encoding for a tool_result being handed back, not something the
//	operator typed).
//
// This is a positive filter, the same discipline agent-estate#1139 required
// of the Codex extractor: it states what IS a genuine turn rather than
// excluding what obviously is not, so a future content shape this tool has
// never seen is excluded by default rather than silently admitted.
//
// # RecordIndex matches internal/provenance's contract, not corpusextract's
//
// internal/provenance.UnitProvenance.RecordIndex is documented as "0-based
// ordinal position among units extracted from SourceFile, in file order" --
// NOT a raw line number. cmd/corpusextract's own ManifestEntry.RecordIndex
// is a different, local field (1-based raw line number) belonging to that
// command's own type, not to the shared contract; using it as a model here
// would have been the semantic-contract collision this task's brief warns
// about. This file's RecordIndex counts only extracted units, 0-based,
// exactly as internal/provenance documents.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/provenance"
)

// Unit is one candidate extracted from a Claude transcript file, carrying
// both its identity (Provenance) and the raw text ContentHash was computed
// over -- kept in memory only long enough to compute a hash and compare
// against the corpus's own text_raw; never written to disk or logged.
type Unit struct {
	Provenance provenance.UnitProvenance
	Text       string
}

// ClaudeRecord is the subset of one Claude transcript JSONL line's shape
// this tool reads. Extra fields on the real record are ignored by design --
// this tool only needs enough to apply the positive filter above.
type ClaudeRecord struct {
	Type      string         `json:"type"`
	SessionID string         `json:"sessionId"`
	Message   *claudeMessage `json:"message"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ParseIssue names one file this tool could not fully read, and why -- a
// file is never silently dropped from accounting (agent-estate#1139's
// standing "never a silent zero" rule, restated in this task's brief as
// "skipped WITH REASONS").
type ParseIssue struct {
	Path   string
	Reason string
}

// FindClaudeFiles walks root and returns a basename -> full path map for
// every *.jsonl file found, plus the list of basenames that collided across
// more than one full path (excluded from the map entirely, never guessed
// at).
func FindClaudeFiles(root string) (map[string]string, []string, error) {
	byBase := map[string]string{}
	seen := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		base := filepath.Base(path)
		seen[base] = append(seen[base], path)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	var collisions []string
	for base, paths := range seen {
		if len(paths) == 1 {
			byBase[base] = paths[0]
			continue
		}
		collisions = append(collisions, base)
	}
	sort.Strings(collisions)
	return byBase, collisions, nil
}

// ExtractFile reads one Claude transcript file top to bottom and returns
// every genuine operator turn as a Unit, in file order. A line that is not
// valid JSON, or valid JSON with no "type" field, is counted and skipped --
// reported to the caller, never silently absorbed into "zero found".
func ExtractFile(path string) ([]Unit, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)

	var units []Unit
	ordinal := 0
	malformed := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var rec ClaudeRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			malformed++
			continue
		}
		if rec.Type != "user" || rec.Message == nil || rec.Message.Role != "user" {
			continue
		}
		var text string
		if err := json.Unmarshal(rec.Message.Content, &text); err != nil {
			// content is not a bare JSON string -- e.g. a tool_result array.
			// Excluded by the positive filter, not an error.
			continue
		}
		u := Unit{
			Text: text,
			Provenance: provenance.UnitProvenance{
				SourceName:  "claude-transcript",
				Harness:     "claude",
				SourceFile:  path,
				SessionID:   rec.SessionID,
				RecordIndex: ordinal,
				ContentHash: provenance.HashContent(text),
			},
		}
		units = append(units, u)
		ordinal++
	}
	if err := scanner.Err(); err != nil {
		return units, malformed, err
	}
	return units, malformed, nil
}

// WatermarkPlan is the result of applying ONE fixed watermark to every file
// FindClaudeFiles found: which files are eligible to read, which are
// excluded because they changed after the watermark, and which basenames
// were ambiguous collisions.
type WatermarkPlan struct {
	Watermark    time.Time
	Eligible     map[string]string // basename -> full path
	Excluded     []string          // full paths, changed after watermark
	Collisions   []string          // basenames colliding across >1 path
	StatFailures []ParseIssue
}

// PlanWatermark takes the ONE recorded watermark and classifies every file
// FindClaudeFiles saw against it. Nothing here re-reads the tree after this
// call returns -- the caller must not call FindClaudeFiles or PlanWatermark
// a second time within the same run and expect the same answer forever
// (a live tree can keep changing); this function's job is only to freeze
// today's set of file states as of the ONE stat pass it performs.
func PlanWatermark(byBase map[string]string, collisions []string, watermark time.Time) WatermarkPlan {
	plan := WatermarkPlan{
		Watermark:  watermark,
		Eligible:   map[string]string{},
		Collisions: collisions,
	}
	bases := make([]string, 0, len(byBase))
	for b := range byBase {
		bases = append(bases, b)
	}
	sort.Strings(bases)
	for _, base := range bases {
		path := byBase[base]
		info, err := os.Stat(path)
		if err != nil {
			plan.StatFailures = append(plan.StatFailures, ParseIssue{Path: path, Reason: err.Error()})
			continue
		}
		if info.ModTime().After(watermark) {
			plan.Excluded = append(plan.Excluded, path)
			continue
		}
		plan.Eligible[base] = path
	}
	sort.Strings(plan.Excluded)
	return plan
}

func defaultClaudeRoot() (string, error) {
	if p := os.Getenv("PROVENANCE_CLAUDE_ROOT"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}
