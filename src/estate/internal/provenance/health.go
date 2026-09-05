package provenance

// SourceState is a source's own reachability, kept deliberately separate
// from whether it has any content -- agent-estate#1139's own complaint about
// the pre-slice-3 corpus is that its ONE global capture_health number
// "renders 'Claude fresh, Codex entirely absent' identically to 'everything
// slightly stale'". A source that cannot be reached at all, a source that
// was reached but could not be read, and a source that was read and is
// genuinely empty are three different answers, matching the absence
// discipline this repo already uses for knowledge retrieval (`no_match` /
// `index_missing` / `index_unreadable`, see AGENTS.md's knowledge-retrieval
// section) and for cost figures (`cost.Figure.Known` in src/tui). None of
// the four states below is a bare zero value standing in for "we don't
// know" -- SourceStateUnknown exists explicitly for that, so a
// zero-initialized SourceHealth is never silently read as SourceStateEmpty.
type SourceState int

const (
	// SourceStateUnknown is the zero value: nobody has classified this
	// source yet. A caller must never treat this as SourceStateEmpty --
	// see the package-level discussion above.
	SourceStateUnknown SourceState = iota

	// SourceStateMissing means the source's own root (a directory, a file,
	// whatever the source's shape requires) does not exist at all. For
	// Codex rollouts: `~/.codex/sessions` itself is absent -- e.g. a machine
	// that has never run Codex.
	SourceStateMissing

	// SourceStateUnreadable means the source's root exists but could not be
	// read -- a permission error, a corrupt filesystem entry, or (for a
	// non-filesystem source later) a connection failure. Distinct from
	// SourceStateMissing: the source is known to exist, but this reader
	// could not see into it.
	SourceStateUnreadable

	// SourceStateEmpty means the source was reached and read successfully
	// and genuinely contains zero extractable units. This is a real answer,
	// not blindness -- mirrors src/tui's cost.Snapshot doc comment: "An
	// empty Harnesses with Known true means ccusage ran and genuinely found
	// no usage yet today: a real answer, not blindness."
	SourceStateEmpty

	// SourceStatePopulated means the source was reached, read successfully,
	// and contains at least one extractable unit.
	SourceStatePopulated
)

// String renders a SourceState for human-readable output. Never used for
// JSON -- SourceHealth's own json tag encodes the int; a consumer wanting
// the label decodes the int and calls this, so the wire format never
// depends on these exact strings staying stable.
func (s SourceState) String() string {
	switch s {
	case SourceStateMissing:
		return "missing"
	case SourceStateUnreadable:
		return "unreadable"
	case SourceStateEmpty:
		return "empty"
	case SourceStatePopulated:
		return "populated"
	default:
		return "unknown"
	}
}

// ParseFailure names one source-specific unit (a file, a record, whatever
// the source's own shape is) this source's reader could not parse, and why.
// A source must always report these explicitly rather than skipping a
// failure silently -- agent-estate#1139's own correction 3 ("refuse on
// unknown shape rather than skipping silently") generalized to the contract
// level.
type ParseFailure struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Freshness is how recently a source's own content was captured, with
// "unknown" as a typed value rather than a zero duration -- same discipline
// as src/tui's cost.Figure.Known: "a zero reads as nothing spent, and this
// estate has been bitten by exactly that silent-blindness three times."
// Applied here to time instead of spend: a SecondsSinceCapture of 0 must
// never be produced by "we couldn't tell", only by "the newest unit really
// was captured this instant".
type Freshness struct {
	// Known is false when the source has no unit to measure freshness from
	// at all (SourceStateMissing, SourceStateUnreadable, or
	// SourceStateEmpty) or when a unit exists but its own capture time could
	// not be parsed. NewestCapturedAt and SecondsSinceCapture are meaningless
	// when Known is false and must not be printed or compared.
	Known bool `json:"known"`

	// NewestCapturedAt is the newest unit's own capture timestamp, RFC3339.
	NewestCapturedAt string `json:"newest_captured_at,omitempty"`

	// SecondsSinceCapture is the age of NewestCapturedAt as of the moment
	// this SourceHealth was built, in whole seconds. Computed once at build
	// time, not re-derived by a later reader against its own clock, so a
	// report saved and re-read later still shows the age it actually had
	// when measured.
	SecondsSinceCapture int64 `json:"seconds_since_capture,omitempty"`
}

// SourceHealth is what capturehealth (or any future equivalent) must report
// for ANY source -- agent-estate#1139 slice 3 acceptance criterion 2. Codex
// rollout JSONL is the first implementation (cmd/capturehealth/contract.go
// converts its own Codex-specific Report into this shape); a second source
// (Claude transcripts, and later others) populates the exact same fields
// rather than inventing new ones. Nothing below is Codex-specific: no field
// named after a Codex record type, no assumption that "unit" means "rollout
// JSONL line".
type SourceHealth struct {
	// SourceName and Harness identify which source and which coding-agent
	// CLI this health report is about -- same two identity fields
	// UnitProvenance carries, so a SourceHealth and the units it describes
	// join on the same two strings.
	SourceName string `json:"source_name"`
	Harness    string `json:"harness"`

	// Root is the source's own root as read (a directory path today; a
	// future non-filesystem source states whatever "root" means for it --
	// a base URL, a bucket name -- as a string here rather than adding a
	// second, source-specific field).
	Root string `json:"root"`

	State SourceState `json:"state"`

	// FilesTotal/FilesParsed/FilesUnparseable are named generically enough
	// to survive a source whose own unit of file-like input isn't literally
	// an OS file (a source file's "file" could be a rollout JSONL, a
	// transcript directory, or a remote object) -- they still mean "how many
	// discrete containers did this source have, and how many parsed".
	FilesTotal       int            `json:"files_total"`
	FilesParsed      int            `json:"files_parsed"`
	FilesUnparseable []ParseFailure `json:"files_unparseable,omitempty"`

	// UnitsExtracted is the count of genuine extractable units this source
	// found -- Codex: genuine operator turns (role=="user" AND
	// content[0].type=="input_text"); a future source states its own
	// positive-filter count here, using whatever definition of "genuine
	// unit" that source's own contract-conversion code documents.
	UnitsExtracted int `json:"units_extracted"`

	Freshness Freshness `json:"freshness"`
}
