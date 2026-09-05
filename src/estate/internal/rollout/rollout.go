// Package rollout is the one parser for Codex rollout JSONL files
// (~/.codex/sessions/**/*.jsonl). It was factored out of cmd/capturehealth by
// K2 slice 3 (agent-estate#1139) so cmd/corpusextract's dry-run manifest and
// cmd/capturehealth's aggregate report decode the identical record shapes
// and apply the identical genuine-operator-turn and same-file dedup rule --
// two parsers for one format is how the counts diverge, which is exactly
// what agent-estate#1139 asked this slice not to risk.
//
// Every correction cmd/capturehealth's own doc comment documents (session id
// lives at session_meta.payload.id; role=="developer" is not an operator
// turn; compacted overlaps response_item 97.2% of the time and must be
// deduped by exact text WITHIN one file, never across files, never
// incrementally) is encoded here once, not restated per caller. Read
// cmd/capturehealth's package comment for the measured live-tree numbers
// this package's dedup rule reproduces.
//
// This package only reads. Every function here takes a path and returns
// data; none opens a file for writing, and none touches the corpus.
package rollout

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// RawRecord is the top-level shape shared by every rollout JSONL line:
// {"timestamp": "...", "type": "...", "payload": {...}}. Payload is left as
// raw bytes because its shape depends entirely on Type, and this package
// must not assume it knows every shape that will ever appear.
type RawRecord struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ResponseItemPayload is response_item's payload shape. Only "message"
// payloads (as opposed to "reasoning", "function_call",
// "function_call_output") carry a Role at all.
type ResponseItemPayload struct {
	Type    string        `json:"type"`
	Role    string        `json:"role"`
	Content []ContentItem `json:"content"`
}

type ContentItem struct {
	Type string `json:"type"`

	// Text is decoded because both compacted-overlap detection and
	// corpusextract's manifest hashing need the operator's actual words --
	// every other consumer only ever looks at Type.
	Text string `json:"text"`
}

// CompactedPayload is compacted's payload shape. ReplacementHistory reuses
// ResponseItemPayload's shape rather than a second near-identical struct.
type CompactedPayload struct {
	ReplacementHistory []ResponseItemPayload `json:"replacement_history"`
}

// SessionMetaPayload is session_meta's payload shape. The field is "id",
// never "session_id" -- there is no session_id field to mistakenly read.
// Only ID is decoded typed; payload.originator/source/cwd vary in shape
// across sessions (a forked/subagent session's payload.source is an object,
// not a string) and this package does not need them.
type SessionMetaPayload struct {
	ID string `json:"id"`
}

// GenuineOperatorTurn is the ONE positive predicate this package treats as
// an extractable operator turn: role=="user" AND content[0].type==
// "input_text". It never checks "role != assistant" -- a negative filter
// would count an injected developer instruction as the operator's own words.
func GenuineOperatorTurn(p ResponseItemPayload) bool {
	if p.Role != "user" {
		return false
	}
	if len(p.Content) == 0 {
		return false
	}
	return p.Content[0].Type == "input_text"
}

// SessionAttribution is one distinct session_meta.payload.id's own operator
// turn count within one file.
type SessionAttribution struct {
	SessionID     string `json:"session_id"`
	OperatorTurns int    `json:"operator_turns"`
}

// Turn is one operator turn a K2 extractor would ingest from a single file,
// carrying enough provenance for a manifest line: the line it was found at,
// the session it attributes to (most recently preceding session_meta in file
// order, "" if none preceded it), which record type produced it, and its own
// text (callers that must not retain raw text -- e.g. a manifest -- hash it
// and discard Text immediately; this package does not hash on their behalf
// since capturehealth has no need to).
//
// Turns is ordered: every response_item turn first, in file order, then
// every CompactedOnlyInCompacted turn, in the file order of the compacted
// record that carried it -- a compacted record's own position in the file
// does not establish an ingestion order the rollout format defines, so this
// package picks response_item-first deterministically rather than leaving
// callers to invent their own order.
type Turn struct {
	LineNo    int    `json:"line_no"`
	SessionID string `json:"session_id"`
	Source    string `json:"source"` // "response_item" or "compacted"
	Text      string `json:"-"`
}

// FileAnalysis is one rollout file's full parse: every aggregate
// cmd/capturehealth's FileReport needs, plus the ordered Turns a dry-run
// extractor would ingest.
type FileAnalysis struct {
	SessionIDs                       []string
	SessionMetaRecords               int
	Sessions                         []SessionAttribution
	OperatorTurns                    int
	CompactedRecords                 int
	CompactedUserTurnsRaw            int
	CompactedUserTurnsDistinct       int
	CompactedOverlapWithResponseItem int
	CompactedOnlyInCompacted         int
	RecordTypeCounts                 map[string]int
	RoleCounts                       map[string]int
	UserContentTypeCounts            map[string]int
	LineCount                        int
	Turns                            []Turn
}

func newFileAnalysis() FileAnalysis {
	return FileAnalysis{
		RecordTypeCounts:      map[string]int{},
		RoleCounts:            map[string]int{},
		UserContentTypeCounts: map[string]int{},
	}
}

