// Package knowledge renders Jon's real memory vault -- he asked for this
// directly: "I want to see my memories and knowledge from inside the
// TUI." Source of truth is $AGENT_MEMORY_VAULT (index.md at the vault
// root + 01 - Notes/<earned subdir>/<id>.md, an OKF-conformant bundle --
// see 01 - Notes/01f - Facts/20260712173000.md, "memory-conventions", for
// the schema this package reads). This used to be agent/index.md +
// agent/facts/<slug>.md; A2-COMPLETION (agent-estate#1275) dissolved
// agent/ in full and moved the capped-index carrier to the vault root
// (99 - Meta/index-contract.md is the governing contract now).
// agent-estate#1304: both readers here still pointed at the deleted
// directory, unguarded, for a month after that move.
//
// PROGRESSIVE DISCLOSURE is a hard constraint here, not a nicety: this
// package must never read every fact's own file to draw a list. Load
// (index.go) reads exactly one file, index.md -- the vault's own capped,
// "one line per fact that has earned index space" carrier
// (99 - Meta/index-contract.md's own cap: 160 entries, 119 today) -- for
// the list view. A fact's own file (fact.go's LoadFact) is read only when
// that ONE fact is opened.
//
// One real consequence of that constraint: index.md's own bullet format
// carries an id, a title-or-id, and a description -- NOT type or created.
// Those two are only inside each fact file's own frontmatter, so they are
// unknown for a row until that row has actually been opened at least once
// this session (Model's own cache, model.go) -- absence as a typed value
// (AGENTS.md), the same shape internal/agents.Row.Model/Cost and
// internal/skills.Skill.LastEval/InvocationCount already use for a column
// with no cheap source.
package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// IndexEntry is one line of index.md -- everything the list view can show
// without opening the fact's own file. Slug holds the note's own 14-digit
// id (or 12, for an older note) -- index.md's two link forms both name
// one, and LoadFact below resolves either form by id or by a frontmatter
// aliases: entry, so "Slug" here means "the identifier LoadFact accepts,"
// not literally a pre-relayout slug string.
type IndexEntry struct {
	Slug        string
	Title       string
	Description string
}

// noteFilename is the same digit-shape every reader across the estate
// checks a note filename against (src/estate/internal/candidates/moc.go's
// noteFilename, src/estate/internal/knowledge/vault.go's vaultSource) --
// 12 digits (the original id form) or 14 (YYYYMMDDHHMMSS, the current
// one; measured 2026-09-08: 0 twelve-digit, 3569 fourteen-digit real note
// filenames in the live vault). Reproduced here rather than imported --
// src/tui is its own Go module, separate from src/estate.
var noteFilename = regexp.MustCompile(`^(\d{12}|\d{14})\.md$`)

// reLinked matches "- [Title](<path ending in>/<id>.md) — description",
// index.md's own format for a bullet that also carries a distinct display
// title. The path itself is not otherwise interpreted -- index-contract.md
// names one required form, "[title](01 - Notes/<subdir>/<id>.md)", but a
// bullet's path is URL-encoded in practice ("01%20-%20Notes/01f%20-%20Facts/
// <id>.md", measured against the live index.md) and the id is what LoadFact
// needs, not the directory it currently sits under -- so this captures the
// trailing "<id>.md" component generically rather than hardcoding one
// subdirectory or one encoding, and ParseIndex rejects anything whose
// trailing component is not noteFilename-shaped.
var reLinked = regexp.MustCompile(`^-\s+\[([^\]]+)\]\([^)]*/([^/)]+\.md)\)\s+—\s+(.*)$`)

// reWiki matches "- [[id]] — description", index.md's other valid form
// (index-contract.md: "[[slug]] -- an Obsidian wikilink ... resolved ...
// by filename regardless of subdirectory") -- Title is the id itself
// here, matching what a reader actually sees: index.md never invents a
// nicer title for these, and neither does this package.
var reWiki = regexp.MustCompile(`^-\s+\[\[([^\]]+)\]\]\s+—\s+(.*)$`)

// ParseIndex reads index.md's own bullet-list body into one IndexEntry
// per line. A line matching neither known format, or whose extracted id
// is not noteFilename-shaped, is skipped, not an error: this file is
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
			if !noteFilename.MatchString(m[2]) {
				continue
			}
			id := strings.TrimSuffix(m[2], ".md")
			out = append(out, IndexEntry{Slug: id, Title: m[1], Description: m[3]})
			continue
		}
		if m := reWiki.FindStringSubmatch(line); m != nil {
			if !noteFilename.MatchString(m[1] + ".md") {
				continue
			}
			out = append(out, IndexEntry{Slug: m[1], Title: m[1], Description: m[2]})
			continue
		}
	}
	return out
}

// LoadIndex reads vaultDir's own index.md (the vault root's capped
// carrier, agent-estate#1304 -- not agent/index.md, which agent-estate#1275 removed a
// month before this was fixed) and parses it. vaultDir == "" is
// $AGENT_MEMORY_VAULT unset -- a distinct, visible error, never an empty
// (and therefore indistinguishable from "no facts yet") list; see this
// package's own top comment and Fetcher's doc comment for why that
// distinction is a hard requirement here, not a nicety.
func LoadIndex(vaultDir string) ([]IndexEntry, error) {
	if vaultDir == "" {
		return nil, fmt.Errorf("$AGENT_MEMORY_VAULT is not set")
	}
	path := filepath.Join(vaultDir, "index.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return ParseIndex(string(data)), nil
}

// CountFacts reports how many fact files 01 - Notes/01f - Facts/ actually
// holds -- the real, uncapped count, distinct from len(LoadIndex(...)).
// agent-estate#1304: index.md is a DELIBERATELY capped, prunable subset
// of what has earned index space (99 - Meta/index-contract.md: "does not
// require every fact... to be indexed" -- 119 entries against 352 real
// fact files, measured 2026-09-08), so the index's own length answers
// "how many facts are currently loaded at session start," not "how many
// facts does the vault hold." A dashboard stat named VaultFacts means the
// second question -- counting the capped index instead would silently
// understate by roughly two thirds, the same class of instrument-answers-
// an-adjacent-question defect this fix exists to close elsewhere.
//
// Counts filenames only (os.ReadDir, one syscall) -- it never opens a
// fact's own body, so it does not reintroduce the cost this package's
// list view exists to avoid; "must never read all of agent/facts/ to draw
// a list" (this file's own top comment) is about reading CONTENT to
// render rows, not about counting how many files exist.
func CountFacts(vaultDir string) (int, error) {
	if vaultDir == "" {
		return 0, fmt.Errorf("$AGENT_MEMORY_VAULT is not set")
	}
	dir := filepath.Join(vaultDir, "01 - Notes", "01f - Facts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", dir, err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && noteFilename.MatchString(e.Name()) {
			n++
		}
	}
	return n, nil
}
