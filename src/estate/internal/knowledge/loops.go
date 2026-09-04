package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// loopsSource reads every top-level .md file directly under dir
// (~/source/repos/Personal/Loops-Research) -- one Item per file, Tier1
// from the file's own first `# ` heading (falling back to its filename),
// Tier2 the first non-empty paragraph after it. Never recurses, never
// rewrites: this is a plain read of a plain directory of notes.
func loopsSource(dir string, clock *idClock) (SourceResult, []Item) {
	res := SourceResult{Name: "loops-research"}
	if dir == "" {
		res.Reason = "no Loops-Research path configured"
		return res, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		res.Reason = fmt.Sprintf("cannot list %s: %v", dir, err)
		return res, nil
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var items []Item
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // one unreadable note does not fail the source
		}
		heading, para := firstHeadingAndParagraph(string(data))
		if heading == "" {
			heading = strings.TrimSuffix(name, ".md")
		}
		publishable, basis := classify("loops-research")
		items = append(items, Item{
			ID:             clock.NextID(),
			Source:         "loops-research",
			Permalink:      path,
			StructuralTags: []string{"loops-research"},
			Tier1:          truncate(heading, 200),
			Tier2:          truncate(para, 400),
			Tier3:          "open " + path + " for the full note",
			Publishable:    publishable,
			PublishBasis:   basis,
		})
	}

	res.OK = true
	res.Count = len(items)
	return res, items
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
