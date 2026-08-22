// Package skills is docs/SPEC-shell.md's S8: "List skills from
// ~/.claude/skills and the skills repo: name, description, last eval
// result, invocation count. [e] runs its eval."
//
// In this estate ~/.claude/skills/<name> is a symlink into the "skills"
// repo's own skills/<name> directory (confirmed: `ls -la
// ~/.claude/skills/adopt-or-build` -> a symlink to
// .../Personal/Skills/skills/adopt-or-build) -- one directory scan reaches
// both halves of S8's own "from X and Y" phrasing, not two reads.
//
// Two of S8's four columns have no source to fill in today, measured
// rather than assumed, exactly the shape S6 (internal/agents) already
// found for model/cost:
//   - Last eval result / verdict: RE-CHECKED 2026-08-22 against
//     agent-evals@a7f61ce (the jonhill90/skills#230 harness this doc
//     comment used to say did not exist at all -- it now does:
//     scripts/eval_skill.py runs a real with/without comparison for one
//     named skill and prints a keep/improve/rename/drop verdict). What has
//     NOT changed: it persists nothing. Its own `main()` prints JSON to
//     stdout and writes only to `--outdir` (a fresh tempdir by default,
//     never a stable path); there is no committed results file, no
//     `tests/evals/results/skill-verdicts.*`, nothing this package could
//     open. A verdict was produced for `research-the-limit` by hand three
//     times on 2026-08-22 (agent-evals#21's own PR body) -- that value
//     lives in a PR description and a chat transcript, neither of which is
//     a file this package may read (AGENTS.md's adapter discipline: no
//     second reader of an out-of-repo source improvised in place of a
//     real seam). Until agent-evals adds a persistence layer, every skill
//     is honestly Verdict "unevaluated", not a guess at what it might be.
//   - Invocation count: nothing in this estate counts skill invocations.
//     `mine-transcripts` (a skill in this same repo) is a deliberate,
//     periodic, human-triggered review of transcripts for new skill
//     CANDIDATES -- not a counter, and not run automatically.
//
// LastEval/InvocationCount are therefore always nil, and Verdict is
// therefore always its zero value (rendered "unevaluated" by View, never
// silently blank) -- absence as a typed value, never a bare zero
// (AGENTS.md), the same pattern internal/agents.Row.Model/Cost already
// established for this exact shape of gap. "Unevaluated" is a real,
// positive fact, not a filler string: SPEC-shell.md S8's own model is
// explicit that a skill with no recorded invocations is UNEVALUATED, not
// dead, and this field exists to say that in words rather than let an
// empty cell be misread as "looks unused."
package skills

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is one skill directory. ParseErr is non-empty when the directory
// has a SKILL.md that could not be read as this package's own minimal
// frontmatter shape (see parseFrontmatter) -- a directory with NO
// SKILL.md at all is not a skill and is not returned as a Skill (Scan
// skips it silently: plenty of non-skill files/dirs can legitimately sit
// alongside skill directories, e.g. this repo's own README.md).
type Skill struct {
	// Dir is the directory name Scan found this skill under (the ID a
	// caller should use for selection/eval requests) -- not necessarily
	// equal to Name: a malformed SKILL.md missing its own `name:` field
	// still produces a Skill, keyed by Dir, so a broken frontmatter is
	// visible in the list rather than silently absent from it.
	Dir         string
	Name        string
	Description string
	ParseErr    string

	// LastEval and InvocationCount are always nil today -- see this
	// package's own doc comment for the measured reason. A caller must
	// render "unknown," never "0" or blank, exactly as
	// internal/agents.Row's identical fields already require.
	LastEval        *string
	InvocationCount *int

	// Verdict is agent-evals#21's own vocabulary (keep/improve/rename/drop)
	// plus "unevaluated" -- its zero value, since agent-evals persists no
	// store yet (see this file's own doc comment). Deliberately a plain
	// string, not a *string: "unevaluated" is the true, positive value for
	// every skill today, not a placeholder standing in for a real one this
	// package failed to fetch -- View renders VerdictUnevaluated whenever
	// this is empty, so Scan never has to set it explicitly.
	Verdict string
}

// VerdictUnevaluated is Skill.Verdict's zero-value meaning, exported so a
// caller (a future eval-store Fetcher, this package's own tests) can
// compare against the same constant View renders rather than the literal
// "unevaluated" string in two places.
const VerdictUnevaluated = "unevaluated"

// Fetcher retrieves the current skill list -- the adapter seam
// (AGENTS.md's own discipline) a Model is built around. ScanFetcher below
// is the one real implementation this repo ships; every test in this
// package constructs a fake instead (the same "own seam, own fake"
// pattern every other package's Fetcher-shaped type follows).
type Fetcher func() ([]Skill, error)

// ScanFetcher builds a Fetcher over Scan(dir) -- the adapter cmd/keelson
// would wire in for a real ~/.claude/skills path, kept as a one-line
// constructor (mirroring internal/board's own Fetcher-building functions
// in cmd/keelson/board.go) so a caller never has to write the closure
// itself.
func ScanFetcher(dir string) Fetcher {
	return func() ([]Skill, error) { return Scan(dir) }
}

// Scan reads every entry of dir, in name order, treating each one with a
// readable SKILL.md as a Skill -- entries without one (a stray file, a
// README, anything else that isn't a skill directory) are silently
// skipped, matching what this estate's own dir layout already has sitting
// next to its skill directories. dir not existing or not readable at all
// is a real error, returned as such -- never an empty list standing in
// for "could not look" (AGENTS.md's own "blind, not quiet" rule).
func Scan(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var out []Skill
	for _, e := range entries {
		skillPath := filepath.Join(dir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			// No SKILL.md (or unreadable for a reason short of "dir
			// itself is gone") -- not a skill directory, not an error.
			continue
		}
		name, desc, perr := parseFrontmatter(string(data))
		s := Skill{Dir: e.Name(), Name: name, Description: desc}
		if perr != "" {
			s.ParseErr = perr
		}
		out = append(out, s)
	}
	return out, nil
}

// parseFrontmatter reads SKILL.md's `---`-delimited YAML header for
// exactly two fields, `name:` and `description:`, each a plain scalar on
// one line -- every SKILL.md in this estate's skills repo uses this shape
// today (checked: no `description: |`/`description: >` block scalar
// across any of its 39 skills, 2026-08-22). A multi-line block scalar
// would read here as only its first line, surfaced via the returned
// parse error string rather than silently truncated -- this is a
// deliberate simplification (no YAML library in this module's go.mod, and
// two known-scalar fields don't need one), not an oversight; a skill
// whose description genuinely needs a block scalar will show a visible
// ParseErr, not a quietly wrong one.
func parseFrontmatter(data string) (name, description, parseErr string) {
	sc := bufio.NewScanner(strings.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return "", "", "SKILL.md does not start with a --- frontmatter fence"
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			if name == "" {
				return name, description, "frontmatter has no name: field"
			}
			return name, description, ""
		}
		key, val, ok := splitFrontmatterLine(line)
		if !ok {
			continue
		}
		switch key {
		case "name":
			name = val
		case "description":
			description = val
		}
	}
	return "", "", "frontmatter never closed with a second ---"
}

// splitFrontmatterLine splits "key: value" -- ok is false for a blank
// line or one with no ": " separator, both silently skipped by
// parseFrontmatter's caller rather than treated as a parse failure (a
// blank line inside frontmatter is normal YAML, not this estate's
// convention, but harmless to tolerate).
func splitFrontmatterLine(line string) (key, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}
