package reposcan

// This file adds a second, independent guard to the reposcan package:
// agent-estate#768 item 4, "the stop-the-bleeding guard" for the director's
// naming decision (#767's own doc comment / agent-estate#768's issue body).
// The decision was B: fix the wire format now (#769 shipped that half),
// stop new naming debt landing, and let the ~100 pre-rename `agent-supervisor`
// references and ~285 `agent-tui` references age out via their own deferred
// sweeps (agent-estate#768 items 1-2) rather than blanket-`sed`ing them now
// (#729 already established that discipline: some references are true
// statements about the past). This guard enforces only the "stop new debt"
// half; it never touches an existing reference.
//
// Two forbidden shapes, exactly the two agent-estate#768 item 4 names --
// deliberately not a wider "hill90" or "agent-supervisor" ban, see below:
//
//   - hill90-supervisor / hill90-codex-supervisor: this ESTATE naming its
//     OWN state directories after the tenant it operates (agent-estate#768
//     item 2). Exact-name denylist, not a wildcard on "hill90-*": the tenant's
//     own repos and env var -- hill90-app, hill90-docs, hill90-ui, hill90-web,
//     HILL90_APP_REPO -- are correct and must keep matching cleanly. This
//     guard cannot tell "estate names itself after tenant" apart from
//     "estate correctly names the tenant it operates" for any *hypothetical*
//     future hill90-<something> identifier -- only these two, already named
//     by the issue, are covered. Widening this to a wildcard would ban
//     correct code; see the package doc comment on cross_repo_references.go
//     for the same "narrow but honest" posture applied to bare '#N' scope.
//   - agent-supervisor, unqualified: the dead pre-rename repo slug. A
//     mention explicitly qualified with an owning path segment
//     (`jonhill90/agent-supervisor`, or any `.../agent-supervisor`) is
//     treated as a deliberate, specific pointer -- same convention the
//     existing bare-issue-reference guard already uses for `owner/repo#N`
//     (see bareRefRE's own comment: a '#N' preceded by '/' is already
//     qualified and out of scope). Only the bare word is debt.
//
// Escape hatch: a genuinely new historical citation (a doc correctly
// describing what this estate was called, or what it operated, before the
// rename) is exempted by putting NamingDebtEscapeHatch anywhere on the same
// line -- never by editing the baseline to raise a file's count, which
// would silently grandfather a real new violation instead of a citation.
//
// Ratchet, not an allowlist: unlike the numbered bare-issue-reference guard,
// there is no natural key to allowlist a specific line by (a citation isn't
// numbered), and there are already hundreds of existing references --
// requiring every one to carry an inline marker before this guard could ship
// would be exactly the sweep agent-estate#768 defers. Instead,
// testdata/naming_debt_baseline.json pins the CURRENT per-file count of
// forbidden lines; the guard fails a file only if its live count exceeds
// what the baseline recorded. A brand-new file has an implicit baseline of
// 0, so any forbidden reference in it fails immediately. Known failure mode
// of a count-based ratchet (stated here per agent-estate#768's own
// instruction to say so): removing one existing reference and adding a
// different new one to the same file, without exceeding that file's prior
// count, is not caught. A per-line content hash would close that gap at the
// cost of breaking on any unrelated edit to a flagged line; count-based was
// chosen as the cheaper, more maintainable of the two -- same tradeoff the
// brief names for "a pinned count that must not increase."

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// NamingDebtEscapeHatch is the literal marker recognized anywhere on a line
// to permanently exempt that line from the naming-debt guard, for a
// genuinely new historical citation. See the package-level doc comment
// above for why this exists instead of a numbered allowlist entry.
const NamingDebtEscapeHatch = "naming-guard:historical"

var (
	// tenantEstateIdentityRE: agent-estate#768 item 2's exact two offenders.
	// Case-insensitive because a heading or shell prompt may render either
	// name in a different case.
	tenantEstateIdentityRE = regexp.MustCompile(`(?i)\bhill90-(?:codex-)?supervisor\b`)
	// deadRepoSlugRE: the bare word only; qualification is checked
	// separately (see lineHasNamingDebt) because Go's RE2 engine has no
	// lookbehind.
	deadRepoSlugRE = regexp.MustCompile(`\bagent-supervisor\b`)
)

