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
	// Body is everything after the closing frontmatter fence, trimmed --
	// the fact's own full text (agent-estate#1027). Compiled into
	// Item.Tier2 so it enters Query's searchable text (searchableText in
	// query.go already reads Tier1+Tier2); this package changes what
	// goes into that existing field, not query.go's own logic.
	Body string
}

// vaultSource reads every agent/facts/*.md file's own frontmatter under
// vaultDir. A vault that cannot be listed at all (unset, missing,
// unreadable) is one failed source, not a silently empty Items slice; a
// single fact file that fails to parse is skipped and does not fail the
// whole source, since agent/index.md itself already tolerates unparsed
// bullet lines (src/tui's ParseIndex).
func vaultSource(vaultDir string) (SourceResult, []Item) {
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
		// tier2 carries the fact's own full body, not just its
		// description (agent-estate#1027) -- description is kept as a
		// lead-in when present so Tier2 alone still reads sensibly, but
		// the body is what closes the ~14% indexed-fraction gap #1027
		// measured: query.go's searchableText already reads Tier1+Tier2,
		// so this is the whole fix on this package's side of the
		// boundary -- no change to query.go's own logic.
		tier2 := f.Body
		if f.Description != "" {
			if tier2 != "" {
				tier2 = f.Description + "\n\n" + tier2
			} else {
				tier2 = f.Description
			}
		}
		publishable, basis := classify("vault-fact")
		items = append(items, Item{
			ID:             itemID(path),
			Source:         "vault-fact",
			Permalink:      path,
			StructuralTags: structural,
			Tier1:          truncate(tier1, 200),
			Tier2:          tier2,
			Tier3:          vaultTier3(path, string(data)),
			Publishable:    publishable,
			PublishBasis:   basis,
		})
	}

	res.OK = true
	res.Count = len(items)
	return res, items
}

// vaultTier3 is the third disclosure rung for a vault fact -- agent-
// estate#1139 defect B: the pointer this used to return ("open <path> for
// the full fact") was a pointer, not a deeper level of disclosure, and
// Tier2 already carries the fact's own full body (agent-estate#1027), so a
// reader who followed the ladder from Tier2 to Tier3 got LESS material,
// not more. raw is the fact file's own bytes, read once by the caller
// (vaultSource) and passed in here rather than re-read, so this can never
// diverge from what Tier1/Tier2 were actually built from. Tier3 is the
// entire file verbatim -- frontmatter fence and all -- which is strictly a
// superset of Tier2 (body only): a reader gets the fact's own structural
// fields (type/title/description/created) alongside the body, not just the
// body again. Nothing here is authoritative over the file itself (see this
// package's own "never authoritative" doc comment) -- path is still named,
// for a reader who wants to open and edit the real file rather than trust
// this copy.
func vaultTier3(path, raw string) string {
	body := strings.TrimSpace(raw)
	if body == "" {
		return "(fact file at " + path + " is empty)"
	}
	return body + "\n\n(full fact file: " + path + ")"
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

	// Everything after the closing fence is the fact's own body --
	// captured verbatim (agent-estate#1027), never reworded or
	// summarised by this package.
	var body strings.Builder
	for sc.Scan() {
		body.WriteString(sc.Text())
		body.WriteByte('\n')
	}
	f.Body = strings.TrimSpace(body.String())
	return f, nil
}
