package knowledge

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// repoDocsSource reads this repository's own written rules -- AGENTS.md
// (CLAUDE.md is the same file, a symlink; see the caller in generate.go,
// which resolves repoRoot once and only ever reads AGENTS.md's path, so
// nothing here reads the symlink a second time) and every *.md file under
// docs/, recursively. agent-estate#1034: AGENTS.md states hard rules no
// other source here carries -- "Capture exit codes directly. `cmd | tail`
// gives you `tail`'s status" is the one that motivated this source -- and
// before this package existed, `estate knowledge query` had no way to
// surface it.
//
// INDEXED BY SECTION, NOT BY FILE. agent-estate#1043 measured that
// dumping a whole document into Tier2 dilutes the field-weighted ranking
// Tier1 depends on: a 900-line file as one Item is one unanswerable
// question, forty sections are forty answerable ones. Markdown has real
// structure -- headings -- and a heading is a Tier1-shaped thing in a way
// a fact's own body never was. Every Markdown heading from level 2 up to
// repoDocsMaxHeadingLevel starts a new Item; a section's own heading path
// (its file's H1 title, then every enclosing heading down to its own) is
// Tier1, and the prose under it -- including any deeper subheadings,
// folded in as plain text rather than split further -- is Tier2.
//
// repoDocsMaxHeadingLevel=3 (## and ### both split) was chosen by
// measuring both options against the golden set -- see this repo's
// #1034 PR body for the two retrieval scores. ##-only collapses
// AGENTS.md's ~15 ### sections into 3 oversized ## items, repeating
// #1043's own file-granularity finding one level down; ##+### is the one
// that actually produces "forty answerable questions" rather than three.
const repoDocsMaxHeadingLevel = 3

