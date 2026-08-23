// Package reposcan guards against stale cross-repo and intra-repo issue/PR
// references in tracked docs.
//
// Ported from jonhill90/agent-dotfiles' tests/test_cross_repo_references.py
// (TestBareIssueReferencesResolveInThisRepository) after agent-tui#117's
// go-public checklist found two dangling references that would resolve to
// the wrong thing -- or nothing -- once read outside this estate:
// internal/mcpservers/server.go:8 cited `agent-tui#494` (the real target is
// jonhill90/agent-supervisor#494), and AGENTS.md cited bare `#202` twice
// (real target jonhill90/agent-supervisor#202). Both were fixed by
// qualifying as `owner/repo#N`; this guard exists so a future bare '#N'
// citation in a tracked doc can't recur silently.
//
// A bare '#N' always resolves to *something* in whatever repo reads it --
// it fails silently and plausibly instead of 404ing, so a citation correct
// before a repo split (or written by an agent that forgot which repo it was
// in) can point at the wrong issue forever.
//
// This guard is deliberately narrow, same as its agent-dotfiles source:
//   - It only scans tracked *.md files. internal/mcpservers/server.go's own
//     `agent-tui#494` was a Go comment, not a Markdown file, and is not
//     re-checked by this guard going forward -- catching stale references
//     in code comments generally is a different, larger scope this port
//     does not attempt.
//   - A '#N' preceded by a word character or '/' (`owner/repo#9`,
//     `agent-tui#9`) is already qualified and out of scope: it names a
//     specific repository, even without a leading owner. This guard
//     validates that a bare reference resolves *somewhere sane*; it does
//     not confirm an already-qualified reference names the *correct*
//     repository -- `agent-tui#494` looked exactly this "qualified" and was
//     still wrong. That class of bug (self-qualified but pointing at the
//     wrong repo) is not detectable offline by number-parsing alone and is
//     out of scope here, same as agent-dotfiles' own guard documents for
//     its equivalent edge case.
package reposcan

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RepoSlug is the GitHub repository this guard validates bare references
// against.
const RepoSlug = "jonhill90/agent-tui"

var (
	fencedCodeRE = regexp.MustCompile(`(?s)` + "```" + `.*?` + "```")
	inlineCodeRE = regexp.MustCompile("`[^`\n]+`")
	// A '#N' preceded by a word character or '/' is already qualified
	// ('owner/repo#9', 'agent-tui#9') and out of scope -- see the package
	// doc comment above.
	bareRefRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9/])#(\d+)`)
)

// Violation is a single bare issue/PR reference that does not resolve in
// this repository.
type Violation struct {
	Path string
	Line int
	Ref  string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: %s", v.Path, v.Line, v.Ref)
}

// stripCodeSpans blanks fenced and inline code spans before scanning, since
// they hold illustrative template syntax (e.g. a doc showing the literal
// Markdown to write, `[title](path)`), not real references. Newline count
// inside fenced blocks is preserved so reported line numbers stay accurate.
func stripCodeSpans(text string) string {
	withoutFences := fencedCodeRE.ReplaceAllStringFunc(text, func(m string) string {
		return strings.Repeat("\n", strings.Count(m, "\n"))
	})
	return inlineCodeRE.ReplaceAllString(withoutFences, "")
}

func trackedMarkdownFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "*.md")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// LoadKnownNumbers reads the manifest at path: one issue/PR number per
// line, plus leading '#'-comment lines.
func LoadKnownNumbers(path string) (map[int]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	known := map[int]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, fmt.Errorf("bad line in %s: %q", path, line)
		}
		known[n] = true
	}
	return known, nil
}

type allowlistFile struct {
	Allowed []struct {
		Number int    `json:"number"`
		Reason string `json:"reason"`
	} `json:"allowed"`
}

// LoadAllowlist reads the escape-hatch manifest: numbers that are
// legitimate historical references no longer resolving in this repository.
func LoadAllowlist(path string) (map[int]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f allowlistFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	allowed := map[int]bool{}
	for _, e := range f.Allowed {
		allowed[e.Number] = true
	}
	return allowed, nil
}

// FindBareReferenceViolations scans already-read file content for bare
// '#N' citations not present in known or allowed. Exposed at this
// granularity (rather than only as a whole-repository scan) so it can be
// mutation-checked directly against synthetic content.
func FindBareReferenceViolations(path, text string, known, allowed map[int]bool) []Violation {
	var out []Violation
	stripped := stripCodeSpans(text)
	for lineno, line := range strings.Split(stripped, "\n") {
		for _, m := range bareRefRE.FindAllStringSubmatchIndex(line, -1) {
			numStr := line[m[2]:m[3]]
			n, err := strconv.Atoi(numStr)
			if err != nil {
				continue
			}
			if !known[n] && !allowed[n] {
				out = append(out, Violation{Path: path, Line: lineno + 1, Ref: "#" + numStr})
			}
		}
	}
	return out
}

// ScanRepository scans every tracked *.md file under root for bare '#N'
// citations that do not resolve in this repository.
func ScanRepository(root, manifestPath, allowlistPath string) ([]Violation, error) {
	known, err := LoadKnownNumbers(manifestPath)
	if err != nil {
		return nil, err
	}
	allowed, err := LoadAllowlist(allowlistPath)
	if err != nil {
		return nil, err
	}
	files, err := trackedMarkdownFiles(root)
	if err != nil {
		return nil, err
	}
	var all []Violation
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		all = append(all, FindBareReferenceViolations(rel, string(data), known, allowed)...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Line < all[j].Line
	})
	return all, nil
}
