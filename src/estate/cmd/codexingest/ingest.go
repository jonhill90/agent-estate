// ingest.go is codexingest's read-and-validate half: load a
// cmd/corpusextract manifest, re-derive each entry's identity and text
// against the SAME source files at the SAME positions, and decide, per
// entry, whether it is safe to insert -- never re-scanning the source tree,
// never trusting the manifest's own claim about a file's content without
// re-checking it against that file as it stands right now.
//
// # Why re-parse instead of trusting the manifest's hash outright
//
// The manifest (cmd/corpusextract) never carries raw text, only a hash of
// it -- by design, so a dry-run manifest can be reviewed and even committed
// without leaking operator words. That means THIS command, the write step,
// is the first (and only) place that needs the text back, and it can only
// get it by re-reading the exact file the manifest named. Re-reading is also
// exactly how "reject source change" (agent-estate#1139) is enforced: if a
// unit's freshly-computed hash no longer matches what the manifest recorded,
// the file changed after the manifest was built, and that unit is refused
// rather than ingested under a stale hash.
//
// # RecordIndex matches internal/provenance's contract, not corpusextract's
//
// internal/provenance.UnitProvenance.RecordIndex is documented as "0-based
// ordinal position among units extracted from SourceFile, in file order" --
// NOT a raw line number. cmd/corpusextract's own ManifestEntry.RecordIndex
// is a different, local field (1-based raw line number) belonging to that
// command's own type. This file's provRecordIndex is the position (0-based)
// of a manifest entry within ITS OWN FILE's slice of manifest entries, which
// is exactly internal/rollout.AnalyzeFile's own Turns order (response_item
// turns first, in file order, then CompactedOnlyInCompacted turns) --
// because that is the order buildManifestFromMetas appended entries in when
// the manifest was built. This file's whole-file turn-count check (below)
// is what makes relying on that positional equivalence safe rather than
// assumed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/provenance"
	"github.com/jonhill90/agent-estate/estate/internal/rollout"
)

// manifestEntry mirrors cmd/corpusextract's own ManifestEntry JSON shape --
// only the fields this command needs to decode, so a manifest field this
// command doesn't use (dedup accounting, excluded files, ...) is silently
// ignored by json.Unmarshal rather than requiring a full duplicate of that
// command's types.
type manifestEntry struct {
	File        string `json:"file"`
	SessionID   string `json:"session_id"`
	RecordIndex int    `json:"record_index"` // corpusextract's own meaning: 1-based raw line number
	TextSHA256  string `json:"text_sha256"`
	Source      string `json:"source"` // "response_item" or "compacted"
}

// manifest is the subset of cmd/corpusextract's Manifest this command reads.
type manifest struct {
	Root      string          `json:"root"`
	Watermark string          `json:"watermark"`
	Entries   []manifestEntry `json:"entries"`
}

func loadManifest(path string) (manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return manifest{}, err
	}
	defer f.Close()
	var m manifest
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return manifest{}, fmt.Errorf("decoding manifest %s: %w", path, err)
	}
	return m, nil
}

// Outcome names, one per UnitPlan -- never a silent zero, every manifest
// entry ends up in exactly one of these.
const (
	outcomePending         = "" // not yet decided against the db (buildPlan's job stops here)
	outcomeSourceChanged   = "source_changed_since_manifest"
	outcomeFileUnreadable  = "source_file_unreadable"
	outcomeInserted        = "inserted"
	outcomeAlreadyIngested = "already_ingested"
)

// UnitPlan is one manifest entry's own disposition: either a ready-to-insert
// UnitProvenance + its raw text (kept only in memory, only long enough to
// write it to -db), or a refusal with a reason. insertPlan (db.go) turns
// Pending plans into Inserted/AlreadyIngested against -db; it never touches
// a plan already carrying SourceChanged or FileUnreadable.
type UnitPlan struct {
	Entry           manifestEntry
	ProvRecordIndex int
	Provenance      provenance.UnitProvenance
	Text            string // raw operator text -- never logged, never printed, written only to -db's prompts.text_raw
	Outcome         string
	Reason          string
}