// repoDocsSource reads repoRoot/AGENTS.md and every repoRoot/docs/**/*.md
// file. A repoRoot that cannot be resolved at all (see findRepoRoot in
// write.go) is one failed source, matching every other reader in this
// package's honest-absence discipline; a single file this package cannot
// read or parse is skipped, never failing the whole source.
func repoDocsSource(repoRoot string) (SourceResult, []Item) {
	res := SourceResult{Name: "repo-docs"}
	if repoRoot == "" {
		res.Reason = "repository root could not be resolved -- no AGENTS.md found above the working directory, and $ESTATE_REPO_ROOT is not set"
		return res, nil
	}

	var relPaths []string
	if _, err := os.Stat(filepath.Join(repoRoot, "AGENTS.md")); err == nil {
		relPaths = append(relPaths, "AGENTS.md")
	}

	docsDir := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			if rel, relErr := filepath.Rel(repoRoot, path); relErr == nil {
				relPaths = append(relPaths, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(relPaths)

	if len(relPaths) == 0 {
		res.Reason = "no AGENTS.md or docs/*.md found under " + repoRoot
		return res, nil
	}

	var items []Item
	for _, rel := range relPaths {
		full := filepath.Join(repoRoot, filepath.FromSlash(rel))
		data, err := os.ReadFile(full)
		if err != nil {
			continue // one unreadable doc does not fail the source
		}
		for _, sec := range splitMarkdownSections(string(data), repoDocsMaxHeadingLevel) {
			body := strings.TrimSpace(sec.Body)
			if len(sec.HeadingPath) == 0 || body == "" {
				continue // a section with a heading but no prose has nothing to answer with
			}
			tier1 := strings.Join(sec.HeadingPath, " — ")
			// agent-estate#1113: Tier1 (above, displayed verbatim) stays
			// the full ancestor path -- that is what makes a repo-docs
			// citation legible. tier1Leaf/tier1Ancestors split the SAME
			// text for scoring only, via Item.Tier1Scored/
			// Tier1AncestorScored -- see knowledge.go's doc comments on
			// those fields for why the split exists.
			tier1Leaf := sec.HeadingPath[len(sec.HeadingPath)-1]
			tier1Ancestors := strings.Join(sec.HeadingPath[:len(sec.HeadingPath)-1], " — ")
			// agent-estate#1072: the permalink is repo-relative (rel, not
			// full) so itemID -- a pure function of permalink -- produces
			// the same id for the same section regardless of which
			// checkout's absolute path generated the index. Every
			// dispatched turn runs in its own worktree (internal/isolate),
			// so an absolute-path permalink minted a different id per
			// worktree for identical content; a lane citing an id in a PR
			// body was uncitable from a reviewer's own worktree. The
			// tradeoff named in the issue: a repo-relative locator is not
			// independently openable the way full was -- see main.go's
			// printRepoDocsRoot, which resolves and prints this checkout's
			// own repo root once alongside any repo-docs match/item, so a
			// reader still knows what to join the permalink to.
			permalink := rel + "#" + headingAnchor(sec.HeadingPath)
			publishable, basis := classify("repo-docs")
			items = append(items, Item{
				ID:                  itemID(permalink),
				Source:              "repo-docs",
				Permalink:           permalink,
				StructuralTags:      []string{"repo-docs", rel},
				Tier1:               truncate(tier1, 200),
				Tier1Scored:         truncate(tier1Leaf, 200),
				Tier1AncestorScored: truncate(tier1Ancestors, 200),
				Tier2:               truncate(body, 800),
				Tier3:               "open " + rel + " for the full section (repo-relative -- see the repo root your own checkout resolves)",
				Publishable:         publishable,
				PublishBasis:        basis,
			})
		}
	}

	res.OK = true
	res.Count = len(items)
	return res, items
}

// docSection is one heading-delimited chunk of a Markdown file --
// HeadingPath is every enclosing heading's own text, from the file's H1
// title down to the section's own heading; Body is the section's prose,
// with any deeper (unsplit) subheadings folded in as plain lines.
type docSection struct {
	HeadingPath []string
	Body        string
}

// splitMarkdownSections walks data line by line and returns one
// docSection per heading whose level is in [2, maxLevel]. Level-1 (`# `)
// headings never start a section of their own -- a file's H1 is title
// context, carried into every section's HeadingPath, not a section --
// and a heading deeper than maxLevel is folded into its enclosing
// section's Body as a plain line rather than splitting further, per this
// file's own doc comment on why maxLevel was measured rather than
// guessed.
func splitMarkdownSections(data string, maxLevel int) []docSection {
	type stackEntry struct {
		level int
		text  string
	}
	var stack []stackEntry
	var sections []docSection
	var bodyLines []string
	open := false

	flush := func() {
		if !open {
			return
		}
		path := make([]string, len(stack))
		for i, e := range stack {
			path[i] = e.text
		}
		sections = append(sections, docSection{
			HeadingPath: path,
			Body:        strings.TrimSpace(strings.Join(bodyLines, "\n")),
		})
		bodyLines = nil
	}

	for _, line := range strings.Split(data, "\n") {
		level, text, ok := parseHeadingLine(line)
		if !ok {
			if open {
				bodyLines = append(bodyLines, line)
			}
			continue
		}
		if level > maxLevel && level != 1 {
			// Deeper than this run splits at -- content, not a new item.
			if open {
				bodyLines = append(bodyLines, line)
			}
			continue
		}
		// A new level-1 or in-range heading closes whatever section was
		// open (it does not close for level 1 mid-document -- there is
		// none in this repo's docs today, but closing is still correct:
		// a second H1 would otherwise wrongly fold under the first).
		flush()
		for len(stack) > 0 && stack[len(stack)-1].level >= level {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, stackEntry{level: level, text: text})
		open = level >= 2
	}
	flush()
	return sections
}

// parseHeadingLine reports the level and text of an ATX Markdown heading
// ("## Title" -> 2, "Title"), or ok=false for any other line. No support
// for Setext (underlined) headings -- none of this repo's docs use them
// (agent-estate#1034: checked `grep -n '^==\+$\|^--\+$'` across AGENTS.md
// and docs/*.md before writing this, found none).
func parseHeadingLine(line string) (level int, text string, ok bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i == 0 || i > 6 || i >= len(line) || line[i] != ' ' {
		return 0, "", false
	}
	text = strings.TrimSpace(line[i+1:])
	text = strings.TrimRight(text, "#")
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, "", false
	}
	return i, text, true
}

// headingAnchor turns a section's own heading path into a permalink
// fragment -- GitHub's own anchor scheme applied to the section's own
// (last) heading, disambiguated by every ancestor heading's own slug
// joined with "--" so two identically-named sections in the same file
// (e.g. AGENTS.md's two "Conventions" subsections) never collide.
func headingAnchor(path []string) string {
	slugs := make([]string, len(path))
	for i, h := range path {
		slugs[i] = slugify(h)
	}
	return strings.Join(slugs, "--")
}

// slugify is GitHub's own heading-anchor scheme: lowercase, spaces to
// hyphens, everything but [a-z0-9-] dropped -- mechanical, not this
// package composing a name.
func slugify(s string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case r == ' ' || r == '-' || r == '_':
			if !lastHyphen && b.Len() > 0 {
				b.WriteRune('-')
				lastHyphen = true
			}
		default:
			// dropped -- punctuation, em dashes, backticks etc. carry no
			// anchor-identity of their own
		}
	}
	return strings.TrimRight(b.String(), "-")
}
