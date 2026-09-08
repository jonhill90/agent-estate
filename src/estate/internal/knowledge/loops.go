package knowledge

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoopsResearchFiles lists every .md file under dir, at any depth --
// agent-estate#1305's decided rule: this is a single-purpose research repo
// (jonhill90/Loops-Research), and everything in it, at every depth, is
// research content. specs/ (a routing README, two full skill specs, and a
// deferred-sketches file, read in full before deciding) is the same KIND of
// material as the 24 top-level files it sits beside -- the top-level
// README.md is itself a routing document mixed with real content, the exact
// shape specs/README.md has, and specs/deferred.md cites the same numbered
// research files (../04-verifiers.md, ../07-isolation.md, ...) the indexed
// files already do. No file in this repo is scaffolding by depth alone.
//
// Exported and shared: loopsSource (content) and main.go's staleness check
// both call this SAME function, so they describe the same set of files by
// construction, not by two independently written walks that happen to
// agree today and can drift apart tomorrow -- the exact coupling gap
// agent-estate#1305 was filed to close. Only dot-directories are skipped
// (.git and similar VCS/tooling metadata, never research content); nothing
// else is excluded, matching the "everything here is research" rule above.
func LoopsResearchFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// loopsSource reads every .md file under dir (~/source/repos/Personal/
// Loops-Research), at any depth -- one Item per file, Tier1 from the
// file's own first `# ` heading (falling back to its filename), Tier2 the
// first non-empty paragraph after it. Never rewrites: this is a plain read
// of a research directory's own notes, via LoopsResearchFiles so this
// source and main.go's staleness check can never describe different sets
// of files (agent-estate#1305).
func loopsSource(dir string) (SourceResult, []Item) {
	res := SourceResult{Name: "loops-research"}
	if dir == "" {
		res.Reason = "no Loops-Research path configured"
		return res, nil
	}
	paths, err := LoopsResearchFiles(dir)
	if err != nil {
		res.Reason = fmt.Sprintf("cannot list %s: %v", dir, err)
		return res, nil
	}

	var items []Item
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // one unreadable note does not fail the source
		}
		heading, para := firstHeadingAndParagraph(string(data))
		if heading == "" {
			heading = strings.TrimSuffix(filepath.Base(path), ".md")
		}
		publishable, basis := classify("loops-research")
		items = append(items, Item{
			ID:             itemID(path),
			Source:         "loops-research",
			Permalink:      path,
			StructuralTags: []string{"loops-research"},
			Tier1:          truncate(heading, 200),
			Tier2:          truncate(para, 400),
			Tier3:          loopsTier3(path, string(data)),
			Publishable:    publishable,
			PublishBasis:   basis,
		})
	}

	res.OK = true
	res.Count = len(items)
	return res, items
}

// loopsTier3 is the third disclosure rung for a Loops-Research note --
// agent-estate#1139 defect B: the pointer this used to return ("open <path>
// for the full note") was a pointer, not a deeper level of disclosure.
// Tier2 is only the note's first paragraph, truncated to 400 characters
// (loopsSource above); Tier3 is the ENTIRE note file verbatim, so a reader
// who follows the ladder actually gets more material at each step -- the
// full text a truncated single paragraph could never carry. raw is the
// note's own bytes, read once by the caller (loopsSource) and passed in
// here rather than re-read, so this can never diverge from what Tier1/Tier2
// were actually built from.
func loopsTier3(path, raw string) string {
	body := strings.TrimSpace(raw)
	if body == "" {
		return "(note file at " + path + " is empty)"
	}
	return body + "\n\n(full note file: " + path + ")"
}

// firstHeadingAndParagraph pulls a file's first `# ` heading and the
// first non-empty, non-heading line after it -- a mechanical extraction
// of text already in the file, never a summary this package composed.
func firstHeadingAndParagraph(data string) (heading, para string) {
	sc := bufio.NewScanner(strings.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sawHeading := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !sawHeading {
			if strings.HasPrefix(line, "# ") {
				heading = strings.TrimSpace(strings.TrimPrefix(line, "# "))
				sawHeading = true
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "|") {
			continue
		}
		para = line
		break
	}
	return heading, para
}
