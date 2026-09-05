// Command corpusextract is K2 slice 3 (agent-estate#1139): the dry-run
// extractor. It walks ~/.codex/sessions rollout JSONL (via internal/rollout,
// the same parser cmd/capturehealth uses -- see that package's doc comment
// for why sharing one parser matters here) and produces a manifest of
// exactly which operator turns an ingestion step would write, WITHOUT
// writing anything: no corpus mutation, no ~/corpus/ledger.sqlite3 open at
// all (not even read-only -- this slice has no reason to touch it), and no
// write of any kind under ~/.codex. Every source path is opened with
// os.Open only.
//
// # Scope decision: FEED, not SUPERSEDE
//
// This extractor FEEDS a future write step; it does not supersede the
// existing corpus ingestion path. The two are answers to different
// questions: the existing path (dead since 2026-09-02 19:09:37, 0 prompts in
// 3 days measured against ~/corpus/ledger.sqlite3 -- see prompt-corpus's own
// docs for that path's mechanism) is/was the record of the OPERATOR'S OWN
// corpus of decisions and standing parameters, sourced from wherever that
// pipeline read transcripts. This extractor reads a different, narrower
// source -- Codex CLI rollout JSONL under ~/.codex/sessions -- and produces
// a dry-run manifest of turns FROM THAT SOURCE ONLY. Nothing here reads or
// writes live_parameters, resolved_to, or any other corpus table; nothing
// here judges a turn's weight, resolves it to a parameter, or decides
// whether it is binding. A write step consuming this manifest would be one
// more feed into the same corpus the existing (dead) pipeline used to feed,
// not a replacement mechanism for judging or storing what gets fed in. If a
// future slice decides this extractor's output should instead become the
// corpus's only ingestion path (superseding rather than feeding), that is a
// new decision this doc comment does not make on its own, and should replace
// this paragraph rather than layer beside it.
//
// # Storage format: not decided here
//
// This slice emits a manifest (JSON, in memory / to a file), not a store.
// Whether the write step (a separate, later slice, explicitly gated on this
// manifest being reviewed) persists to markdown, sqlite, duckdb, or
// something else is an open operator decision this file does not settle by
// implication -- ManifestEntry below is deliberately storage-agnostic (a
// path, a session id, a record index, and a hash; nothing that presumes a
// schema).
//
// # Dedup rule (identical to internal/rollout's, by construction)
//
// A turn is a candidate for the manifest if it is either:
//  1. a genuine operator turn (response_item, role=="user",
//     content[0].type=="input_text") -- always included, or
//  2. a compacted-record replacement_history entry of the same shape whose
//     exact text does NOT appear anywhere else in the same file as a
//     response_item -- i.e. internal/rollout's CompactedOnlyInCompacted set.
//
// A compacted turn whose text DOES appear as a response_item in the same
// file (internal/rollout's CompactedOverlapWithResponseItem) is dropped as a
// duplicate -- that is the "duplicates dropped, by rule" DedupAccount.Overlap
// exists to report. This is the SAME rule, computed by the SAME function
// (rollout.AnalyzeFile), that produced slice 2's measured
// overlap-with-response_item=1535 / only-in-compacted=44 / distinct=1579 --
// see PrintSummary's reconciliation line and this package's own doc comment
// for what a disagreement there would mean.
//
// # No raw text anywhere
//
// ManifestEntry never carries a text body -- only TextSHA256, a sha256 hex
// digest of the operator's own words. Never log, print, or write the raw
// text this tool reads; it belongs to the operator, not to source control or
// a build log.
//
// # Unknown record shapes are refused loudly, not swallowed
//
// internal/rollout.AnalyzeFile's own switch has a silent default case for
// any record type it does not extract turns from (event_msg, turn_context,
// world_state, inter_agent_communication_metadata) -- correct for its job,
// since those types genuinely carry nothing to extract. But that same
// default case would also silently accept a type nobody has ever seen, with
// no error and no signal that a new or malformed shape slipped through
// uncounted. validateKnownRecordTypes below runs BEFORE AnalyzeFile and
// checks every record's own "type" against knownRecordTypes -- the
// whitelist named in this comment -- so a file containing an unrecognised
// type is refused for that file (folded into FilesUnparseable, same as a
// JSON-malformed file already is) rather than silently parsed as if nothing
// unusual were there.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/rollout"
)

// knownRecordTypes is every top-level rollout record "type" this codebase
// recognises: the three internal/rollout.AnalyzeFile extracts turns from
// (session_meta, response_item, compacted) plus the four it deliberately
// extracts nothing from but still recognises as legitimate rollout shapes
// (event_msg, turn_context, world_state,
// inter_agent_communication_metadata). Anything else is refused loudly by
// validateKnownRecordTypes rather than silently ignored.
var knownRecordTypes = map[string]bool{
	"session_meta":                       true,
	"response_item":                      true,
	"compacted":                          true,
	"event_msg":                          true,
	"turn_context":                       true,
	"world_state":                        true,
	"inter_agent_communication_metadata": true,
}

