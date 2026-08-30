// Package knowledge renders Jon's real memory vault -- he asked for this
// directly: "I want to see my memories and knowledge from inside the
// TUI." Source of truth is $AGENT_MEMORY_VAULT
// (agent/index.md + agent/facts/<slug>.md, an OKF-conformant bundle --
// see agent/facts/memory-conventions.md's own doc for the schema this
// package reads).
//
// PROGRESSIVE DISCLOSURE is a hard constraint here, not a nicety: this
// package must never read all of agent/facts/ to draw a list. Load
// (index.go) reads exactly one file, agent/index.md -- the vault's own
// "capped index, one line per fact" -- for the list view. A fact's own
// file (fact.go's LoadFact) is read only when that ONE fact is opened.
// This mirrors the vault's own design intent (memory-conventions.md:
// "agent/index.md is the only file loaded at session start") rather
// than inventing a new access pattern for this one view.
//
// One real consequence of that constraint: agent/index.md's own bullet
// format carries slug, a title-or-slug, and a description -- NOT type or
// created. Those two are only inside each fact file's own frontmatter,
// so they are unknown for a row until that row has actually been
// opened at least once this session (Model's own cache, model.go) --
// absence as a typed value (AGENTS.md), the same shape
// internal/agents.Row.Model/Cost and internal/skills.Skill.LastEval/
// InvocationCount already use for a column with no cheap source.
package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// IndexEntry is one line of agent/index.md -- everything the list view
// can show without opening the fact's own file.
type IndexEntry struct {
	Slug        string
	Title       string
	Description string
}

// reLinked matches "- [Title](facts/slug.md) — description", the index's
// own format for a fact that also has a distinct display title.
var reLinked = regexp.MustCompile(`^-\s+\[([^\]]+)\]\(facts/([^)]+)\.md\)\s+—\s+(.*)$`)

// reWiki matches "- [[slug]] — description", the index's own format for
// a fact referenced by its slug alone (an Obsidian wiki-link) -- Title
// is the slug itself here; the index never invented a nicer title for
// these, and neither does this package.
var reWiki = regexp.MustCompile(`^-\s+\[\[([^\]]+)\]\]\s+—\s+(.*)$`)

// ParseIndex reads agent/index.md's own bullet-list body -- everything
// under its "# Facts" heading -- into one IndexEntry per line. A line
// matching neither known format is skipped, not an error: this file is
// hand-written prose with a YAML frontmatter fence above the list
// (okf_version), and a heading line or a blank line between bullets is
// normal, not a parse failure.
func ParseIndex(data string) []IndexEntry {
	var out []IndexEntry
	sc := bufio.NewScanner(strings.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := reLinked.FindStringSubmatch(line); m != nil {
			out = append(out, IndexEntry{Slug: m[2], Title: m[1], Description: m[3]})
			continue
		}
		if m := reWiki.FindStringSubmatch(line); m != nil {
			out = append(out, IndexEntry{Slug: m[1], Title: m[1], Description: m[2]})
			continue
		}
	}
	return out
}

// LoadIndex reads vaultDir's own agent/index.md and parses it. vaultDir
// == "" is $AGENT_MEMORY_VAULT unset -- a distinct, visible error, never
// an empty (and therefore indistinguishable from "no facts yet") list;
// see this package's own top comment and Fetcher's doc comment for why
// that distinction is a hard requirement here, not a nicety.
func LoadIndex(vaultDir string) ([]IndexEntry, error) {
	if vaultDir == "" {
		return nil, fmt.Errorf("$AGENT_MEMORY_VAULT is not set")
	}
	path := filepath.Join(vaultDir, "agent", "index.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return ParseIndex(string(data)), nil
}
