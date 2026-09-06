// Package catalogue is the durable answer to "what sources does this estate
// ingest from, and what is known about each?" (agent-estate#1139 gate 5).
//
// A Source is a record, not a store: name, harness, root path, which fields
// identify one of its units, a current typed health state, and the unit
// count last observed together with the instant it was observed at -- the
// sources this package reads are the operator's own LIVE, growing
// conversation history, so any count is a snapshot, never a fact that holds
// after the moment it was taken.
//
// This package never writes to a source. Every read is a directory listing,
// an os.Stat, or (for Codex rollouts specifically) the same read-only
// internal/rollout parse cmd/capturehealth already uses -- os.Open only,
// never O_RDWR, never os.Create, never os.Remove, and nothing here calls
// os.Chtimes. It also never opens ~/corpus/ledger.sqlite3: a catalogue of
// sources is not itself a corpus query.
//
// Two sources are seeded because two are real: Codex rollout JSONL under
// ~/.codex/sessions, and Claude Code's own session transcripts under
// ~/.claude/projects/*/*.jsonl. Neither existing on this machine is not this
// package's problem to paper over -- a missing root is recorded as
// HealthMissing with the exact path looked for, never silently omitted.
//
// A third-and-fourth pair of records cover the two seed PDF artifacts named
// in the Agent Memory vault fact "seed-knowledge-source-artifacts" (created
// 2026-09-05T03:04:27Z). A prior turn recorded these two as HealthMissing
// with "referent unresolved" (agent-estate#1236) after a search that covered
// this repository, its issues, the corpus, and the filesystem excluding
// ~/Downloads -- but never queried the memory vault, which is precisely
// where the operator had already declared the two artifacts. That search is
// now known to have been incomplete, not the referent genuinely absent: see
// SeedPDFDescriptors below, each entry sourced directly from that fact, with
// this package's own from-scratch SHA-256 re-verification recorded in
// BuildSeedPDFSource's Detail rather than trusted from the fact alone.
package catalogue

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/rollout"
)

// HealthState is the four-state vocabulary this package reports every
// source's health in. It is deliberately not a bare bool or a zero value:
// a source this package could not even find is a different fact from one it
// found but could not read, which is a different fact again from one it
// read successfully and found to hold nothing.
//
// This vocabulary was not yet on main (github.com/jonhill90/agent-estate
// @f480032) when this package was written -- cmd/capturehealth and
// internal/codexrollout, the two sibling K2 slices, report counts but do not
// classify a source into named health states. This is this package's own
// typed enum, not a reuse of an existing one; if a shared vocabulary lands
// on main later, migrate this package onto it rather than keeping two.
type HealthState int

const (
	// HealthMissing means the root path does not exist at all.
	HealthMissing HealthState = iota
	// HealthUnreadable means the root path exists but this package could not
	// list or read it (e.g. permission denied).
	HealthUnreadable
	// HealthEmpty means the root path exists, was read successfully, and
	// contains zero units.
	HealthEmpty
	// HealthPopulated means the root path exists, was read successfully, and
	// contains at least one unit.
	HealthPopulated
)

// String renders the health state the same way in JSON (via MarshalJSON)
// and in any human-facing output, so the two never drift apart.
func (h HealthState) String() string {
	switch h {
	case HealthMissing:
		return "Missing"
	case HealthUnreadable:
		return "Unreadable"
	case HealthEmpty:
		return "Empty"
	case HealthPopulated:
		return "Populated"
	default:
		return "Unknown"
	}
}

// MarshalJSON emits the health state as its name, never its underlying int
// -- a bare 0/1/2/3 in the catalogue JSON would silently break the moment
// this list is reordered.
func (h HealthState) MarshalJSON() ([]byte, error) {
	return []byte(`"` + h.String() + `"`), nil
}