// validateKnownRecordTypes reads path top to bottom, decoding only each
// line's own "type" field (never the payload -- that is AnalyzeFile's job),
// and returns an error naming the first line whose type is not in
// knownRecordTypes or whose JSON does not parse at all. It is a second,
// narrower pass over the same bytes AnalyzeFile will read next -- not a
// second parser for this format, since it never interprets a payload.
func validateKnownRecordTypes(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			return fmt.Errorf("line %d: malformed record: %w", lineNo, err)
		}
		if !knownRecordTypes[rec.Type] {
			return fmt.Errorf("line %d: unknown record type %q", lineNo, rec.Type)
		}
	}
	return scanner.Err()
}

// fileMeta is one rollout file's path plus the mtime this tool observed for
// it in the single directory-and-stat pass listRolloutFilesWithMTimes makes.
// Nothing downstream re-stats or re-walks root -- this is the one read of
// filesystem metadata a run performs.
type fileMeta struct {
	Path    string
	ModTime time.Time
}

// listRolloutFilesWithMTimes is the ONE filesystem read pass this tool makes
// over root's directory structure and every file's own mtime (os.Stat only,
// never a write, never a truncate, never a touch). Every later step --
// watermark computation, inclusion/exclusion, validation, analysis -- works
// from this single snapshot; nothing after this function reads the live tree
// again, which is what makes two runs at an identical watermark reproduce
// each other's output even while the real source tree may be growing
// underneath them.
func listRolloutFilesWithMTimes(root string) ([]fileMeta, error) {
	paths, err := rollout.WalkRolloutFiles(root)
	if err != nil {
		return nil, err
	}
	metas := make([]fileMeta, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", p, err)
		}
		metas = append(metas, fileMeta{Path: p, ModTime: info.ModTime()})
	}
	return metas, nil
}

// watermarkFromMetas is the source watermark a run pins to when the caller
// supplies none explicitly: the highest mtime observed across metas -- the
// same single listing pass every other step in this run uses. Recording this
// value BEFORE validation begins (acceptance criterion 1) and then never
// re-deriving it from a fresh directory read is what criterion 2 ("no step
// re-reads the live tree afterwards") requires.
func watermarkFromMetas(metas []fileMeta) time.Time {
	var max time.Time
	for _, m := range metas {
		if m.ModTime.After(max) {
			max = m.ModTime
		}
	}
	return max
}

// ExcludedFile names one source file this run saw but excluded because its
// own mtime is after the run's watermark -- acceptance criterion 4: what was
// skipped must be visible, never silently absent.
type ExcludedFile struct {
	Path    string `json:"path"`
	ModTime string `json:"mod_time"` // RFC3339Nano, UTC
}

// ManifestEntry is exactly what agent-estate#1139's acceptance criterion 1
// asks for: file, session payload.id, record index, and a hash of the text
// -- never the text itself.
type ManifestEntry struct {
	File        string `json:"file"`
	SessionID   string `json:"session_id"`
	RecordIndex int    `json:"record_index"` // 1-based line number the record was found at
	TextSHA256  string `json:"text_sha256"`
	Source      string `json:"source"` // "response_item" or "compacted"
}

// FileDedup is one file's own dedup accounting, mirroring the fields
// internal/rollout.FileAnalysis already computes -- reported per file so a
// disagreement can be localized to the file it came from, not just the
// aggregate.
type FileDedup struct {
	File                             string `json:"file"`
	CompactedUserTurnsDistinct       int    `json:"compacted_user_turns_distinct"`
	CompactedOverlapWithResponseItem int    `json:"compacted_overlap_with_response_item"`
	CompactedOnlyInCompacted         int    `json:"compacted_only_in_compacted"`
}

// DedupAccount is the whole-tree dedup account acceptance criterion 2 asks
// for: how many candidate compacted turns were dropped as duplicates (the
// Overlap total, dropped under the "text already present as a response_item
// in this same file" rule) and how many distinct compacted turns had no such
// match and were therefore recovered into the manifest (the OnlyInCompacted
// total).
type DedupAccount struct {
	// CompactedUserTurnsDistinctTotal is every distinct (per-file, exact-text)
	// compacted turn seen, before the overlap check -- slice 2's "1,579".
	CompactedUserTurnsDistinctTotal int `json:"compacted_user_turns_distinct_total"`

	// DroppedAsDuplicateOfResponseItem is CompactedUserTurnsDistinctTotal's
	// subset whose exact text also appears as a response_item in the same
	// file -- dropped from the manifest under that rule. Slice 2's "1,535".
	DroppedAsDuplicateOfResponseItem int `json:"dropped_as_duplicate_of_response_item"`

	// RecoveredOnlyInCompacted is the remainder: distinct compacted turns
	// with no response_item match anywhere in their own file, included in
	// the manifest exactly once each. Slice 2's "44".
	RecoveredOnlyInCompacted int `json:"recovered_only_in_compacted"`

	PerFile []FileDedup `json:"per_file"`
}

