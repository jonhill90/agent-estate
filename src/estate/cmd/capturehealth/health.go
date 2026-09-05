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
//     tree has) to 1,579 distinct turns: 1,535 of those (97.2%) ALSO appear
//     verbatim as an ordinary response_item in the same file -- an extractor
//     that reads both compacted and response_item WILL double-count these.
//     44 distinct turns (2.8%), confined to 5 files, have NO matching
//     response_item anywhere in that same file -- an extractor that skips
//     compacted entirely WILL silently lose these 44. (An earlier pass of
//     this same measurement matched compacted turns against response_item
//     text GLOBALLY, across every file, and found only 12 -- the other 32
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
//     (Corrected 2026-09-05: an independent review of PR #1226 found the
//     originally-shipped 1,533/46 split wrong, because the per-file overlap
//     check was single-pass and incremental -- it tested each compacted turn
//     against only response_item text already seen EARLIER in the same file.
//     The live tree has a compacted record's replacement_history duplicating
//     a response_item that appears LATER in its own file (two counterexamples
//     found, e.g. a compacted record at line 3546 of one file whose matching
//     response_item is at line 7087, after it); the incremental check missed
//     both and miscounted them as only-in-compacted. analyzeFile now runs a
//     genuine two-pass-per-file scan -- collectResponseItemTexts reads the
//     whole file before any compacted record is checked against it -- and
//     the 1,535/44 figures above are re-derived from that corrected scan,
//     not copied from the review that found the bug.)
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
//
// # K2 slice 3 (agent-estate#1139): parsing moved to internal/rollout
//
// The record-shape decoding, genuineOperatorTurn predicate, and the
// compacted/response_item same-file dedup rule documented above now live in
// internal/rollout, not in this file -- cmd/corpusextract's dry-run manifest
// needs the identical logic (down to the identical dedup rule) to guarantee
// its own counts cannot diverge from the numbers measured here. This file
// keeps the FileReport/Report shapes and the human-readable summary; every
// type below that used to be decoded here is now a type alias onto
// internal/rollout, and analyzeFile/buildReport are thin wrappers over
// rollout.AnalyzeFile/rollout.WalkRolloutFiles. No behavior changed by this
// move -- this package's own test suite (unmodified by K2 slice 3) is what
// proves that.
//
// # K2 slice 3, provenance contract (agent-estate#1139)
//
// FileReport.NewestTimestamp / Report.NewestTimestamp and
// Report.SourceHealth are purely additive on top of everything above: they
// derive from the same read-only walk this file already performed and do
// not change any pre-existing count (see contract.go, and this package's
// tests, for the before/after proof). Timestamp extraction is kept in THIS
// file rather than folded into internal/rollout's own AnalyzeFile, since
// that package is shared with cmd/corpusextract and this slice's own scope
// is capturehealth's freshness contract, not a change to the shared parser.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/provenance"
	"github.com/jonhill90/agent-estate/estate/internal/rollout"
)

// responseItemPayload and contentItem are aliased onto internal/rollout so
// this package's existing tests (which construct them directly) keep
// compiling unchanged while the actual decoding logic lives in one place.
type responseItemPayload = rollout.ResponseItemPayload
type contentItem = rollout.ContentItem

// SessionAttribution is aliased the same way -- FileReport.Sessions is
// exactly rollout.SessionAttribution's shape.
type SessionAttribution = rollout.SessionAttribution

// genuineOperatorTurn delegates to internal/rollout's positive predicate --
// see that package's doc comment for correction 2.
func genuineOperatorTurn(p responseItemPayload) bool {
	return rollout.GenuineOperatorTurn(p)
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

	// NewestTimestamp is the newest parsable "timestamp" value seen on any
	// record in this file, RFC3339 as Codex itself writes it, or "" if no
	// record in the file carried a parsable one. Additive for slice 3's
	// freshness contract (agent-estate#1139, provenance.Freshness) -- it
	// does not change OperatorTurns or any other pre-existing count.
	NewestTimestamp string `json:"newest_timestamp,omitempty"`
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

	// NewestTimestamp is the newest FileReport.NewestTimestamp across every
	// parsed file, or "" if no file carried a parsable one. Additive for
	// slice 3's freshness contract -- see FileReport.NewestTimestamp.
	NewestTimestamp string `json:"newest_timestamp,omitempty"`

	// SourceHealth is this SAME report, expressed through the generic
	// per-source contract a second source implements too (agent-estate#1139
	// slice 3, internal/provenance.SourceHealth). Populated by
	// BuildSourceHealth (contract.go); every field on it is derived from
	// the values already computed above -- see toSourceHealth's own doc
	// comment for the mapping. Purely additive: every field ABOVE this one
	// is computed exactly as it was before slice 3 existed.
	SourceHealth provenance.SourceHealth `json:"source_health"`

	Files []FileReport `json:"files"`
}