// Source is one durable record: everything this package knows about one
// ingestion source, as of ObservedAt.
type Source struct {
	// Name is a short, stable identifier for this source, e.g. "codex-rollouts".
	Name string `json:"name"`
	// Harness is the harness this source belongs to -- "claude" or "codex".
	Harness string `json:"harness"`
	// RootPath is the directory (for Codex/Claude) or file (for a seed PDF)
	// this source's units live under. An empty string means
	// BuildCodexSource/BuildClaudeSource could not resolve a home directory
	// to build the default root (see Detail) -- a real path was attempted
	// and failed to resolve. Do not read an empty RootPath as "root is the
	// filesystem root" (Go's os package never returns "" for that); Detail
	// always carries the fuller explanation for the specific row.
	RootPath string `json:"root_path"`
	// IdentityFields names which fields, together, identify one unit of this
	// source -- e.g. which JSON field(s) a caller must key on to tell two
	// units apart or recognize the same unit seen twice.
	IdentityFields []string `json:"identity_fields"`
	// Health is this source's current typed health state.
	Health HealthState `json:"health"`
	// UnitCount is how many units were observed the last time this source
	// was read. Meaningless without ObservedAt alongside it -- see that
	// field's own comment.
	UnitCount int `json:"unit_count"`
	// ObservedAt is the instant UnitCount was measured. The sources this
	// package reads are live and growing; a UnitCount with no attached
	// instant is a claim with no expiry, which is worse than no claim at
	// all. Zero value (time.Time{}) means this source was never
	// successfully read (Health is Missing or Unreadable) and UnitCount is
	// not meaningful.
	ObservedAt time.Time `json:"observed_at"`
	// Detail carries a short, human-readable note -- why a source is
	// Missing or Unreadable, or what "unit" means for a Populated one. Never
	// omitted just because a source is healthy: a Populated source's Detail
	// says what was counted, so a reader does not have to infer it from
	// IdentityFields alone.
	Detail string `json:"detail"`

	// The six fields below were added for the two seed-PDF artifact records
	// (agent-estate#1139 K2, per the Agent Memory fact
	// "seed-knowledge-source-artifacts": "K2 must catalogue their authority,
	// scope, sensitivity/access policy, freshness, owner, and rebuild path").
	// They are not meaningful for every source -- Codex rollouts and Claude
	// transcripts are live, first-party, unpublished operator data with no
	// external "rebuild" concept -- but every source populates them rather
	// than leaving them blank with no explanation, for the same reason a
	// Populated source always states its Detail: a reader should never have
	// to guess whether an empty field means "not applicable" or "forgotten."

	// Authority states who or what stands behind this source's contents and
	// how much that backing is worth -- e.g. "first-party, harness-written"
	// versus "unreviewed preprint, not accepted truth."
	Authority string `json:"authority"`
	// Scope states what this source does and does not cover -- one document,
	// one machine's own session history, etc.
	Scope string `json:"scope"`
	// SensitivityAccess states this source's access policy -- who may read
	// it, whether it may be published, and any handling constraint (e.g.
	// "never enters git").
	SensitivityAccess string `json:"sensitivity_access"`
	// Freshness states how current this source's content is and how that
	// currency was established -- a fixed publication date for a static
	// document, "live and append-only" for a growing transcript log.
	Freshness string `json:"freshness"`
	// Owner names who is accountable for this source existing and being
	// accurate -- almost always the operator, stated explicitly rather than
	// left implicit.
	Owner string `json:"owner"`
	// RebuildPath states how this source's content could be reacquired if
	// lost -- a canonical URL to re-fetch, or "cannot be rebuilt" when there
	// is no such path.
	RebuildPath string `json:"rebuild_path"`

	// RecordedSHA256 and ObservedSHA256 are populated only for a source
	// verified against a previously recorded identity hash (currently: the
	// two seed PDFs, checked against the SHA-256 values in the Agent Memory
	// fact named above). Both empty means no hash verification applies to
	// this source. RecordedSHA256 is the hash the fact claims; ObservedSHA256
	// is what BuildSeedPDFSource actually computed from the live file this
	// run. A mismatch between the two is a finding to report in Detail, not
	// something this package silently corrects -- see BuildSeedPDFSource.
	RecordedSHA256 string `json:"recorded_sha256,omitempty"`
	ObservedSHA256 string `json:"observed_sha256,omitempty"`
}