// buildPlan validates every manifest entry against a fresh, independent
// re-parse of its own source file (via the SAME internal/rollout parser
// cmd/corpusextract used to build the manifest -- never a second parser for
// this format) and returns one UnitPlan per entry, in the manifest's own
// order. It opens each source file exactly once (os.Open via
// rollout.AnalyzeFile, read-only) regardless of how many entries in the
// manifest reference it, and touches no other path -- it never walks a
// directory, so a file the manifest doesn't name is never read.
func buildPlan(entries []manifestEntry) []UnitPlan {
	plans := make([]UnitPlan, len(entries))

	// Group entry indices by File, preserving first-seen file order and
	// each file's own entry order -- both are exactly the order the
	// manifest already carries them in, since buildManifestFromMetas
	// (cmd/corpusextract) iterated files in that same order and appended
	// each file's entries via a single AnalyzeFile call in Turns order.
	var files []string
	byFile := map[string][]int{}
	for i, e := range entries {
		if _, seen := byFile[e.File]; !seen {
			files = append(files, e.File)
		}
		byFile[e.File] = append(byFile[e.File], i)
	}

	for _, file := range files {
		idxs := byFile[file]
		for local, gi := range idxs {
			plans[gi] = UnitPlan{Entry: entries[gi], ProvRecordIndex: local}
		}

		fa, err := rollout.AnalyzeFile(file)
		if err != nil {
			for _, gi := range idxs {
				plans[gi].Outcome = outcomeFileUnreadable
				plans[gi].Reason = err.Error()
			}
			continue
		}

		// A different turn count than the manifest recorded means this
		// file's own shape changed since the manifest was built (a line
		// added, removed, or a compacted record's replacement_history
		// grew/shrank) -- positional matching below is no longer provably
		// safe for ANY entry in this file, so every entry from it is
		// refused, not just the ones whose position happens to look wrong.
		if len(fa.Turns) != len(idxs) {
			reason := fmt.Sprintf(
				"source file's own turn count changed since the manifest was built "+
					"(manifest recorded %d unit(s) for this file, live re-parse found %d) -- "+
					"refusing every unit from this file rather than guessing which ones still line up",
				len(idxs), len(fa.Turns))
			for _, gi := range idxs {
				plans[gi].Outcome = outcomeSourceChanged
				plans[gi].Reason = reason
			}
			continue
		}

		for local, gi := range idxs {
			turn := fa.Turns[local]
			e := entries[gi]

			if turn.LineNo != e.RecordIndex || turn.Source != e.Source || turn.SessionID != e.SessionID {
				plans[gi].Outcome = outcomeSourceChanged
				plans[gi].Reason = fmt.Sprintf(
					"position %d in this file no longer matches the manifest entry "+
						"(manifest: line %d, source %q, session %q; live: line %d, source %q, session %q)",
					local, e.RecordIndex, e.Source, e.SessionID, turn.LineNo, turn.Source, turn.SessionID)
				continue
			}

			hash := provenance.HashContent(turn.Text)
			if hash != e.TextSHA256 {
				plans[gi].Outcome = outcomeSourceChanged
				plans[gi].Reason = fmt.Sprintf(
					"text hash no longer matches the manifest entry (manifest: %s, live: %s) -- "+
						"source file content changed since the manifest was built", e.TextSHA256, hash)
				continue
			}

			plans[gi].Text = turn.Text
			plans[gi].Provenance = provenance.UnitProvenance{
				SourceName:  "codex-rollout",
				Harness:     "codex",
				SourceFile:  file,
				SessionID:   turn.SessionID,
				RecordIndex: local,
				ContentHash: hash,
				CapturedAt:  turn.CapturedAt,
			}
			plans[gi].Outcome = outcomePending
		}
	}

	return plans
}

// atFor derives the prompts.at value (INTEGER, NOT NULL) for one plan:
// the record's own CapturedAt, parsed, if it is present and parses as
// RFC3339 or RFC3339Nano; otherwise the manifest's own recorded watermark
// instant. Both are values already fixed before this run started -- never
// time.Now() -- so two runs against the same manifest compute the identical
// `at` for the identical unit.
func atFor(p UnitPlan, watermark time.Time) int64 {
	if p.Provenance.CapturedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, p.Provenance.CapturedAt); err == nil {
			return t.Unix()
		}
		if t, err := time.Parse(time.RFC3339, p.Provenance.CapturedAt); err == nil {
			return t.Unix()
		}
	}
	return watermark.Unix()
}
