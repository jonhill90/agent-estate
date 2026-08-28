package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Fact is one agent/facts/<slug>.md file, in full -- Type/Title/
// Description/Created from its own frontmatter (memory-conventions.md's
// own schema: "type (required), title, description, created/updated
// (ISO 8601 with seconds, UTC), source"), Body is everything after the
// closing frontmatter fence, unrendered markdown. Loaded ONLY when a
// caller actually opens this one fact (LoadFact below) -- never as part
// of drawing the list (index.go's own doc comment).
type Fact struct {
	Slug        string
	Type        string
	Title       string
	Description string
	Created     string
	Source      string
	Body        string
}

// LoadFact reads exactly one fact file -- vaultDir/agent/facts/<slug>.md
// -- and parses its frontmatter plus body. A slug from agent/index.md
// with no corresponding file (a stale link) is a real, visible error,
// never an empty Fact silently swapped in.
func LoadFact(vaultDir, slug string) (Fact, error) {
	if vaultDir == "" {
		return Fact{}, fmt.Errorf("$AGENT_MEMORY_VAULT is not set")
	}
	path := filepath.Join(vaultDir, "agent", "facts", slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return Fact{}, fmt.Errorf("read %s: %w", path, err)
	}
	f, err := parseFact(string(data))
	if err != nil {
		return Fact{}, fmt.Errorf("%s: %w", path, err)
	}
	f.Slug = slug
	return f, nil
}

// parseFact splits data into its `---`-fenced YAML frontmatter and body,
// the same minimal-scan discipline internal/skills.parseFrontmatter
// already documents for a comparably small, known field set (no YAML
// library in this module's go.mod, and the vault's own schema names
// exactly six scalar fields) -- a genuinely block-scalar value (`type: |
// ...`) would read here as only its first line, surfaced as a visibly
// odd value rather than silently truncated data a reader would trust.
func parseFact(data string) (Fact, error) {
	sc := bufio.NewScanner(strings.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return Fact{}, fmt.Errorf("does not start with a --- frontmatter fence")
	}

	var f Fact
	closed := false
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}
		key, val, ok := splitFrontmatterLine(line)
		if !ok {
			continue
		}
		switch key {
		case "type":
			f.Type = val
		case "title":
			f.Title = val
		case "description":
			f.Description = val
		case "created":
			f.Created = val
		case "source":
			f.Source = val
		}
	}
	if !closed {
		return Fact{}, fmt.Errorf("frontmatter never closed with a second ---")
	}

	var body strings.Builder
	for sc.Scan() {
		body.WriteString(sc.Text())
		body.WriteByte('\n')
	}
	f.Body = strings.TrimLeft(body.String(), "\n")
	return f, nil
}

func splitFrontmatterLine(line string) (key, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.Trim(strings.TrimSpace(line[idx+1:]), `"`)
	if key == "" {
		return "", "", false
	}
	return key, value, true
}
