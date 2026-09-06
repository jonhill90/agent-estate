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
	"strings"
)

// RawRecord is the top-level shape shared by every rollout JSONL line:
// {"timestamp": "...", "type": "...", "payload": {...}}. Payload is left as
// raw bytes because its shape depends entirely on Type, and this package
// must not assume it knows every shape that will ever appear. Timestamp is
// decoded verbatim (never parsed or reformatted here) so a caller that needs
// it -- cmd/codexingest's CapturedAt metadata, agent-estate#1139 -- gets the
// source's own string exactly as written; this package makes no claim about
// its format or presence.
type RawRecord struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
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

	// CapturedAt is the record's own top-level "timestamp" field, verbatim
	// (never parsed, never defaulted to "now" -- see internal/provenance's
	// own CapturedAt doc comment for why absence must stay absence). For a
	// response_item turn this is that record's own timestamp; for a
	// CompactedOnlyInCompacted turn it is the COMPACTED RECORD's timestamp
	// (replacement_history entries carry no timestamp of their own), which
	// is a coarser approximation a caller should treat as "roughly when this
	// history was captured", not "when the operator actually typed it".
	CapturedAt string `json:"captured_at,omitempty"`

	// PriorAssistantText is the text of the assistant turn immediately
	// preceding this one, in the SAME chronological stream this Turn was
	// found in -- "" is a real, typed value meaning no assistant turn
	// precedes it (this turn opens the session, or opens the recovered
	// history), never a parse failure (agent-estate#1139's own contract:
	// internal/provenance.UnitProvenance.PriorAssistantContext is metadata,
	// never operator text, and absence here must stay absence rather than
	// being coerced to a placeholder).
	//
	// For a response_item turn, "the same stream" is this file's own top to
	// bottom record order: PriorAssistantText is the most recently seen
	// role=="assistant" message response_item ANYWHERE earlier in the file,
	// skipping role-less payloads (reasoning/function_call/
	// function_call_output -- see GenuineOperatorTurn's own doc comment for
	// why those carry no role at all) and role=="developer" turns, neither
	// of which is an assistant turn.
	//
	// For a CompactedOnlyInCompacted turn, "the same stream" is instead that
	// turn's OWN compacted record's replacement_history array, walked in
	// its own order -- not the outer file's record order, which the
	// compacted record's position in the file does not reflect (a
	// compaction event can be recorded long after the history it
	// summarizes). An assistant entry in replacement_history is tracked
	// exactly as it appears there, independent of whether that same
	// assistant text also appears as an ordinary response_item elsewhere in
	// the file.
	PriorAssistantText string `json:"prior_assistant_text,omitempty"`
}

// assistantMessageText reports the joined text of an assistant message
// payload, and whether it had any output_text content to join at all. A
// role=="assistant" message with no output_text parts (content entirely of
// some other type) reports ok=false -- callers must leave the running
// "last assistant text" state unchanged in that case, not overwrite it with
// an empty string that would then read as "no prior assistant turn".
func assistantMessageText(p ResponseItemPayload) (string, bool) {
	if p.Type != "message" || p.Role != "assistant" {
		return "", false
	}
	var parts []string
	for _, c := range p.Content {
		if c.Type == "output_text" {
			parts = append(parts, c.Text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n\n"), true
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

	// lastAssistantText is the most recently seen assistant message's text,
	// in this FILE's own top-to-bottom record order -- what a response_item
	// operator turn's own Turn.PriorAssistantText is set from at the moment
	// that turn is appended. "" means no qualifying assistant message has
	// been seen yet in this file (this turn opens the session, or every
	// assistant turn so far had no output_text content -- see
	// assistantMessageText's own doc comment for why that case leaves this
	// unchanged rather than resetting it to "").
	lastAssistantText := ""

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
				// carry no role at all -- not a role to tally, never
				// counted as an operator turn, and never treated as an
				// assistant turn either: lastAssistantText is left exactly
				// as it was, so a reasoning step interleaved between an
				// assistant reply and the operator's next turn does not
				// erase that reply.
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
					LineNo:             lineNo,
					SessionID:          currentSessionID,
					Source:             "response_item",
					Text:               p.Content[0].Text,
					CapturedAt:         rec.Timestamp,
					PriorAssistantText: lastAssistantText,
				})
			}
			if text, ok := assistantMessageText(p); ok {
				lastAssistantText = text
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
			// localLastAssistant tracks the most recently seen assistant
			// message WITHIN THIS COMPACTED RECORD's own replacement_history
			// array, walked in its own order -- deliberately independent of
			// lastAssistantText above. replacement_history is a recovered
			// slice of EARLIER conversation history; its own internal order
			// is what reflects "what preceded this turn when it actually
			// happened", not this record's position in the outer file (a
			// compaction event can be logged long after the history it
			// summarizes -- see Turn.PriorAssistantText's own doc comment).
			localLastAssistant := ""
			for _, item := range p.ReplacementHistory {
				if text, ok := assistantMessageText(item); ok {
					localLastAssistant = text
					continue
				}
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
						LineNo:             lineNo,
						SessionID:          currentSessionID,
						Source:             "compacted",
						Text:               text,
						CapturedAt:         rec.Timestamp,
						PriorAssistantText: localLastAssistant,
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
