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
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/jonhill90/agent-estate/estate/internal/rollout"
)

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
	Root                    string          `json:"root"`
	FilesTotal              int             `json:"files_total"`
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

// buildManifest is the whole tool's read-only core: list files, run
// internal/rollout's shared analysis on each, and turn its Turns into
// ManifestEntry values (hashing text, never retaining it). It never writes
// to root or to any file under it, and never opens the corpus.
func buildManifest(root string) (Manifest, error) {
	files, err := rollout.WalkRolloutFiles(root)
	if err != nil {
		return Manifest{}, err
	}

	m := Manifest{
		Root:       root,
		FilesTotal: len(files),
	}

	for _, path := range files {
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
	fmt.Fprintf(w, "rollout files seen: %d (parsed %d, unparseable %d)\n", m.FilesTotal, m.FilesParsed, len(m.FilesUnparseable))
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
