package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The compiled knowledge index, read for display.
//
// `estate knowledge` compiles an index over sources that already exist --
// GitHub stars first. This reads that artifact so the Knowledge route shows
// it without anyone running a command.
//
// It is a READER of a DERIVED file. If the index is missing, that is a real
// and reportable state, never an empty list: an empty list would say "there
// is no knowledge", which is a lie about the sources rather than a fact
// about the index.

var reRow = regexp.MustCompile("^\\| `(\\d{14})` \\| (.+?) \\| (.*?) \\| (.*?) \\|$")
var reGeneratedAt = regexp.MustCompile(`^generated_at: (.+)$`)
var reStaleAfter = regexp.MustCompile(`^stale_after: (.+)$`)
var reLink = regexp.MustCompile(`^\[([^\]]+)\]\(([^)]+)\)(?:<br>(.*))?$`)

// CompiledSource is one compiled source file, with the freshness the file
// declares about itself.
type CompiledSource struct {
	Slug        string
	GeneratedAt time.Time
	StaleAfter  time.Time
	Entries     []IndexEntry
}

// Stale reports whether the file has passed the staleness rule it carries.
// The answer is the FILE's own claim, not this reader's opinion.
func (c CompiledSource) Stale(now time.Time) bool {
	return !c.StaleAfter.IsZero() && now.After(c.StaleAfter)
}

// LoadCompiled reads every compiled source under docs/knowledge/sources.
//
// A missing directory is an error, not an empty result. The Knowledge route
// must be able to say "the index has not been compiled" rather than
// implying the operator has no knowledge.
func LoadCompiled(repoRoot string) ([]CompiledSource, error) {
	dir := filepath.Join(repoRoot, "docs", "knowledge", "sources")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("no compiled knowledge index at %s -- run `estate knowledge`: %w", dir, err)
	}
	var out []CompiledSource
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		c, err := loadCompiledFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func loadCompiledFile(path string) (CompiledSource, error) {
	f, err := os.Open(path)
	if err != nil {
		return CompiledSource{}, err
	}
	defer f.Close()

	c := CompiledSource{Slug: strings.TrimSuffix(filepath.Base(path), ".md")}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := reGeneratedAt.FindStringSubmatch(line); m != nil {
			c.GeneratedAt, _ = time.Parse(time.RFC3339, strings.TrimSpace(m[1]))
			continue
		}
		if m := reStaleAfter.FindStringSubmatch(line); m != nil {
			c.StaleAfter, _ = time.Parse(time.RFC3339, strings.TrimSpace(m[1]))
			continue
		}
		m := reRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		title, desc := m[2], ""
		if lm := reLink.FindStringSubmatch(strings.TrimSpace(m[2])); lm != nil {
			title = lm[1]
			desc = strings.TrimSpace(lm[3])
		}
		signal, tags := strings.TrimSpace(m[3]), strings.TrimSpace(m[4])
		bits := []string{}
		if desc != "" {
			bits = append(bits, desc)
		}
		if signal != "" {
			bits = append(bits, signal)
		}
		if tags != "" {
			bits = append(bits, tags)
		}
		c.Entries = append(c.Entries, IndexEntry{
			Slug:        m[1],
			Title:       title,
			Description: strings.Join(bits, " · "),
		})
	}
	return c, sc.Err()
}

// NewCompiledFetcher returns a Fetcher over the compiled index, with the
// vault's own entries first so the route keeps showing what it always did.
//
// A failure in either source is reported rather than swallowed: half an
// index rendered as a whole one is the drift this whole design exists to
// prevent.
func NewCompiledFetcher(vaultDir, repoRoot string, now func() time.Time) Fetcher {
	return func() ([]IndexEntry, error) {
		vault, vaultErr := LoadIndex(vaultDir)
		compiled, compErr := LoadCompiled(repoRoot)
		if vaultErr != nil && compErr != nil {
			return nil, vaultErr
		}
		out := append([]IndexEntry(nil), vault...)
		for _, c := range compiled {
			banner := fmt.Sprintf("compiled %s", c.GeneratedAt.UTC().Format("2006-01-02 15:04"))
			if c.Stale(now()) {
				banner = "STALE since " + c.StaleAfter.UTC().Format("2006-01-02") + " -- run `estate knowledge`"
			}
			out = append(out, IndexEntry{
				Slug:        c.Slug,
				Title:       "── " + c.Slug + " (" + fmt.Sprint(len(c.Entries)) + " items)",
				Description: banner,
			})
			out = append(out, c.Entries...)
		}
		if compErr != nil {
			out = append(out, IndexEntry{
				Slug:        "compiled-index-missing",
				Title:       "── compiled index unavailable",
				Description: compErr.Error(),
			})
		}
		return out, nil
	}
}
