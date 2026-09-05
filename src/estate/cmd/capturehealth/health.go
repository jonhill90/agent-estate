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
//
// # K2 slice 2 (agent-estate#1139): two questions settled here, not just asked
//
// Slice 1 (PR #1225) shipped this report with two findings unsettled, flagged
// by both of its independent reviews. Both are answered below with counts
// measured against the live ~/.codex/sessions tree (473 files, read-only,
// mtime unchanged -- see this package's tests for the fixture-based proof;
// the live counts themselves are not re-asserted by any test, since the tree
// they were measured against is the operator's real, growing history, not a
// fixture. Re-run `capturehealth -json` against the current tree before
// citing these numbers further, the same discipline this repo's docs already
// require of any inherited figure).
//
//  1. Does `compacted`/`replacement_history` carry operator text that
//     DOUBLE-COUNTS against response_item, or that is LOST if compacted is
//     skipped? MEASURED ANSWER: both happen, and they are not close to the
//     same size. Measured 2026-09-05 with `capturehealth -json` against the
//     live tree (473 files, mtime unchanged before/after): 86 compacted
//     records across 23 files; 9,158 raw role=="user"/input_text entries in
//     their replacement_history arrays (a session compacted more than once
//     re-embeds its FULL prior history in each later compaction, so this raw
//     count is NOT a distinct-turn count -- see CompactedUserTurnsRaw's own
//     doc comment). Deduplicated PER FILE (never across files -- a
//     compacted record's own session lives in one file in every case this
//     tree has) to 1,579 distinct turns: 1,533 of those (97.1%) ALSO appear
//     verbatim as an ordinary response_item in the same file -- an extractor
//     that reads both compacted and response_item WILL double-count these.
//     46 distinct turns (2.9%), confined to 5 files, have NO matching
//     response_item anywhere in that same file -- an extractor that skips
//     compacted entirely WILL silently lose these 46. (An earlier pass of
//     this same measurement matched compacted turns against response_item
//     text GLOBALLY, across every file, and found only 12 -- the other 34
//     WERE findable somewhere in the corpus, just not in the file the
//     compacted record itself lives in. The narrower, per-file number above
//     is the one that matters: an extractor built to process one rollout
//     file at a time -- the natural shape, since a session's own record
//     never spans two files in this tree -- can only see response_item
//     records within that same file, so cross-file matches are not
//     available to it and must not be credited as "not lost".) Consequence
//     for the extractor this issue is sequenced before: it must read
//     response_item as authoritative and use compacted ONLY to recover
//     turns response_item does not already carry within the SAME file
//     (exact-text dedup, same rule CompactedOverlapWithResponseItem/
//     CompactedOnlyInCompacted below implement) -- reading both without that
//     dedup rule inflates operator-turn counts by roughly two orders of
//     magnitude if raw replacement_history entries are counted directly.
//
//  2. 497 session_meta records were observed across 473 files -- more
//     sessions than files. MEASURED ANSWER: yes, a rollout file can carry
//     more than one session. Measured 2026-09-05: 16 of 473 files (3.4%)
//     contain exactly 2 distinct session_meta.payload.id values; a further 2
//     files carry the SAME id repeated (3x and 7x respectively, accounting
//     for the remaining 497-473-16=8 extra raw records) rather than a second
//     distinct session. Before this change, capturehealth recorded which
//     session ids appeared in a file (FileReport.SessionIDs) but attributed
//     EVERY operator turn in the file to the file as a whole -- it did not
//     silently assume one session, but it also did not answer "how many
//     turns belong to which id" for the 16 multi-session files. FileReport
//     now carries Sessions []SessionAttribution, built by attributing each
//     genuine operator turn to whichever session_meta most recently preceded
//     it in file order -- the only ordering rule the format supports, since
//     no record carries an explicit session id of its own outside
//     session_meta.
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

	// Text is only decoded because compacted-overlap detection needs the
	// actual operator words to compare against response_item's own text --
	// every other consumer of contentItem only ever looked at Type.
	Text string `json:"text"`
}