// Catalogue is every known source, as of the moment Build ran.
type Catalogue struct {
	Sources []Source `json:"sources"`
}

// DefaultCodexRoot is ~/.codex/sessions, the root cmd/capturehealth already
// reads.
func DefaultCodexRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

// DefaultClaudeRoot is ~/.claude/projects: one subdirectory per project, one
// *.jsonl file per session, named `<sessionId>.jsonl`.
func DefaultClaudeRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// Build returns the catalogue seeded with the estate's two real
// conversation-transcript sources plus the two seed-PDF artifact records
// (agent-estate#1139 K2), each read once, read-only, right now.
func Build() Catalogue {
	sources := []Source{
		BuildCodexSource(DefaultCodexRoot()),
		BuildClaudeSource(DefaultClaudeRoot()),
	}
	for _, d := range SeedPDFDescriptors {
		sources = append(sources, BuildSeedPDFSource(d))
	}
	return Catalogue{Sources: sources}
}

// SeedPDFDescriptor is the fixed, hand-verified provenance for one seed PDF
// artifact, sourced from the Agent Memory vault fact
// "seed-knowledge-source-artifacts" (created 2026-09-05T03:04:27Z, source:
// Jon, 2026-09-05). This package does not discover these paths or hashes on
// its own -- an operator-declared fact is the authority for which two files
// these are, and BuildSeedPDFSource's job is to verify that fact against the
// live file, never to (re)search for it.
type SeedPDFDescriptor struct {
	// Name is a short, stable identifier for this record.
	Name string
	// Path is the file's location under the operator's own $HOME/Downloads.
	// Reads of this path are metadata-only (os.Stat, and a full-file read
	// used solely to compute a SHA-256 and count structural page markers --
	// see BuildSeedPDFSource) -- never a text or content extraction.
	Path string
	// RecordedSHA256 is the hash the memory fact claims for this file.
	RecordedSHA256 string
	// IdentityFields names which fields, together, identify this artifact.
	IdentityFields    []string
	Authority         string
	Scope             string
	SensitivityAccess string
	Freshness         string
	Owner             string
	RebuildPath       string
}

// SeedPDFDescriptors is the two artifacts named in the memory fact. Both
// entries carry Jon's own status caveat from that fact: these are
// candidates awaiting Estate review, not accepted truth, and one is an
// unreviewed preprint -- this catalogue must not imply otherwise.
var SeedPDFDescriptors = []SeedPDFDescriptor{
	{
		Name:           "seed-pdf-continual-harness",
		Path:           filepath.Join(os.Getenv("HOME"), "Downloads", "2605.09998v1.pdf"),
		RecordedSHA256: "50f60996b0d962cdbf01f5b6a262ccd68b149c8b67cc2594218453717ea0c45e",
		IdentityFields: []string{"arXiv id (2605.09998v1)", "sha256"},
		Authority: "arXiv preprint arXiv:2605.09998v1, \"Continual Harness: Online Adaptation for " +
			"Self-Improving Foundation Agents\". Unreviewed and not peer-reviewed -- per the memory " +
			"fact's own status caveat, treat as a preprint candidate, not accepted truth, until an " +
			"Estate review distils any of its claims.",
		Scope: "One paper, 28 pages. A candidate seed source for later evidence-based distillation " +
			"into skills or decisions -- not itself knowledge until that review happens.",
		SensitivityAccess: "Local file under the operator's own $HOME/Downloads; never committed to git " +
			"and never had its content read by this catalogue -- only size, mtime, SHA-256 and a " +
			"structural page-object count were read. Readable by the operator's own account only " +
			"(standard $HOME file permissions); no separate access policy has been set.",
		Freshness: "Preprint version v1 (arXiv versions after v1, if any, are a different artifact); " +
			"declared in the memory fact on 2026-09-05T03:04:27Z; not re-fetched or re-verified beyond " +
			"this catalogue run's own SHA-256 check.",
		Owner: "Jon (operator); acquired and placed at this path by him, catalogued per agent-estate#1139.",
		RebuildPath: "Re-download arXiv:2605.09998v1 specifically (https://arxiv.org/abs/2605.09998) and " +
			"verify the re-fetched file's SHA-256 against RecordedSHA256 before treating it as the same " +
			"artifact -- a later arXiv version would not match.",
	},
	{
		Name:           "seed-pdf-agentic-engineering-google",
		Path:           filepath.Join(os.Getenv("HOME"), "Downloads", "Agentic Engineering - Google.pdf"),
		RecordedSHA256: "76cb2eb6ce4789380ed45f2dbb9009f92a363bb2b60a16bfd25d71694438b691",
		IdentityFields: []string{"sha256"},
		Authority: "Unattributed beyond the filename's own \"Google\" credit; per the memory fact, " +
			"provenance and content review are pending. The PDF's own metadata (Producer: Adobe PDF " +
			"Library 18.0) does not itself establish authorship. Treat as candidate, not accepted truth.",
		Scope: "One document, 51 pages.",
		SensitivityAccess: "Local file under the operator's own $HOME/Downloads; never committed to git " +
			"and never had its content read by this catalogue -- only size, mtime, SHA-256 and a " +
			"structural page-object count were read. Readable by the operator's own account only.",
		Freshness: "PDF metadata records a creation/modification date of 2026-06-18 (read via file " +
			"metadata, not verified against any canonical published version); declared in the memory " +
			"fact on 2026-09-05T03:04:27Z.",
		Owner: "Jon (operator); acquired and placed at this path by him.",
		RebuildPath: "No canonical source URL has been identified yet. Per the memory fact's own " +
			"source-discovery loop, record the acquisition source here once it is known; until then " +
			"this artifact has no rebuild path and would need to be re-supplied by the operator.",
	},
}

