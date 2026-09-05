// Package provenance is the K2 slice 3 contract for agent-estate#1139: the
// declared shape of "where did this ingested unit come from" (UnitProvenance)
// and "how healthy is this source to read" (SourceHealth, in health.go),
// written once so a second source -- Claude transcripts, and whatever comes
// after -- implements the SAME contract instead of inventing its own fields.
//
// This package settles the contract only. It writes nothing: no corpus row,
// no file, no ledger entry. Deciding the storage format the corpus will
// eventually hold these in (markdown, SQLite, DuckDB -- agent-estate#1019)
// is explicitly out of scope here and is not settled by this package's shape
// existing; a Go struct is not a store.
//
// Codex rollout JSONL (cmd/capturehealth, and the S4 extractor being built
// alongside this slice under cmd/corpusextract) is the first, but not the
// only, implementation of this contract. Nothing in this package assumes
// Codex's file layout, record types, or field names -- those live in
// cmd/capturehealth, which converts its own Codex-specific analysis into
// these generic shapes (see cmd/capturehealth/contract.go).
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// UnitProvenance is one ingested unit's own record of where it came from.
// "Unit" here is deliberately source-agnostic: for Codex rollouts it is one
// genuine operator turn (agent-estate#1139's own positive filter,
// role=="user" AND content[0].type=="input_text"); a future source defines
// its own notion of "unit" but must still populate every field below.
//
// # Identity vs. metadata
//
// Six fields are IDENTITY: together they say "this is the same unit" or "this
// is a different one". A backfill re-run must derive the exact same ID (see
// ID below) from the exact same six values every time -- that is what makes
// re-running the backfill idempotent rather than duplicating rows
// (agent-estate#1139's own "idempotent ids so re-running the backfill cannot
// duplicate"). Changing any one of the six means a genuinely different unit,
// never the same unit observed again:
//
//   - SourceName   which adapter produced this record ("codex-rollout",
//     "claude-transcript", ...). Two sources reading the same underlying
//     file would still be different SourceNames if they extract different
//     units from it.
//   - Harness      which coding-agent CLI the source file itself came from
//     ("codex", "claude", ...). Kept distinct from SourceName because one
//     harness can eventually have more than one source (e.g. a live capture
//     path AND a backfill-from-disk path for the same harness).
//   - SourceFile   the source file's own path, exactly as read. Two units
//     from two different files are different units even if their text is
//     identical.
//   - SessionID    the source's own session identifier (Codex:
//     session_meta.payload.id -- never a field named session_id, per
//     agent-estate#1139 correction 1). Empty string is a real, typed value
//     (agent-estate#1139 measured turns can precede any session_meta), never
//     coerced to a placeholder.
//   - RecordIndex  the unit's own 0-based ordinal position among units
//     extracted from SourceFile, in file order. This is NOT the raw JSONL
//     line number: a rollout file mixes response_item, event_msg,
//     turn_context and other record types, and RecordIndex counts only
//     units this source's positive filter actually extracted -- so it is
//     stable against a future record type being added between two existing
//     units, which a line number is not.
//   - ContentHash  sha256 hex of the unit's own extracted text, independent
//     of surrounding JSON formatting. Included in identity (not left as pure
//     metadata) specifically because of agent-estate#1139's compacted-overlap
//     finding: a compacted record's replacement_history can re-embed the
//     SAME operator text that also appears as an ordinary response_item in
//     the same file. Two extraction paths landing on the same SourceFile +
//     SessionID + RecordIndex position but different surrounding record
//     shapes must still collapse to one unit when their text is identical --
//     ContentHash is what makes that collapse exact rather than heuristic.
//
// Everything else is METADATA: descriptive, useful for display or debugging,
// and never part of identity. A metadata field changing (a corrected
// timestamp, a re-derived originator) does not make this a different unit.
//
//   - CapturedAt   the record's own timestamp, RFC3339 if the source
//     provides one and it parses; "" if absent or unparsable. Never
//     defaulted to "now" -- an unknown capture time is absence, not zero
//     (see health.go's Freshness for the same discipline applied at the
//     per-source level).
//   - Originator   the source's own notion of what produced the record
//     (Codex: payload.originator -- present in the real tree, shape not
//     stable across forked/subagent sessions, so left as an opaque string
//     rather than a typed field).
//   - OriginSource the source's own notion of where the record entered the
//     system (Codex: payload.source -- named OriginSource, not Source, so it
//     is never confused with this type's own SourceName identity field).
//   - PriorAssistantContext the immediately preceding assistant turn's own
//     text, when the source's ingestion policy carries it. agent-estate#1139's
//     scope is explicit that this is "previous assistant context only, as
//     context -- not as operator text": it must never be folded into the
//     text ContentHash is computed over, and a consumer must never treat it
//     as something the operator said.
type UnitProvenance struct {
	// Identity -- see the six-field list above.
	SourceName  string `json:"source_name"`
	Harness     string `json:"harness"`
	SourceFile  string `json:"source_file"`
	SessionID   string `json:"session_id"`
	RecordIndex int    `json:"record_index"`
	ContentHash string `json:"content_hash"`

	// Metadata -- see the list above. Never part of ID().
	CapturedAt            string `json:"captured_at,omitempty"`
	Originator            string `json:"originator,omitempty"`
	OriginSource          string `json:"origin_source,omitempty"`
	PriorAssistantContext string `json:"prior_assistant_context,omitempty"`
}

// HashContent is the ONE way ContentHash is computed anywhere this contract
// is implemented -- sha256 hex of the unit's own extracted text, nothing
// else mixed in. A source-specific extractor must call this rather than
// hashing its own way, so two independent implementations of this contract
// (Codex today, Claude transcripts later) produce comparable hashes for
// identical text.
func HashContent(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// ID is this unit's own idempotent identifier: derived only from the six
// identity fields, in a fixed order, joined so no field's own contents can
// be mistaken for a delimiter (each field is length-prefixed). Two calls
// against equal identity fields -- whether from the same extractor run twice
// or two independent runs -- produce the exact same ID, which is what makes
// a backfill re-run idempotent (agent-estate#1139's own requirement) rather
// than dependent on the store's own dedup logic.
func (p UnitProvenance) ID() string {
	var b strings.Builder
	for _, field := range []string{
		p.SourceName,
		p.Harness,
		p.SourceFile,
		p.SessionID,
		strconv.Itoa(p.RecordIndex),
		p.ContentHash,
	} {
		b.WriteString(strconv.Itoa(len(field)))
		b.WriteByte(':')
		b.WriteString(field)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
