package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// vaultFact is one agent/facts/<slug>.md file's own frontmatter --
// src/tui's internal/knowledge package parses the identical schema for
// its interactive viewer; this is a separate, small re-implementation
// rather than a cross-module dependency, because this package's own
// access pattern is deliberately different: a ONE-TIME batch compile
// that opens every fact file once per run, not the viewer's per-session
// progressive-disclosure constraint (never read a fact until a human
// opens it). Both are correct for what each package is; they are not the
// same constraint.
type vaultFact struct {
	Slug        string
	Type        string
	Title       string
	Description string
	Created     string
}

// vaultSource reads every agent/facts/*.md file's own frontmatter under
// vaultDir. A vault that cannot be listed at all (unset, missing,
// unreadable) is one failed source, not a silently empty Items slice; a
// single fact file that fails to parse is skipped and does not fail the
// whole source, since agent/index.md itself already tolerates unparsed
// bullet lines (src/tui's ParseIndex).
func vaultSource(vaultDir string, clock *idClock) (SourceResult, []Item) {
	res := SourceResult{Name: "vault-facts"}
	if vaultDir == "" {
		res.Reason = "$AGENT_MEMORY_VAULT is not set"
		return res, nil
	}
	factsDir := filepath.Join(vaultDir, "agent", "facts")
	entries, err := os.ReadDir(factsDir)
	if err != nil {
		res.Reason = fmt.Sprintf("cannot list %s: %v", factsDir, err)
		return res, nil
	}

	var items []Item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		path := filepath.Join(factsDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue // one unreadable fact does not fail the source
		}
		f, err := parseVaultFact(string(data))
		if err != nil {
			continue // frontmatter this package cannot parse -- skipped, not fabricated
		}
		f.Slug = slug
		title := f.Title
		if title == "" {
			title = slug
		}
		tier1 := title
		if f.Description != "" {
			tier1 = title + " -- " + f.Description
		}
		var structural []string
		if f.Type != "" {
			structural = append(structural, f.Type)
		}
		items = append(items, Item{
			ID:             clock.NextID(),
			Source:         "vault-fact",
			Permalink:      path,
			StructuralTags: structural,
			Tier1:          truncate(tier1, 200),
			Tier2:          f.Description,
			Tier3:          "open " + path + " for the full fact",
		})
	}

	res.OK = true
	res.Count = len(items)
	return res, items
}

// parseVaultFact is a minimal frontmatter scan over the same six-field
// schema src/tui's internal/knowledge.parseFact reads -- no YAML library
// in this module either, same reasoning: a genuinely block-scalar value
// would read here as only its first line, which is a visibly odd value,
// not silently truncated data a reader would trust.
func parseVaultFact(data string) (vaultFact, error) {
	sc := bufio.NewScanner(strings.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return vaultFact{}, fmt.Errorf("does not start with a --- frontmatter fence")
	}

	var f vaultFact
	closed := false
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(strings.Trim(strings.TrimSpace(val), `"`))
		switch key {
		case "type":
			f.Type = val
		case "title":
			f.Title = val
		case "description":
			f.Description = val
		case "created":
			f.Created = val
		}
	}
	if !closed {
		return vaultFact{}, fmt.Errorf("frontmatter fence never closed")
	}
	return f, nil
}