// pdfPageObjectPattern matches a PDF's own /Type /Page dictionary entry --
// the structural marker every general-purpose PDF library (pdfinfo among
// them) keys off to count pages. It also matches the page-tree root's
// /Type /Pages entry, which countPDFPageObjects excludes separately.
var pdfPageObjectPattern = regexp.MustCompile(`/Type\s*/Page`)

// countPDFPageObjects counts PDF page objects by counting this byte-level
// structural marker, never by parsing or rendering any page's text, image,
// or content stream -- it reads the PDF's own object-dictionary tags, not
// its content. This was cross-checked against pdfinfo's authoritative parse
// for both seed artifacts before being trusted here (28 and 51 respectively,
// matching pdfinfo exactly for both files as verified for this PR).
func countPDFPageObjects(data []byte) int {
	locs := pdfPageObjectPattern.FindAllIndex(data, -1)
	count := 0
	for _, loc := range locs {
		if loc[1] < len(data) && data[loc[1]] == 's' {
			continue // "/Type /Pages": the page-tree root, not a page object.
		}
		count++
	}
	return count
}

// BuildSeedPDFSource verifies one seed PDF descriptor against the live file
// and returns its Source record. It never reads the file more than once,
// never writes to it, and treats a hash mismatch as a finding to report, not
// something to silently correct: if RecordedSHA256 (from the memory fact) no
// longer matches ObservedSHA256 (computed here, now), the fact is stale and
// Detail says so explicitly rather than picking one value to trust quietly.
func BuildSeedPDFSource(d SeedPDFDescriptor) Source {
	src := Source{
		Name:              d.Name,
		Harness:           "pdf",
		RootPath:          d.Path,
		IdentityFields:    d.IdentityFields,
		Authority:         d.Authority,
		Scope:             d.Scope,
		SensitivityAccess: d.SensitivityAccess,
		Freshness:         d.Freshness,
		Owner:             d.Owner,
		RebuildPath:       d.RebuildPath,
		RecordedSHA256:    d.RecordedSHA256,
	}

	info, err := os.Stat(d.Path)
	if err != nil {
		if os.IsNotExist(err) {
			src.Health = HealthMissing
			src.Detail = "file does not exist at the path recorded in the memory fact: " + d.Path
			return src
		}
		src.Health = HealthUnreadable
		src.Detail = "os.Stat failed: " + err.Error()
		return src
	}
	if info.IsDir() {
		src.Health = HealthUnreadable
		src.Detail = "path exists but is a directory, not a file: " + d.Path
		return src
	}

	data, err := os.ReadFile(d.Path)
	if err != nil {
		src.Health = HealthUnreadable
		src.Detail = "could not read file: " + err.Error()
		return src
	}

	sum := sha256.Sum256(data)
	observed := hex.EncodeToString(sum[:])
	src.ObservedSHA256 = observed
	pages := countPDFPageObjects(data)
	src.ObservedAt = time.Now()
	src.UnitCount = pages
	src.Health = HealthPopulated

	if observed == d.RecordedSHA256 {
		src.Detail = "SHA-256 verified: observed hash matches the value recorded in the Agent Memory " +
			"fact \"seed-knowledge-source-artifacts\"; " + strconv.Itoa(pages) + " page object(s) " +
			"counted from structural /Type/Page markers -- metadata only, no page content read, " +
			"extracted, quoted, or stored."
	} else {
		src.Detail = "SHA-256 MISMATCH: observed " + observed + " does not match the recorded fact's " +
			d.RecordedSHA256 + " -- the memory fact is stale or this file has changed since it was " +
			"recorded. This is a finding to report, not something this package corrects on its own; " +
			"do not treat this record as verified until the discrepancy is resolved. " +
			strconv.Itoa(pages) + " page object(s) counted from structural /Type/Page markers -- " +
			"metadata only, no page content read, extracted, quoted, or stored."
	}
	return src
}

