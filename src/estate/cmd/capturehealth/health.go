// Command capturehealth is the K2-pilot per-source capture-health report for
// agent-estate#1139: it walks ~/.codex/sessions rollout JSONL and reports what
// is there -- record type counts, extractable operator-turn counts, and
// unparseable files -- WITHOUT writing anything anywhere. It ingests nothing
// into the corpus and never opens ~/corpus/ledger.sqlite3.
//
// agent-estate#1139's verified sweep names three corrections this file exists
// to encode, not merely to satisfy once:
//
//  1. The session id lives at session_meta.payload.id, never payload.session_id
//     -- there is no session_id field at all.
//  2. role=="developer" exists and is NOT a human/operator turn. genuineOperatorTurn
//     below filters POSITIVELY (role=="user" AND content[0].type=="input_text"),
//     never negatively as "not assistant" -- a negative filter would count an
//     injected developer instruction as the operator's own words.
//  3. event_msg and turn_context records exist alongside response_item and
//     session_meta, and the real tree also carries world_state, compacted, and
//     inter_agent_communication_metadata records this issue's single-file sample
//     never saw. recordTypeCounts is keyed by whatever top-level "type" string
//     is actually present in the file, not a hardcoded enum, so a fifth record
//     type showing up in a future rollout is counted, not silently dropped.
//
// This binary reads every file with os.Open (never O_RDWR, never os.Create,
// never os.Remove) and never calls os.Chtimes or anything else that could
// change an mtime under ~/.codex -- that tree is the operator's own
// conversation history and is evidence, not a scratch directory.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// rawRecord is the top-level shape shared by every rollout JSONL line:
// {"timestamp": "...", "type": "...", "payload": {...}}. payload is left as
// raw bytes because its shape depends entirely on "type", and this report
// must not assume it knows every shape that will ever appear (correction 3).
type rawRecord struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// responseItemPayload is response_item's payload shape. Only "message"
// payloads (as opposed to "reasoning", "function_call", "function_call_output")
// carry a Role at all.
type responseItemPayload struct {
	Type    string        `json:"type"`
	Role    string        `json:"role"`
	Content []contentItem `json:"content"`
}

type contentItem struct {
	Type string `json:"type"`
}

// sessionMetaPayload is session_meta's payload shape. Field name is "id",
// never "session_id" (correction 1) -- there is no session_id field to
// mistakenly read here.
//
// Only ID is decoded typed. payload.originator/source/cwd are present in the
// real tree but their shape is NOT stable: a forked/subagent session's
// payload.source is an object ({"subagent":{"thread_spawn":{...}}}), not the
// plain string a top-level CLI session carries. This report does not need
// those fields, so it does not decode them at all -- decoding them typed
// previously made a schema variation in an unused field fail the WHOLE file,
// including its genuine operator turns, which is worse than not reading the
// field in the first place.
type sessionMetaPayload struct {
	ID string `json:"id"`
}

// FileReport is one rollout file's own counts.
type FileReport struct {
	Path string `json:"path"`

	// SessionIDs are every distinct session_meta.payload.id seen in this
	// file, in first-seen order. Usually one; a compacted/resumed session
	// can carry more than one session_meta record.
	SessionIDs []string `json:"session_ids"`

	// OperatorTurns is the count of genuine operator turns: response_item
	// records with payload.role=="user" AND payload.content[0].type==
	// "input_text". This is the ONLY thing this report treats as the
	// operator's own words (correction 2).
	OperatorTurns int `json:"operator_turns"`

	// RecordTypeCounts is keyed by the record's own top-level "type" field,
	// whatever string that turns out to be (correction 3).
	RecordTypeCounts map[string]int `json:"record_type_counts"`

	// RoleCounts is keyed by response_item message payloads' "role" field
	// ("user", "assistant", "developer", or whatever else appears).
	// Required separately from OperatorTurns so "developer" and "assistant"
	// are visible as their own counts, never folded into "not a genuine
	// operator turn" as one bucket.
	RoleCounts map[string]int `json:"role_counts"`

	// UserContentTypeCounts is keyed by content[0].type for role=="user"
	// records specifically -- distinguishes "input_text" (genuine) from
	// "input_image" and anything else, so a user-role record that is NOT
	// a genuine operator turn is still visible rather than vanishing.
	UserContentTypeCounts map[string]int `json:"user_content_type_counts"`

	LineCount int `json:"line_count"`
}

func newFileReport(path string) FileReport {
	return FileReport{
		Path:                  path,
		RecordTypeCounts:      map[string]int{},
		RoleCounts:            map[string]int{},
		UserContentTypeCounts: map[string]int{},
	}
}

