// Package vhscheck is the durable guard agent-tui#132 asked for: a
// mechanical check that a VHS tape's own `go build ... ./cmd/<name>`
// reference names a directory that actually exists.
//
// Why this exists: three tapes (agents-mode.tape, agent-tui#130;
// knowledge.tape and knowledge-route.tape, agent-tui#132) each referenced
// a cmd/ path that had either never existed or been quietly removed, and
// nothing caught any of the three until a human swept every tape by hand.
// `go build` failing inside a tape produces no screenshot and no error
// anyone reads -- the visual QA the tape represents silently stops
// happening, indistinguishable from visual QA that ran and passed. This
// package is the check that makes that failure loud instead: it runs as
// an ordinary `go test`, already gated on every PR (.github/workflows/
// ci.yml's own `go test ./...` step), so a broken reference fails CI the
// same day it's introduced rather than waiting for the next by-hand sweep.
//
// Deliberately narrow, matching the issue's own scope: this checks that
// the REFERENCED DIRECTORY EXISTS, nothing about whether the tape itself
// still runs correctly (that is what actually running vhs proves, by
// hand, per tape -- a much larger and flakier surface, see
// agents-mode.tape's own header comment on vhs's own capture-pipeline
// flake). A missing directory is a mechanical fact a `go build` inside
// the tape would hit every single time; that is the class of failure
// worth gating CI on.
package vhscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// cmdRefPattern matches a `go build`-shaped line's own build target,
// `./cmd/<name>` (or `cmd/<name>` with no leading `./`) -- deliberately
// anchored to the same LINE as the literal text "go build", not to any
// occurrence of "cmd/<name>" anywhere in the file. A tape's own header
// comment may legitimately MENTION a sibling tape's build target in
// prose (knowledge-route.tape's own history did exactly this, describing
// knowledge.tape's target) without that being a real reference this
// tape's own execution depends on -- scanning every line for the bare
// substring would have flagged that prose forever, including after the
// referenced file no longer exists to explain it. Requiring "go build"
// on the same line is what a real build/run dependency actually looks
// like in every tape this repo has (`git grep -n 'go build' testdata/vhs`
// confirms one line, one build target, every time, whether the line is a
// live `Type "go build ..."` command or a `#`-prefixed comment
// documenting a prerequisite command the operator runs first, e.g.
// chat-send.tape's own `cmd/fakemcp` -- both are real dependencies and
// both are meant to be caught).
var cmdRefPattern = regexp.MustCompile(`go build[^\n]*?\.?/cmd/([A-Za-z0-9_-]+)`)

// Reference is one `.tape` file's own dependency on a `cmd/<Name>`
// package, as found by ScanTapes.
type Reference struct {
	TapePath string // repo-relative, e.g. "testdata/vhs/knowledge.tape"
	CmdName  string // e.g. "knowledgedemo"
	Line     int    // 1-based line number the reference was found on
}

// CmdDir is the repo-relative directory a Reference's CmdName resolves
// to, e.g. "cmd/knowledgedemo" -- exported so a caller can report it
// without re-deriving the join.
func (r Reference) CmdDir() string {
	return filepath.Join("cmd", r.CmdName)
}

// ScanTapes walks every `.tape` file directly under tapesDir (and its
// subdirectories, e.g. testdata/vhs/variants/) and returns every
// cmd/<name> reference found, in a stable, deterministic order (by tape
// path, then by line number) -- so a test asserting on the returned slice
// never flakes on directory-walk ordering.
func ScanTapes(tapesDir string) ([]Reference, error) {
	var refs []Reference
	err := filepath.WalkDir(tapesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".tape") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(filepath.Dir(tapesDir), path)
		if err != nil {
			relPath = path
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "go build") {
				continue
			}
			m := cmdRefPattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			refs = append(refs, Reference{TapePath: relPath, CmdName: m[1], Line: i + 1})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].TapePath != refs[j].TapePath {
			return refs[i].TapePath < refs[j].TapePath
		}
		return refs[i].Line < refs[j].Line
	})
	return refs, nil
}

// MissingCmdDirs returns every Reference in refs whose CmdDir() does not
// exist under repoRoot, in the same order ScanTapes returned them. An
// empty, non-nil-vs-nil-agnostic result means every reference resolved --
// callers should check len(...) == 0, not nil-ness.
func MissingCmdDirs(repoRoot string, refs []Reference) []Reference {
	var missing []Reference
	for _, ref := range refs {
		info, err := os.Stat(filepath.Join(repoRoot, ref.CmdDir()))
		if err != nil || !info.IsDir() {
			missing = append(missing, ref)
		}
	}
	return missing
}