// BuildCodexSource inspects one Codex rollout root and returns its Source
// record. root is a parameter (not hardcoded to defaultCodexRoot) so a
// caller -- test or CLI flag -- can point this at a fixture tree.
//
// Unit = one genuine operator turn (internal/rollout.GenuineOperatorTurn:
// role=="user" AND content[0].type=="input_text"), the same predicate
// cmd/capturehealth already reports on. Identity = the session it belongs
// to (session_meta.payload.id) plus the line position within its file --
// internal/rollout's own doc comment names why that pair, not payload.id
// alone, is what this format actually supports: no record type after
// session_meta carries an explicit session id of its own.
func BuildCodexSource(root string) Source {
	src := Source{
		Name:              "codex-rollouts",
		Harness:           "codex",
		RootPath:          root,
		IdentityFields:    []string{"session_meta.payload.id (session)", "line position within file (turn ordinal)"},
		Authority:         "First-party: the Codex harness's own rollout log of the operator's live sessions, not a secondary or republished source.",
		Scope:             "This machine's own ~/.codex/sessions tree; one operator's own working sessions only, not a shared or multi-user corpus.",
		SensitivityAccess: "Local only; contains raw operator prompts and harness output. Never publish, paste into anything public, or commit to git.",
		Freshness:         "Live and append-only; freshness is ObservedAt itself, re-measured each time this source is rebuilt.",
		Owner:             "Jon (operator), on this machine.",
		RebuildPath:       "Cannot be rebuilt or reacquired from elsewhere -- it is observed directly wherever the harness itself writes, not fetched from a canonical location.",
	}

	if root == "" {
		src.Health = HealthMissing
		src.Detail = "could not resolve a home directory to build the default root"
		return src
	}

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			src.Health = HealthMissing
			src.Detail = "root path does not exist: " + root
			return src
		}
		src.Health = HealthUnreadable
		src.Detail = "os.Stat failed: " + err.Error()
		return src
	}
	if !info.IsDir() {
		src.Health = HealthUnreadable
		src.Detail = "root path exists but is not a directory: " + root
		return src
	}

	files, err := rollout.WalkRolloutFiles(root)
	if err != nil {
		src.Health = HealthUnreadable
		src.Detail = "could not walk root: " + err.Error()
		return src
	}
	if len(files) == 0 {
		src.Health = HealthEmpty
		src.ObservedAt = time.Now()
		src.Detail = "root exists and is readable; zero *.jsonl rollout files found"
		return src
	}

	total := 0
	unreadable := 0
	for _, path := range files {
		fa, err := rollout.AnalyzeFile(path)
		if err != nil {
			unreadable++
			continue
		}
		total += fa.OperatorTurns
	}
	src.ObservedAt = time.Now()
	if total == 0 && unreadable == len(files) {
		src.Health = HealthUnreadable
		src.Detail = "root exists; every rollout file failed to parse"
		return src
	}
	if total == 0 {
		src.Health = HealthEmpty
		src.Detail = "root exists and is readable; zero genuine operator turns found across files"
		return src
	}
	src.Health = HealthPopulated
	src.UnitCount = total
	src.Detail = "counted genuine operator turns (role=user, content[0].type=input_text) across " +
		strconv.Itoa(len(files)) + " rollout files"
	if unreadable > 0 {
		src.Detail += "; " + strconv.Itoa(unreadable) + " file(s) failed to parse and were excluded from the count"
	}
	return src
}