// compactedPayload is compacted's payload shape. replacement_history is a
// list of the SAME shape response_item.payload uses for a message
// ("type":"message","role":"user"/"assistant"/...,"content":[...]) -- so it
// reuses responseItemPayload rather than a second near-identical struct.
type compactedPayload struct {
	ReplacementHistory []responseItemPayload `json:"replacement_history"`
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

	// SessionMetaRecords is every session_meta record seen in this file,
	// raw -- including a repeat of an already-seen id (observed twice in the
	// live tree, 3x and 7x repeats of the same id in one file). Usually
	// equal to len(SessionIDs); greater when a session_meta re-announces a
	// session already seen, distinguishing that from a genuine additional
	// session (agent-estate#1139 finding 2's "497 across 473 files" is a raw
	// count, not a distinct-session count).
	SessionMetaRecords int `json:"session_meta_records"`

	// Sessions attributes OperatorTurns to whichever distinct session id it
	// belongs to, in SessionIDs' first-seen order -- settles agent-estate#1139's
	// "497 session_meta across 473 files" finding: a file is not always one
	// session (measured: 16 of 473 files carry 2 distinct ids), and turns
	// after the second session_meta must not be folded into the first
	// session's count. A turn is attributed to whichever session_meta most
	// recently preceded it in file order; a turn before ANY session_meta
	// (not observed in the live tree, but not assumed impossible) is counted
	// in OperatorTurns and RoleCounts/UserContentTypeCounts as usual but
	// contributes to no entry here.
	Sessions []SessionAttribution `json:"sessions"`

	// OperatorTurns is the count of genuine operator turns: response_item
	// records with payload.role=="user" AND payload.content[0].type==
	// "input_text". This is the ONLY thing this report treats as the
	// operator's own words (correction 2).
	OperatorTurns int `json:"operator_turns"`

	// CompactedRecords is the count of `compacted` records in this file --
	// each one summarizes and replaces a slice of prior conversation history.
	CompactedRecords int `json:"compacted_records"`

	// CompactedUserTurnsRaw is every role=="user"/content[0].type=="input_text"
	// entry found across every compacted record's replacement_history in
	// this file, INCLUDING repeats. A session compacted more than once
	// re-embeds its full prior history in each later compaction's
	// replacement_history, so this is a raw occurrence count, not a
	// distinct-turn count -- see CompactedUserTurnsDistinct for that.
	CompactedUserTurnsRaw int `json:"compacted_user_turns_raw"`

	// CompactedUserTurnsDistinct is CompactedUserTurnsRaw deduplicated by
	// exact text match within this file.
	CompactedUserTurnsDistinct int `json:"compacted_user_turns_distinct"`

	// CompactedOverlapWithResponseItem is how many of those distinct texts
	// ALSO appear as a genuine operator turn (response_item, role=="user",
	// content[0].type=="input_text") elsewhere in this same file -- the
	// double-count risk an extractor that reads both compacted and
	// response_item must dedup against.
	CompactedOverlapWithResponseItem int `json:"compacted_overlap_with_response_item"`

	// CompactedOnlyInCompacted is the remainder: distinct compacted turns
	// with NO matching response_item text anywhere in this file -- the loss
	// risk an extractor that skips compacted entirely would silently incur.
	CompactedOnlyInCompacted int `json:"compacted_only_in_compacted"`

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

// SessionAttribution is one distinct session_meta.payload.id's own operator
// turn count within one file. See FileReport.Sessions.
type SessionAttribution struct {
	SessionID     string `json:"session_id"`
	OperatorTurns int    `json:"operator_turns"`
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

	// SessionMetaTotal is every session_meta record seen, raw (not
	// deduplicated by id) -- the "497" in agent-estate#1139's "497
	// session_meta across 473 files" finding.
	SessionMetaTotal int `json:"session_meta_total"`

	// FilesWithMultipleSessions is how many files carry more than one
	// DISTINCT session_meta.payload.id -- settles whether a rollout file can
	// carry more than one session (agent-estate#1139 finding 2). This is
	// narrower than "files with more than one session_meta record": a file
	// can repeat the SAME id more than once (observed in the live tree) --
	// that is not a second session and does not count here.
	FilesWithMultipleSessions int `json:"files_with_multiple_sessions"`

	CompactedRecordsTotal                 int `json:"compacted_records_total"`
	CompactedUserTurnsRawTotal            int `json:"compacted_user_turns_raw_total"`
	CompactedUserTurnsDistinctTotal       int `json:"compacted_user_turns_distinct_total"`
	CompactedOverlapWithResponseItemTotal int `json:"compacted_overlap_with_response_item_total"`
	CompactedOnlyInCompactedTotal         int `json:"compacted_only_in_compacted_total"`

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
	sessionIdx := map[string]int{} // session id -> index into report.Sessions
	currentSession := -1           // index into report.Sessions the NEXT turn attributes to, or -1

	// responseItemTexts is every genuine operator turn's own text seen so
	// far in THIS file -- used only to detect compacted/response_item
	// overlap (finding 1). It relies on compacted records summarizing PRIOR
	// history, which is the append-only log's own ordering guarantee: a
	// compacted record has never been observed preceding the response_item
	// it duplicates in the live tree (see this package's doc comment).
	responseItemTexts := map[string]bool{}
	// compactedTexts is every distinct compacted-turn text already counted
	// in THIS file, so a session compacted more than once (which re-embeds
	// its full prior history each time) does not inflate
	// CompactedUserTurnsDistinct.
	compactedTexts := map[string]bool{}

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
				responseItemTexts[p.Content[0].Text] = true
				if currentSession >= 0 {
					report.Sessions[currentSession].OperatorTurns++
				}
			}
		case "session_meta":
			var p sessionMetaPayload
			if err := json.Unmarshal(rec.Payload, &p); err != nil {
				return FileReport{}, fmt.Errorf("line %d: session_meta payload: %w", lineNo, err)
			}
			if p.ID != "" {
				report.SessionMetaRecords++
				if !seenSession[p.ID] {
					seenSession[p.ID] = true
					sessionIdx[p.ID] = len(report.Sessions)
					report.SessionIDs = append(report.SessionIDs, p.ID)
					report.Sessions = append(report.Sessions, SessionAttribution{SessionID: p.ID})
				}
				currentSession = sessionIdx[p.ID]
			}
		case "compacted":
			var p compactedPayload
			if err := json.Unmarshal(rec.Payload, &p); err != nil {
				return FileReport{}, fmt.Errorf("line %d: compacted payload: %w", lineNo, err)
			}
			report.CompactedRecords++
			for _, item := range p.ReplacementHistory {
				if item.Type != "message" || item.Role != "user" {
					continue
				}
				if len(item.Content) == 0 || item.Content[0].Type != "input_text" {
					continue
				}
				text := item.Content[0].Text
				report.CompactedUserTurnsRaw++
				if compactedTexts[text] {
					continue
				}
				compactedTexts[text] = true
				report.CompactedUserTurnsDistinct++
				if responseItemTexts[text] {
					report.CompactedOverlapWithResponseItem++
				} else {
					report.CompactedOnlyInCompacted++
				}
			}
		default:
			// event_msg, turn_context, world_state,
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
		report.SessionMetaTotal += fr.SessionMetaRecords
		if len(fr.SessionIDs) > 1 {
			report.FilesWithMultipleSessions++
		}
		report.CompactedRecordsTotal += fr.CompactedRecords
		report.CompactedUserTurnsRawTotal += fr.CompactedUserTurnsRaw
		report.CompactedUserTurnsDistinctTotal += fr.CompactedUserTurnsDistinct
		report.CompactedOverlapWithResponseItemTotal += fr.CompactedOverlapWithResponseItem
		report.CompactedOnlyInCompactedTotal += fr.CompactedOnlyInCompacted
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