// AnalyzeFile reads one rollout JSONL file top to bottom with a plain
// buffered read (os.Open only -- no write mode, no truncate) and returns its
// own FileAnalysis. A line that fails to unmarshal as RawRecord fails the
// WHOLE file: the error names the line and nothing from that file
// contributes to any aggregate, rather than silently counting only the
// lines before the break.
func AnalyzeFile(path string) (FileAnalysis, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileAnalysis{}, err
	}
	defer f.Close()

	// responseItemTexts is every genuine operator turn's own text ANYWHERE in
	// this file, collected as a full first pass -- not incrementally
	// alongside the second pass below. A compacted record's
	// replacement_history can duplicate a response_item that appears LATER
	// in the same file (agent-estate#1226); checking only text seen so far
	// misses that and wrongly credits it to CompactedOnlyInCompacted.
	responseItemTexts, err := collectResponseItemTexts(f)
	if err != nil {
		return FileAnalysis{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return FileAnalysis{}, err
	}

	fa := newFileAnalysis()
	seenSession := map[string]bool{}
	sessionIdx := map[string]int{} // session id -> index into fa.Sessions
	currentSession := -1           // index into fa.Sessions the NEXT turn attributes to, or -1
	currentSessionID := ""

	// compactedTexts is every distinct compacted-turn text already counted in
	// THIS file, so a session compacted more than once (which re-embeds its
	// full prior history each time) does not inflate CompactedUserTurnsDistinct.
	compactedTexts := map[string]bool{}
	// compactedOnly collects the recovered-turns-only-in-compacted, appended
	// to fa.Turns after every response_item turn so ingestion order is
	// response_item-first, deterministically.
	var compactedOnly []Turn

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec RawRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return FileAnalysis{}, fmt.Errorf("line %d: %w", lineNo, err)
		}
		fa.LineCount++
		fa.RecordTypeCounts[rec.Type]++

		switch rec.Type {
		case "response_item":
			var p ResponseItemPayload
			if err := json.Unmarshal(rec.Payload, &p); err != nil {
				return FileAnalysis{}, fmt.Errorf("line %d: response_item payload: %w", lineNo, err)
			}
			if p.Type != "message" || p.Role == "" {
				// reasoning / function_call / function_call_output payloads
				// carry no role at all -- not a role to tally, and never
				// counted as an operator turn.
				continue
			}
			fa.RoleCounts[p.Role]++
			if p.Role == "user" {
				ct := "(missing)"
				if len(p.Content) > 0 {
					ct = p.Content[0].Type
				}
				fa.UserContentTypeCounts[ct]++
			}
			if GenuineOperatorTurn(p) {
				fa.OperatorTurns++
				if currentSession >= 0 {
					fa.Sessions[currentSession].OperatorTurns++
				}
				fa.Turns = append(fa.Turns, Turn{
					LineNo:    lineNo,
					SessionID: currentSessionID,
					Source:    "response_item",
					Text:      p.Content[0].Text,
				})
			}
		case "session_meta":
			var p SessionMetaPayload
			if err := json.Unmarshal(rec.Payload, &p); err != nil {
				return FileAnalysis{}, fmt.Errorf("line %d: session_meta payload: %w", lineNo, err)
			}
			if p.ID != "" {
				fa.SessionMetaRecords++
				if !seenSession[p.ID] {
					seenSession[p.ID] = true
					sessionIdx[p.ID] = len(fa.Sessions)
					fa.SessionIDs = append(fa.SessionIDs, p.ID)
					fa.Sessions = append(fa.Sessions, SessionAttribution{SessionID: p.ID})
				}
				currentSession = sessionIdx[p.ID]
				currentSessionID = p.ID
			}
		case "compacted":
			var p CompactedPayload
			if err := json.Unmarshal(rec.Payload, &p); err != nil {
				return FileAnalysis{}, fmt.Errorf("line %d: compacted payload: %w", lineNo, err)
			}
			fa.CompactedRecords++
			for _, item := range p.ReplacementHistory {
				if item.Type != "message" || item.Role != "user" {
					continue
				}
				if len(item.Content) == 0 || item.Content[0].Type != "input_text" {
					continue
				}
				text := item.Content[0].Text
				fa.CompactedUserTurnsRaw++
				if compactedTexts[text] {
					continue
				}
				compactedTexts[text] = true
				fa.CompactedUserTurnsDistinct++
				if responseItemTexts[text] {
					fa.CompactedOverlapWithResponseItem++
				} else {
					fa.CompactedOnlyInCompacted++
					compactedOnly = append(compactedOnly, Turn{
						LineNo:    lineNo,
						SessionID: currentSessionID,
						Source:    "compacted",
						Text:      text,
					})
				}
			}
		default:
			// event_msg, turn_context, world_state,
			// inter_agent_communication_metadata, and anything else: counted
			// above by RecordTypeCounts already, nothing further to extract.
		}
	}
	if err := scanner.Err(); err != nil {
		return FileAnalysis{}, fmt.Errorf("scan: %w", err)
	}

	fa.Turns = append(fa.Turns, compactedOnly...)
	return fa, nil
}

// collectResponseItemTexts is AnalyzeFile's first pass: every genuine
// operator turn's own text found ANYWHERE in f, read top to bottom once. It
// exists as a separate full pass (rather than folded into AnalyzeFile's main
// loop) specifically so the compacted/response_item overlap check tests
// against the WHOLE file, not just the text seen before a given compacted
// record. f must be positioned at the start of the file on entry; the caller
// seeks it back before its own pass.
func collectResponseItemTexts(f *os.File) (map[string]bool, error) {
	texts := map[string]bool{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec RawRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if rec.Type != "response_item" {
			continue
		}
		var p ResponseItemPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return nil, fmt.Errorf("line %d: response_item payload: %w", lineNo, err)
		}
		if p.Type != "message" || p.Role == "" {
			continue
		}
		if GenuineOperatorTurn(p) {
			texts[p.Content[0].Text] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return texts, nil
}

// WalkRolloutFiles returns every *.jsonl path under root, sorted, so a
// report run twice over an unchanged tree lists files in the same order.
func WalkRolloutFiles(root string) ([]string, error) {
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
