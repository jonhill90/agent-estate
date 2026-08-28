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
// agent-tui#157 extended the guard to Go comments too, after the
// qualification pass that PR shipped found the same hazard living in
// hundreds of `//` doc comments (internal/board/layout.go,
// internal/shell/model.go, ...), not just Markdown -- a bare '#N' in a Go
// comment resolves exactly as silently after a repo merge as one in a
// README. The scan is comment-text only, never full source: a Go string
// literal can legitimately contain a bare '#N' that is not a citation at
// all (a rendered UI marker like board's own `"#1 "` card-number prefix,
// or a JSON hex colour), and treating those as violations would make the
// guard un-runnable. See extractGoComments's own doc comment for how that
// split is done, and TestBareReferenceGuardIgnoresGoStringLiterals for the
// regression this exists to prevent.
//
// This guard is deliberately narrow, same as its agent-dotfiles source:
//   - It only scans tracked *.md files and the *comment* text of tracked
//     *.go files -- never Go string literals, shell/Python source, or
//     `.tape` files, each a real but out-of-scope extension of its own.
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
	"go/scanner"
	"go/token"
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

func trackedFiles(root, pattern string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", pattern)
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

func trackedMarkdownFiles(root string) ([]string, error) {
	return trackedFiles(root, "*.md")
}

func trackedGoFiles(root string) ([]string, error) {
	return trackedFiles(root, "*.go")
}

// extractGoComments returns src with every byte OUTSIDE a `//` or `/* */`
// comment replaced by a space (newlines preserved), so FindBareReferenceViolations
// can scan it with the exact same line numbers as the original file while
// seeing only comment text -- never a string literal, which can legitimately
// contain a bare '#N' that is not a citation (a rendered UI marker like
// internal/board's own "#1 " card-number prefix, or a hex colour in a JSON
// fixture). go/scanner is used rather than a regex so a '#' or "//" inside a
// string or rune literal is never mistaken for a comment boundary.
func extractGoComments(src []byte) (string, error) {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))

	out := make([]byte, len(src))
	for i, b := range src {
		if b == '\n' {
			out[i] = '\n'
		} else {
			out[i] = ' '
		}
	}

	var s scanner.Scanner
	// ScanComments so comment tokens are emitted at all; errors are
	// swallowed via a no-op handler because a syntactically invalid .go
	// file (mid-edit, generated, etc.) should degrade to "no comments
	// found" for this guard, not fail the whole scan.
	s.Init(file, src, func(pos token.Position, msg string) {}, scanner.ScanComments)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.COMMENT {
			continue
		}
		start := file.Offset(pos)
		end := start + len(lit)
		copy(out[start:end], lit)
	}
	return string(out), nil
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

// ScanRepository scans every tracked *.md file, plus the comment text of
// every tracked *.go file, under root for bare '#N' citations that do not
// resolve in this repository. Go string literals are never scanned -- see
// extractGoComments's own doc comment for why.
func ScanRepository(root, manifestPath, allowlistPath string) ([]Violation, error) {
	known, err := LoadKnownNumbers(manifestPath)
	if err != nil {
		return nil, err
	}
	allowed, err := LoadAllowlist(allowlistPath)
	if err != nil {
		return nil, err
	}

	var all []Violation

	mdFiles, err := trackedMarkdownFiles(root)
	if err != nil {
		return nil, err
	}
	for _, rel := range mdFiles {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		all = append(all, FindBareReferenceViolations(rel, string(data), known, allowed)...)
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
		// Same code-span convention as the Markdown path: a backtick-quoted
		// example inside a doc comment (`` `#202` `` illustrating what a
		// FIXED file used to literally say) is not a live citation.
		comments = stripCodeSpans(comments)
		all = append(all, FindBareReferenceViolations(rel, comments, known, allowed)...)
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Line < all[j].Line
	})
	return all, nil
}
