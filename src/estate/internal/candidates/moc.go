package candidates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const mocStart = "<!-- generated-links:start -->"
const mocEnd = "<!-- generated-links:end -->"

// MOCProposals emits drafts only; a hub must already link the whole cluster to
// suppress a proposal. Refresh changes only a delimited generated link section.
func MOCProposals(vault string, apply bool) ([]string, error) {
	groups := map[string][]string{}
	notes, e := filepath.Glob(filepath.Join(vault, "01 - Notes", "*.md"))
	if e != nil {
		return nil, e
	}
	for _, p := range notes {
		b, e := os.ReadFile(p)
		if e != nil {
			return nil, e
		}
		if field(string(b), "status") != "stable" {
			continue
		}
		var tags []string
		if json.Unmarshal([]byte(field(string(b), "tags")), &tags) != nil {
			continue
		}
		seen := map[string]bool{}
		for _, tag := range tags {
			if !seen[tag] {
				groups[tag] = append(groups[tag], p)
				seen[tag] = true
			}
		}
	}
	hubs, _ := filepath.Glob(filepath.Join(vault, "02 - MOCs", "*.md"))
	var proposed []string
	changes := map[string][]byte{}
	tags := []string{}
	for tag := range groups {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		paths := groups[tag]
		if len(paths) < 8 {
			continue
		}
		covered := false
		for _, hub := range hubs {
			b, e := os.ReadFile(hub)
			if e != nil {
				return nil, e
			}
			all := field(string(b), "status") == "stable"
			for _, p := range paths {
				if !strings.Contains(string(b), filepath.Base(p)) {
					all = false
				}
			}
			if all {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		p := Proposal{Type: "MOC", Title: tag, Description: "Connections for " + tag, Learning: "Review this cluster before accepting its hub.", Tags: []string{tag}}
		if e := validateINMAPS(vault, p); e != nil {
			return nil, e
		}
		path := filepath.Join(vault, "00 - Inbox", "moc-"+strings.ReplaceAll(tag, "/", "-")+".md")
		at := time.Now().UTC().Format(time.RFC3339)
		body := fmt.Sprintf("---\ntype: MOC\nid: %s\ntitle: %s\ndescription: %s\ntags: [%s]\ncreated: %s\nupdated: %s\nstatus: draft\nsource: %s\n---\n\n# %s\n\n## Overview\n\nReview these connections before accepting.\n\n%s\n", scalar(strings.TrimSuffix(filepath.Base(path), ".md")), scalar(tag), scalar(p.Description), scalar(tag), at, at, scalar("Derived from cited stable notes"), tag, mocLinks(paths))
		if _, e := os.Stat(path); os.IsNotExist(e) {
			changes[path] = []byte(body)
		}
		proposed = append(proposed, path)
	}
	if apply && len(changes) > 0 {
		unlock, e := lockFile(filepath.Join(vault, "99 - Meta/.candidate-memory.lock"))
		if e != nil {
			return nil, e
		}
		defer unlock()
		if e = writeSet(vault, changes); e != nil {
			return nil, e
		}
	}
	return proposed, nil
}
func mocLinks(paths []string) string {
	sort.Strings(paths)
	lines := []string{mocStart}
	for _, p := range paths {
		b, _ := os.ReadFile(p)
		lines = append(lines, "- ["+field(string(b), "title")+"](../01%20-%20Notes/"+filepath.Base(p)+")")
	}
	return strings.Join(append(lines, mocEnd), "\n")
}

// ReviewMOC accepts or rejects a tool-produced Inbox draft. The reviewer is an
// explicit actor; absent review never creates a live hub.
func ReviewMOC(vault, name, reviewer string, accept, apply bool) error {
	if apply {
		unlock, e := lockFile(filepath.Join(vault, "99 - Meta/.candidate-memory.lock"))
		if e != nil {
			return e
		}
		defer unlock()
	}
	if filepath.Base(name) != name || !strings.HasPrefix(name, "moc-") || !strings.HasSuffix(name, ".md") || strings.TrimSpace(reviewer) == "" {
		return fmt.Errorf("MOC basename and reviewer required")
	}
	path := filepath.Join(vault, "00 - Inbox", name)
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	raw := string(b)
	if field(raw, "type") != "MOC" || field(raw, "status") != "draft" {
		return fmt.Errorf("not a draft MOC")
	}
	at := time.Now().UTC().Format(time.RFC3339)
	changes := map[string][]byte{}
	if accept {
		dest := filepath.Join(vault, "02 - MOCs", strings.TrimPrefix(name, "moc-"))
		if _, e := os.Stat(dest); e == nil {
			return fmt.Errorf("existing hub must be refreshed, not overwritten")
		}
		live := replaceField(raw, "status", "stable")
		live = replaceField(live, "updated", at)
		live = replaceField(live, "verified", fmt.Sprintf("[{by: %s, at: %s}]", scalar(reviewer), at))
		changes[dest] = []byte(live)
	}
	changes[path] = []byte(replaceField(replaceField(raw, "status", "deprecated"), "updated", at))
	if !apply {
		return nil
	}
	return writeSet(vault, changes)
}
func RefreshMOCs(vault string, apply bool) (int, error) {
	if apply {
		unlock, e := lockFile(filepath.Join(vault, "99 - Meta/.candidate-memory.lock"))
		if e != nil {
			return 0, e
		}
		defer unlock()
	}
	hubs, _ := filepath.Glob(filepath.Join(vault, "02 - MOCs", "*.md"))
	notes, _ := filepath.Glob(filepath.Join(vault, "01 - Notes", "*.md"))
	changes := map[string][]byte{}
	for _, hub := range hubs {
		b, e := os.ReadFile(hub)
		if e != nil {
			return 0, e
		}
		raw := string(b)
		if field(raw, "status") != "stable" {
			continue
		}
		var tags []string
		if e = json.Unmarshal([]byte(field(raw, "tags")), &tags); e != nil {
			return 0, e
		}
		var paths []string
		for _, p := range notes {
			b, e := os.ReadFile(p)
			if e != nil {
				return 0, e
			}
			if field(string(b), "status") != "stable" {
				continue
			}
			var nt []string
			json.Unmarshal([]byte(field(string(b), "tags")), &nt)
			match := false
			for _, a := range tags {
				for _, b := range nt {
					if a == b {
						match = true
					}
				}
			}
			if match {
				paths = append(paths, p)
			}
		}
		start, end := strings.Index(raw, mocStart), strings.Index(raw, mocEnd)
		if start < 0 || end < start {
			return 0, fmt.Errorf("hub lacks generated section: %s", hub)
		}
		next := raw[:start] + mocLinks(paths) + raw[end+len(mocEnd):]
		if next != raw {
			next = replaceField(next, "updated", time.Now().UTC().Format(time.RFC3339))
			changes[hub] = []byte(next)
		}
	}
	if apply && len(changes) > 0 {
		return len(changes), writeSet(vault, changes)
	}
	return len(changes), nil
}