// Manifest is corpusextract's whole output: every entry that WOULD be
// ingested (never written), plus the dedup account reconciling against
// slice 2's measured figures.
type Manifest struct {
	Root string `json:"root"`

	// Watermark is the source watermark this run pinned to, recorded BEFORE
	// validation began (acceptance criterion 1), RFC3339Nano UTC.
	// WatermarkSource says whether it was derived from this run's own
	// listing pass ("auto", the highest mtime observed) or supplied
	// explicitly by the caller ("explicit").
	Watermark       string `json:"watermark"`
	WatermarkSource string `json:"watermark_source"`

	// FilesTotal is every rollout file this run's one listing pass saw,
	// before watermark filtering. FilesIncluded is the subset at or before
	// Watermark that validation and analysis actually ran on;
	// ExcludedAfterWatermark names the rest (criterion 4).
	FilesTotal             int            `json:"files_total"`
	FilesIncluded          int            `json:"files_included"`
	ExcludedAfterWatermark []ExcludedFile `json:"excluded_after_watermark"`

	FilesParsed             int             `json:"files_parsed"`
	FilesUnparseable        []ParseFailure  `json:"files_unparseable"`
	Entries                 []ManifestEntry `json:"entries"`
	EntriesFromResponseItem int             `json:"entries_from_response_item"`
	EntriesFromCompacted    int             `json:"entries_from_compacted"`
	Dedup                   DedupAccount    `json:"dedup"`
}

// ParseFailure names one file this tool could not read, and why -- never
// skipped silently. Identical shape to cmd/capturehealth's own, kept as a
// separate type since these two commands do not share a "report" type, only
// the underlying parser.
type ParseFailure struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// buildManifest is buildManifestAtWatermark using this call's own listing
// pass to establish the watermark automatically: the highest mtime observed
// across every file it just listed. Callers that don't need to pin an
// explicit, possibly-older watermark (e.g. every existing test in this
// package) use this.
func buildManifest(root string) (Manifest, error) {
	metas, err := listRolloutFilesWithMTimes(root)
	if err != nil {
		return Manifest{}, err
	}
	return buildManifestFromMetas(root, metas, watermarkFromMetas(metas), "auto")
}

// buildManifestAtWatermark is the whole tool's read-only core, pinned to an
// explicitly supplied watermark rather than one this call derives on its
// own. It performs exactly one filesystem read pass (listRolloutFilesWithMTimes),
// partitions the result against watermark, then runs internal/rollout's
// shared analysis on every included file, turning its Turns into
// ManifestEntry values (hashing text, never retaining it). It never writes to
// root or to any file under it, and never opens the corpus.
func buildManifestAtWatermark(root string, watermark time.Time) (Manifest, error) {
	metas, err := listRolloutFilesWithMTimes(root)
	if err != nil {
		return Manifest{}, err
	}
	return buildManifestFromMetas(root, metas, watermark, "explicit")
}

