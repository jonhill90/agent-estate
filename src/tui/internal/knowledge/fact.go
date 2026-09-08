package knowledge

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Fact is one 01 - Notes/<earned subdir>/<id>.md file, in full -- Type/
// Title/Description/Created from its own frontmatter (memory-conventions.md's
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

// LoadFact reads exactly one fact file and parses its frontmatter plus
// body. A slug from index.md with no corresponding file (a stale link) is
// a real, visible error, never an empty Fact silently swapped in.
func LoadFact(vaultDir, slug string) (Fact, error) {
	if vaultDir == "" {
		return Fact{}, fmt.Errorf("$AGENT_MEMORY_VAULT is not set")
	}
	path, data, err := resolveNoteFile(vaultDir, slug)
	if err != nil {
		return Fact{}, err
	}
	f, err := parseFact(string(data))
	if err != nil {
		return Fact{}, fmt.Errorf("%s: %w", path, err)
	}
	f.Slug = slug
	return f, nil
}

// aliasesLineRE matches a frontmatter aliases: line holding a flow-style
// list -- both quoting conventions seen in the vault today are accepted:
// aliases: [slug] (bare) and aliases: ["slug"] (quoted). Same pattern
// src/estate/internal/corpus/standinglaw.go's aliasesLineRE uses for the
// identical problem; reproduced, not imported (separate Go module).
var aliasesLineRE = regexp.MustCompile(`(?m)^aliases:\s*\[(.*)\]\s*$`)

// resolveNoteFile locates slug's backing note under vaultDir/01 - Notes/,
// walked RECURSIVELY (facts live one directory deeper than 01 - Notes/
// itself, under an earned subdir like 01f - Facts -- agent-estate#1283's
// own defect class; agent-estate#1304 is this reader's instance of it).
// slug matches either the note's own filename stem -- the common case: an
// id read straight from index.md -- or a frontmatter aliases: entry (a
// pre-relayout name a [[wikilink]] or an older caller might still pass).
// This is the same two-form resolution
// src/estate/internal/corpus/standinglaw.go's resolveStandingLawMemberFile
// uses for the identical problem (a declared name that must keep
// resolving across a vault relayout) -- reproduced here rather than
// imported, since src/tui is its own Go module, separate from
// src/estate. The first note matching wins; index.md is small and
// curated, so a genuine collision would be caught by a human before it
// could matter (same reasoning standinglaw.go's own doc comment gives).
//
// Two passes, not one, and deliberately so: this runs every time a reader
// opens a fact in the live TUI, an interactive path, unlike
// standinglaw.go's own once-per-dispatch call. Pass 1 compares filenames
// only (fs.DirEntry.Name(), never opened) -- the expected case, since
// index.md now names an id directly (agent-estate#1304), resolves here
// without reading a single OTHER file's content. Pass 2, the aliases
// fallback for a pre-relayout slug or a caller that did not go through
// LoadIndex, only runs -- and only then opens file content -- when pass 1
// found nothing.
func resolveNoteFile(vaultDir, slug string) (path string, raw []byte, err error) {
	notesDir := filepath.Join(vaultDir, "01 - Notes")

	var byName string
	nameErr := filepath.WalkDir(notesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if byName != "" {
			return nil // already resolved -- keep walking cheaply to EOF
		}
		if !d.IsDir() && noteFilename.MatchString(d.Name()) && strings.TrimSuffix(d.Name(), ".md") == slug {
			byName = p
		}
		return nil
	})
	if nameErr != nil {
		return "", nil, fmt.Errorf("%s could not be searched for %q: %w", notesDir, slug, nameErr)
	}
	if byName != "" {
		b, rerr := os.ReadFile(byName)
		if rerr != nil {
			return "", nil, fmt.Errorf("read %s: %w", byName, rerr)
		}
		return byName, b, nil
	}

	var found string
	var foundRaw []byte
	walkErr := filepath.WalkDir(notesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !noteFilename.MatchString(d.Name()) {
			return nil
		}
		if found != "" {
			return nil // already resolved -- keep walking cheaply to EOF
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil // unreadable candidate -- keep searching, do not fail the whole resolution on it
		}
		if noteDeclaresAlias(string(b), slug) {
			found, foundRaw = p, b
		}
		return nil
	})
	if walkErr != nil {
		return "", nil, fmt.Errorf("%s could not be searched for %q: %w", notesDir, slug, walkErr)
	}
	if found == "" {
		return "", nil, fmt.Errorf("no note under %s matches %q by filename or alias -- a stale link", notesDir, slug)
	}
	return found, foundRaw, nil
}

// noteDeclaresAlias reports whether raw's frontmatter carries an
// aliases: flow list containing slug exactly.
func noteDeclaresAlias(raw, slug string) bool {
	fm, ok := frontmatterBlock(raw)
	if !ok {
		return false
	}
	m := aliasesLineRE.FindStringSubmatch(fm)
	if m == nil {
		return false
	}
	for _, item := range strings.Split(m[1], ",") {
		item = strings.Trim(strings.TrimSpace(item), `"'`)
		if item == slug {
			return true
		}
	}
	return false
}

// frontmatterBlock returns the text strictly between the opening and
// closing --- fences, or ok=false if raw has no well-formed fence pair.
func frontmatterBlock(raw string) (string, bool) {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), true
		}
	}
	return "", false
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