// lineHasNamingDebt reports whether line contains a forbidden reference not
// already qualified by a preceding '/' (jonhill90/agent-supervisor, or any
// other .../agent-supervisor path -- see the package doc comment above for
// why that's treated as a deliberate, specific pointer rather than debt).
func lineHasNamingDebt(line string) bool {
	if tenantEstateIdentityRE.MatchString(line) {
		return true
	}
	for _, loc := range deadRepoSlugRE.FindAllStringIndex(line, -1) {
		start := loc[0]
		if start > 0 && line[start-1] == '/' {
			continue
		}
		return true
	}
	return false
}

// NamingDebtViolation is one line in one file matching a forbidden
// tenant-name/dead-repo-slug pattern.
type NamingDebtViolation struct {
	Path string
	Line int
	Text string
}

func (v NamingDebtViolation) String() string {
	return fmt.Sprintf("%s:%d: %s", v.Path, v.Line, strings.TrimSpace(v.Text))
}

// FindNamingDebtLines returns every line in text matching a forbidden
// pattern, skipping lines carrying NamingDebtEscapeHatch. Exposed at this
// granularity (rather than only a whole-repository scan) so it can be
// mutation-checked directly against synthetic content, same split as
// FindBareReferenceViolations in cross_repo_references' sibling file.
func FindNamingDebtLines(path, text string) []NamingDebtViolation {
	var out []NamingDebtViolation
	stripped := stripCodeSpans(text)
	for lineno, line := range strings.Split(stripped, "\n") {
		if strings.Contains(line, NamingDebtEscapeHatch) {
			continue
		}
		if lineHasNamingDebt(line) {
			out = append(out, NamingDebtViolation{Path: path, Line: lineno + 1, Text: line})
		}
	}
	return out
}

// NamingDebtCounts scans every tracked *.md file, plus the comment text of
// every tracked *.go file, under root and returns the current
// forbidden-pattern line count per file. Same scan scope as ScanRepository
// (never Go string literals, shell/Python source, or .tape files -- see
// that function's own doc comment for why); a file with zero forbidden
// lines is omitted, not recorded as 0, so the baseline file only lists
// files that actually carry debt.
func NamingDebtCounts(root string) (map[string]int, error) {
	counts := map[string]int{}

	mdFiles, err := trackedMarkdownFiles(root)
	if err != nil {
		return nil, err
	}
	for _, rel := range mdFiles {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		if n := len(FindNamingDebtLines(rel, string(data))); n > 0 {
			counts[rel] = n
		}
	}

	goFiles, err := trackedGoFiles(root)
	if err != nil {
		return nil, err
	}
	for _, rel := range goFiles {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		comments, err := extractGoComments(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		comments = stripCodeSpans(comments)
		if n := len(FindNamingDebtLines(rel, comments)); n > 0 {
			counts[rel] = n
		}
	}

	return counts, nil
}

type namingDebtBaselineFile struct {
	Doc    string         `json:"_doc"`
	Counts map[string]int `json:"counts"`
}

// LoadNamingDebtBaseline reads the pinned per-file count manifest. A path
// absent from the file (including because the file has no "counts" key at
// all) has an implicit baseline of 0.
func LoadNamingDebtBaseline(path string) (map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f namingDebtBaselineFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Counts == nil {
		return map[string]int{}, nil
	}
	return f.Counts, nil
}

// NamingDebtRatchetViolation is one file whose current forbidden-pattern
// count exceeds its pinned baseline -- i.e. new debt landed in it.
type NamingDebtRatchetViolation struct {
	Path     string
	Current  int
	Baseline int
}

func (v NamingDebtRatchetViolation) String() string {
	return fmt.Sprintf("%s: %d reference(s) now, baseline allows %d", v.Path, v.Current, v.Baseline)
}

// CheckNamingDebtRatchet compares current per-file forbidden-reference
// counts against the pinned baseline at baselinePath and reports every file
// that grew. A file not present in the baseline (including a brand-new
// file) has an implicit baseline of 0.
func CheckNamingDebtRatchet(root, baselinePath string) ([]NamingDebtRatchetViolation, error) {
	current, err := NamingDebtCounts(root)
	if err != nil {
		return nil, err
	}
	baseline, err := LoadNamingDebtBaseline(baselinePath)
	if err != nil {
		return nil, err
	}

	var out []NamingDebtRatchetViolation
	for path, n := range current {
		if n > baseline[path] {
			out = append(out, NamingDebtRatchetViolation{Path: path, Current: n, Baseline: baseline[path]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