// buildManifestFromMetas is the shared core both buildManifest and
// buildManifestAtWatermark reduce to: given the ONE listing pass already
// taken (metas) and the watermark to pin against, partition files into
// included/excluded (criteria 1, 2 and 4), then validate and analyze only
// the included files. metas is already sorted by path
// (rollout.WalkRolloutFiles sorts), so both the excluded list and every
// per-file loop below stay in deterministic, path-sorted order -- required
// for criterion 5 (a rerun at the same watermark reproduces the manifest
// exactly, including order).
func buildManifestFromMetas(root string, metas []fileMeta, watermark time.Time, watermarkSource string) (Manifest, error) {
	m := Manifest{
		Root:            root,
		Watermark:       watermark.UTC().Format(time.RFC3339Nano),
		WatermarkSource: watermarkSource,
		FilesTotal:      len(metas),
	}

	var included []fileMeta
	for _, meta := range metas {
		if meta.ModTime.After(watermark) {
			m.ExcludedAfterWatermark = append(m.ExcludedAfterWatermark, ExcludedFile{
				Path:    meta.Path,
				ModTime: meta.ModTime.UTC().Format(time.RFC3339Nano),
			})
			continue
		}
		included = append(included, meta)
	}
	m.FilesIncluded = len(included)

	for _, meta := range included {
		path := meta.Path
		if err := validateKnownRecordTypes(path); err != nil {
			m.FilesUnparseable = append(m.FilesUnparseable, ParseFailure{
				Path:   path,
				Reason: err.Error(),
			})
			continue
		}

		fa, err := rollout.AnalyzeFile(path)
		if err != nil {
			m.FilesUnparseable = append(m.FilesUnparseable, ParseFailure{
				Path:   path,
				Reason: err.Error(),
			})
			continue
		}
		m.FilesParsed++

		for _, t := range fa.Turns {
			entry := ManifestEntry{
				File:        path,
				SessionID:   t.SessionID,
				RecordIndex: t.LineNo,
				TextSHA256:  sha256Hex(t.Text),
				Source:      t.Source,
			}
			m.Entries = append(m.Entries, entry)
			switch t.Source {
			case "response_item":
				m.EntriesFromResponseItem++
			case "compacted":
				m.EntriesFromCompacted++
			}
		}

		m.Dedup.CompactedUserTurnsDistinctTotal += fa.CompactedUserTurnsDistinct
		m.Dedup.DroppedAsDuplicateOfResponseItem += fa.CompactedOverlapWithResponseItem
		m.Dedup.RecoveredOnlyInCompacted += fa.CompactedOnlyInCompacted
		if fa.CompactedUserTurnsDistinct > 0 {
			m.Dedup.PerFile = append(m.Dedup.PerFile, FileDedup{
				File:                             path,
				CompactedUserTurnsDistinct:       fa.CompactedUserTurnsDistinct,
				CompactedOverlapWithResponseItem: fa.CompactedOverlapWithResponseItem,
				CompactedOnlyInCompacted:         fa.CompactedOnlyInCompacted,
			})
		}
	}

	return m, nil
}

// PrintSummary writes the human-readable manifest summary (never the raw
// entries -- use -json for that) including the reconciliation line
// acceptance criterion 2 asks for.
func PrintSummary(w interface{ Write([]byte) (int, error) }, m Manifest, slice2Distinct, slice2Overlap, slice2Only int) {
	fmt.Fprintf(w, "root: %s\n", m.Root)
	fmt.Fprintf(w, "watermark: %s (%s)\n", m.Watermark, m.WatermarkSource)
	fmt.Fprintf(w, "rollout files seen: %d (included %d, excluded-after-watermark %d)\n",
		m.FilesTotal, m.FilesIncluded, len(m.ExcludedAfterWatermark))
	if len(m.ExcludedAfterWatermark) > 0 {
		fmt.Fprintln(w, "excluded (modified after watermark):")
		for _, f := range m.ExcludedAfterWatermark {
			fmt.Fprintf(w, "  %s (mtime %s)\n", f.Path, f.ModTime)
		}
	} else {
		fmt.Fprintln(w, "excluded (modified after watermark): none")
	}
	fmt.Fprintf(w, "rollout files included: %d (parsed %d, unparseable %d)\n", m.FilesIncluded, m.FilesParsed, len(m.FilesUnparseable))
	fmt.Fprintf(w, "manifest entries: %d (%d from response_item, %d recovered from compacted)\n",
		len(m.Entries), m.EntriesFromResponseItem, m.EntriesFromCompacted)
	fmt.Fprintf(w, "dedup account: %d distinct compacted turns, %d dropped as duplicate-of-response_item, %d recovered only-in-compacted\n",
		m.Dedup.CompactedUserTurnsDistinctTotal, m.Dedup.DroppedAsDuplicateOfResponseItem, m.Dedup.RecoveredOnlyInCompacted)

	agree := m.Dedup.CompactedUserTurnsDistinctTotal == slice2Distinct &&
		m.Dedup.DroppedAsDuplicateOfResponseItem == slice2Overlap &&
		m.Dedup.RecoveredOnlyInCompacted == slice2Only
	if agree {
		fmt.Fprintf(w, "reconciliation: AGREES with slice 2's measured distinct=%d overlap=%d only-in-compacted=%d\n",
			slice2Distinct, slice2Overlap, slice2Only)
	} else {
		fmt.Fprintf(w, "reconciliation: DISAGREES with slice 2's measured distinct=%d overlap=%d only-in-compacted=%d "+
			"(this run: distinct=%d overlap=%d only-in-compacted=%d) -- re-run capturehealth -json against the same "+
			"tree and compare before trusting either number\n",
			slice2Distinct, slice2Overlap, slice2Only,
			m.Dedup.CompactedUserTurnsDistinctTotal, m.Dedup.DroppedAsDuplicateOfResponseItem, m.Dedup.RecoveredOnlyInCompacted)
	}

	if len(m.FilesUnparseable) > 0 {
		fmt.Fprintln(w, "unparseable files:")
		for _, f := range m.FilesUnparseable {
			fmt.Fprintf(w, "  %s: %s\n", f.Path, f.Reason)
		}
	} else {
		fmt.Fprintln(w, "unparseable files: none")
	}
}