// BuildClaudeSource inspects one Claude Code transcript root and returns its
// Source record. root is a parameter for the same reason BuildCodexSource's
// is.
//
// Unit = one session transcript file. Observed on the live tree: a project
// subdirectory holds one *.jsonl file per session, named `<sessionId>.jsonl`
// -- e.g. ~/.claude/projects/-private-tmp/0aca25a9-...-e0e.jsonl -- and the
// file's own records repeat that same id in a "sessionId" field on nearly
// every line. Counting files rather than parsing each one's contents keeps
// this a directory listing, not a read of the operator's actual words, and
// needs no os.Open at all: only os.Stat and directory reads, so there is
// nothing here to leave an mtime changed.
func BuildClaudeSource(root string) Source {
	src := Source{
		Name:              "claude-transcripts",
		Harness:           "claude",
		RootPath:          root,
		IdentityFields:    []string{"sessionId (also the file's own name, <sessionId>.jsonl)"},
		Authority:         "First-party: Claude Code's own session transcripts of the operator's live sessions, not a secondary or republished source.",
		Scope:             "This machine's own ~/.claude/projects tree; one operator's own working sessions only, not a shared or multi-user corpus.",
		SensitivityAccess: "Local only; contains raw operator prompts and harness output. Never publish, paste into anything public, or commit to git.",
		Freshness:         "Live and append-only; freshness is ObservedAt itself, re-measured each time this source is rebuilt.",
		Owner:             "Jon (operator), on this machine.",
		RebuildPath:       "Cannot be rebuilt or reacquired from elsewhere -- it is observed directly wherever the harness itself writes, not fetched from a canonical location.",
	}

	if root == "" {
		src.Health = HealthMissing
		src.Detail = "could not resolve a home directory to build the default root"
		return src
	}

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			src.Health = HealthMissing
			src.Detail = "root path does not exist: " + root
			return src
		}
		src.Health = HealthUnreadable
		src.Detail = "os.Stat failed: " + err.Error()
		return src
	}
	if !info.IsDir() {
		src.Health = HealthUnreadable
		src.Detail = "root path exists but is not a directory: " + root
		return src
	}

	projects, err := os.ReadDir(root)
	if err != nil {
		src.Health = HealthUnreadable
		src.Detail = "could not list root: " + err.Error()
		return src
	}

	count := 0
	for _, proj := range projects {
		if !proj.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, proj.Name()))
		if err != nil {
			// One unreadable project subdirectory does not make the whole
			// source Unreadable -- report what could be read, same as
			// BuildCodexSource excludes individual unparseable files rather
			// than failing the whole source.
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".jsonl" {
				count++
			}
		}
	}

	src.ObservedAt = time.Now()
	if count == 0 {
		src.Health = HealthEmpty
		src.Detail = "root exists and is readable; zero *.jsonl session files found under any project subdirectory"
		return src
	}
	src.Health = HealthPopulated
	src.UnitCount = count
	src.Detail = "counted *.jsonl session transcript files across " + strconv.Itoa(len(projects)) + " project subdirectories"
	return src
}