// analyzeFile is a thin wrapper over rollout.AnalyzeFile: it re-shapes
// rollout.FileAnalysis into this package's own FileReport. All decoding,
// the genuineOperatorTurn predicate, and the compacted/response_item
// same-file dedup rule live in internal/rollout -- see this file's package
// comment, K2 slice 3 section. NewestTimestamp is the one field internal/
// rollout does not compute -- see newestTimestampInFile's own doc comment
// for why that scan stays local to this package instead.
func analyzeFile(path string) (FileReport, error) {
	fa, err := rollout.AnalyzeFile(path)
	if err != nil {
		return FileReport{}, err
	}

	newest, err := newestTimestampInFile(path)
	if err != nil {
		return FileReport{}, err
	}

	return FileReport{
		Path:                             path,
		SessionIDs:                       fa.SessionIDs,
		SessionMetaRecords:               fa.SessionMetaRecords,
		Sessions:                         fa.Sessions,
		OperatorTurns:                    fa.OperatorTurns,
		CompactedRecords:                 fa.CompactedRecords,
		CompactedUserTurnsRaw:            fa.CompactedUserTurnsRaw,
		CompactedUserTurnsDistinct:       fa.CompactedUserTurnsDistinct,
		CompactedOverlapWithResponseItem: fa.CompactedOverlapWithResponseItem,
		CompactedOnlyInCompacted:         fa.CompactedOnlyInCompacted,
		RecordTypeCounts:                 fa.RecordTypeCounts,
		RoleCounts:                       fa.RoleCounts,
		UserContentTypeCounts:            fa.UserContentTypeCounts,
		LineCount:                        fa.LineCount,
		NewestTimestamp:                  newest,
	}, nil
}

// rawTimestampRecord decodes only the one field internal/rollout's own
// RawRecord does not carry: every rollout record type shares a top-level
// "timestamp" string, and slice 3's freshness contract is the only consumer
// that needs it, so it is decoded here rather than added to the shared
// parser (see this file's package comment).
type rawTimestampRecord struct {
	Timestamp string `json:"timestamp"`
}

// newestTimestampInFile is a second, minimal read-only pass over path: it
// extracts the newest parsable RFC3339 "timestamp" seen on any line,
// independent of record type, and returns "" if none parsed. It never
// changes OperatorTurns or any other rollout.AnalyzeFile count -- it exists
// purely to feed FileReport.NewestTimestamp / Report.NewestTimestamp
// (agent-estate#1139 slice 3's Freshness contract).
func newestTimestampInFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var newest time.Time
	var newestRaw string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec rawTimestampRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return "", fmt.Errorf("line %d: %w", lineNo, err)
		}
		if rec.Timestamp == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
		if err != nil {
			// An unparsable timestamp is metadata, not identity or a
			// countable record -- it is silently not credited toward
			// freshness rather than failing the whole file.
			continue
		}
		if t.After(newest) {
			newest = t
			newestRaw = rec.Timestamp
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan: %w", err)
	}
	return newestRaw, nil
}

// buildReport is the whole tool's read-only core: list files, analyze each,
// aggregate. It never writes to root or to any file under it.
func buildReport(root string) (Report, error) {
	files, err := rollout.WalkRolloutFiles(root)
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

	var newestTimestamp time.Time
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
		if fr.NewestTimestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, fr.NewestTimestamp); err == nil && t.After(newestTimestamp) {
				newestTimestamp = t
				report.NewestTimestamp = fr.NewestTimestamp
			}
		}
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