// ParseFailure names one file this report could not read as rollout JSONL,
// and why -- never skipped silently.
type ParseFailure struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Report is the full aggregate this tool produces.
type Report struct {
	Root               string         `json:"root"`
	FilesTotal         int            `json:"files_total"`
	FilesParsed        int            `json:"files_parsed"`
	FilesUnparseable   []ParseFailure `json:"files_unparseable"`
	OperatorTurnsTotal int            `json:"operator_turns_total"`

	RecordTypeCounts      map[string]int `json:"record_type_counts"`
	RoleCounts            map[string]int `json:"role_counts"`
	UserContentTypeCounts map[string]int `json:"user_content_type_counts"`

	Files []FileReport `json:"files"`
}

// genuineOperatorTurn is the ONE positive predicate this report treats as an
// extractable operator turn. It never checks "role != assistant" -- see the
// package comment's correction 2.
func genuineOperatorTurn(p responseItemPayload) bool {
	if p.Role != "user" {
		return false
	}
	if len(p.Content) == 0 {
		return false
	}
	return p.Content[0].Type == "input_text"
}

// analyzeFile reads one rollout JSONL file top to bottom with a plain
// buffered read (os.Open only -- no write mode, no truncate) and returns its
// own FileReport. A line that fails to unmarshal as the shared rawRecord
// shape fails the WHOLE file: it is reported as unparseable with the line
// number and the json error, and contributes nothing to any aggregate count,
// rather than silently counting only the lines before the break.
func analyzeFile(path string) (FileReport, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileReport{}, err
	}
	defer f.Close()

	report := newFileReport(path)
	seenSession := map[string]bool{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec rawRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return FileReport{}, fmt.Errorf("line %d: %w", lineNo, err)
		}
		report.LineCount++
		report.RecordTypeCounts[rec.Type]++

		switch rec.Type {
		case "response_item":
			var p responseItemPayload
			if err := json.Unmarshal(rec.Payload, &p); err != nil {
				return FileReport{}, fmt.Errorf("line %d: response_item payload: %w", lineNo, err)
			}
			if p.Type != "message" || p.Role == "" {
				// reasoning / function_call / function_call_output payloads
				// carry no role at all -- not a role to tally, and never
				// counted as an operator turn.
				continue
			}
			report.RoleCounts[p.Role]++
			if p.Role == "user" {
				ct := "(missing)"
				if len(p.Content) > 0 {
					ct = p.Content[0].Type
				}
				report.UserContentTypeCounts[ct]++
			}
			if genuineOperatorTurn(p) {
				report.OperatorTurns++
			}
		case "session_meta":
			var p sessionMetaPayload
			if err := json.Unmarshal(rec.Payload, &p); err != nil {
				return FileReport{}, fmt.Errorf("line %d: session_meta payload: %w", lineNo, err)
			}
			if p.ID != "" && !seenSession[p.ID] {
				seenSession[p.ID] = true
				report.SessionIDs = append(report.SessionIDs, p.ID)
			}
		default:
			// event_msg, turn_context, world_state, compacted,
			// inter_agent_communication_metadata, and anything else: counted
			// above by RecordTypeCounts already, nothing further to extract.
		}
	}
	if err := scanner.Err(); err != nil {
		return FileReport{}, fmt.Errorf("scan: %w", err)
	}
	return report, nil
}

// walkRolloutFiles returns every *.jsonl path under root, sorted, so a report
// run twice over an unchanged tree lists files in the same order.
func walkRolloutFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// buildReport is the whole tool's read-only core: list files, analyze each,
// aggregate. It never writes to root or to any file under it.
func buildReport(root string) (Report, error) {
	files, err := walkRolloutFiles(root)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		Root:                  root,
		FilesTotal:            len(files),
		RecordTypeCounts:      map[string]int{},
		RoleCounts:            map[string]int{},
		UserContentTypeCounts: map[string]int{},
	}

	for _, path := range files {
		fr, err := analyzeFile(path)
		if err != nil {
			report.FilesUnparseable = append(report.FilesUnparseable, ParseFailure{
				Path:   path,
				Reason: err.Error(),
			})
			continue
		}
		report.FilesParsed++
		report.OperatorTurnsTotal += fr.OperatorTurns
		for k, v := range fr.RecordTypeCounts {
			report.RecordTypeCounts[k] += v
		}
		for k, v := range fr.RoleCounts {
			report.RoleCounts[k] += v
		}
		for k, v := range fr.UserContentTypeCounts {
			report.UserContentTypeCounts[k] += v
		}
		report.Files = append(report.Files, fr)
	}

	return report, nil
}
