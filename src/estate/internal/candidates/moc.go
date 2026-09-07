package candidates

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const mocStart = "<!-- generated-links:start -->"
const mocEnd = "<!-- generated-links:end -->"

// walkNotes lists every *.md file under "01 - Notes", at any depth --
// notes live directly there (the layout MOCProposals/RefreshMOCs were
// originally tested against) AND nested under earned letter subdirs like
// "01p - Parameters"/"01f - Facts" (agent-estate#942's note-subdirs
// registry, the layout the live vault actually uses). filepath.Glob's
// "*.md" pattern only ever matched the flat case -- against the real
// vault (every note one directory deeper) it silently returned zero
// notes, so MOCProposals/RefreshMOCs never saw a single one to group or
// refresh, no matter how dense a tag became. This was found running C4
// (Push 4.5) against the vault C2 just tagged: `moc-propose` returned
// `null` with tag counts well past the >=8 threshold. Recursive by
// WalkDir, same traversal internal/knowledge/vault.go already uses for
// the identical directory, so this file no longer disagrees with the
// package that reads the same tree correctly.
func walkNotes(vault string) ([]string, error) {
	var notes []string
	root := filepath.Join(vault, "01 - Notes")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			notes = append(notes, p)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Strings(notes)
	return notes, nil
}

// MOCProposals emits drafts only; a hub must already link the whole cluster to
// suppress a proposal. Refresh changes only a delimited generated link section.
func MOCProposals(vault string, apply bool) ([]string, error) {
	groups := map[string][]string{}
	notes, e := walkNotes(vault)
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
			// A structural/time tag (note, MM-YYYY, standing-rule) groups
			// past the threshold on nearly every real vault -- they are
			// not in 99 - Meta/tags.md's governed vocabulary because
			// they are never meant to head a MOC, only an associative or
			// axis-based tag is. Before this fix, the first such tag
			// reached (guaranteed, since these are near-universal) made
			// validateINMAPS's tag-vocabulary check fail and this whole
			// function returned that as a hard error, aborting proposals
			// for every OTHER, genuinely governed tag that also cleared
			// the threshold -- found running Push 4.5 C4 against the
			// live vault: `moc-propose` failed outright with "tag
			// outside vocabulary: 07-2026" and produced zero proposals
			// for azure/deploy/estate/etc., which were all well past 8.
			// Skipping an ungoverned tag is the correct reading of "not
			// MOC-eligible," not an error to abort the batch over --
			// every other validateINMAPS failure this call can actually
			// produce (bad type, missing title/description/learning) is
			// impossible here since MOCProposals constructs every field
			// of p itself except Tags.
			continue
		}
		path := filepath.Join(vault, "00 - Inbox", "moc-"+strings.ReplaceAll(tag, "/", "-")+".md")
		at := time.Now().UTC().Format(time.RFC3339)
		body := fmt.Sprintf("---\ntype: MOC\nid: %s\ntitle: %s\ndescription: %s\ntags: [%s]\ncreated: %s\nupdated: %s\nstatus: draft\nsource: %s\n---\n\n# %s\n\n## Overview\n\nReview these connections before accepting.\n\n%s\n", scalar(strings.TrimSuffix(filepath.Base(path), ".md")), scalar(tag), scalar(p.Description), scalar(tag), at, at, scalar("Derived from cited stable notes"), tag, mocLinks(paths, vault))
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
// mocLinks builds the generated-links section, one wikilink per note, from
// the SAME MOC-relative root every hub actually sits under: "00 - Inbox"
// and "02 - MOCs" are both direct children of vault, exactly one level up
// from "01 - Notes" -- so "../01 - Notes/" is correct for either location,
// but only if what follows it is the note's REAL path under "01 - Notes",
// not just its filename. Before this fix it was filepath.Base(p) alone,
// which produced "../01%20-%20Notes/<id>.md" for every note regardless of
// which earned letter subdir (agent-estate#942: "01p - Parameters", "01f
// - Facts") it actually lives in -- a link Obsidian cannot resolve, since
// every real note in this vault lives one directory deeper than that.
// Found generating the first real MOC drafts against the live, nested
// vault for Push 4.5 C4: `moc-estate.md`'s own links all 404'd.
func mocLinks(paths []string, vault string) string {
	sort.Strings(paths)
	lines := []string{mocStart}
	notesRoot := filepath.Join(vault, "01 - Notes")
	for _, p := range paths {
		b, _ := os.ReadFile(p)
		rel, err := filepath.Rel(notesRoot, p)
		if err != nil {
			rel = filepath.Base(p)
		}
		href := strings.ReplaceAll(filepath.ToSlash(rel), " ", "%20")
		lines = append(lines, "- ["+field(string(b), "title")+"](../01%20-%20Notes/"+href+")")
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
	notes, e := walkNotes(vault)
	if e != nil {
		return 0, e
	}
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
		next := raw[:start] + mocLinks(paths, vault) + raw[end+len(mocEnd):]
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
