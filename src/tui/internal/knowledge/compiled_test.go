package knowledge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeIndex(t *testing.T, root, slug, generated, stale string, rows ...string) {
	t.Helper()
	dir := filepath.Join(root, "docs", "knowledge", "sources")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ntitle: x\npermalink: knowledge/" + slug + "\ngenerated_at: " + generated +
		"\nstale_after: " + stale + "\n---\n\n| id | item | signal | tags |\n|---|---|---|---|\n"
	for _, r := range rows {
		body += r + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCompiledReadsRowsAndFreshness(t *testing.T) {
	root := t.TempDir()
	writeIndex(t, root, "github-stars", "2026-09-03T05:00:00Z", "2026-09-10T05:00:00Z",
		"| `20260903050000` | [pacifio/atlas](https://github.com/pacifio/atlas)<br>Source control for agents. | pushed 2026-09-02 | #agent-harness stars |")

	got, err := LoadCompiled(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Entries) != 1 {
		t.Fatalf("want 1 source with 1 entry, got %+v", got)
	}
	e := got[0].Entries[0]
	if e.Title != "pacifio/atlas" {
		t.Errorf("title = %q, want the repo name without the link markup", e.Title)
	}
	if e.Slug != "20260903050000" {
		t.Errorf("slug = %q, want the 14-char id", e.Slug)
	}
	for _, want := range []string{"Source control", "pushed 2026-09-02", "#agent-harness"} {
		if !contains(e.Description, want) {
			t.Errorf("description %q missing %q", e.Description, want)
		}
	}
}

// The file's own staleness rule is what decides, not this reader's opinion.
func TestStalenessComesFromTheFile(t *testing.T) {
	root := t.TempDir()
	writeIndex(t, root, "s", "2026-09-01T00:00:00Z", "2026-09-08T00:00:00Z",
		"| `20260901000000` | [a](https://x/a) | s | t |")
	got, _ := LoadCompiled(root)
	if got[0].Stale(time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)) {
		t.Error("before stale_after it is fresh")
	}
	if !got[0].Stale(time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)) {
		t.Error("after stale_after it is stale")
	}
}

// A missing index is a reportable state, never an empty list. An empty list
// would say "there is no knowledge" -- a lie about the sources rather than a
// fact about the index.
func TestMissingIndexIsReportedNotEmptied(t *testing.T) {
	if _, err := LoadCompiled(t.TempDir()); err == nil {
		t.Fatal("a missing compiled index must be an error")
	}
	f := NewCompiledFetcher("", t.TempDir(), time.Now)
	got, err := f()
	if err == nil {
		var sawNotice bool
		for _, e := range got {
			if e.Slug == "compiled-index-missing" {
				sawNotice = true
			}
		}
		if !sawNotice {
			t.Error("the route must say the index is unavailable, not show nothing")
		}
	}
}

// A stale file must announce itself where a reader is looking.
func TestStaleIndexAnnouncesItselfInTheList(t *testing.T) {
	root := t.TempDir()
	writeIndex(t, root, "github-stars", "2026-01-01T00:00:00Z", "2026-01-08T00:00:00Z",
		"| `20260101000000` | [a](https://x/a) | s | t |")
	f := NewCompiledFetcher("", root, func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) })
	got, err := f()
	if err != nil {
		t.Fatal(err)
	}
	var sawStale bool
	for _, e := range got {
		if contains(e.Description, "STALE") {
			sawStale = true
		}
	}
	if !sawStale {
		t.Error("a compiled file past its own staleness rule must say so in the list")
	}
}

func contains(h, n string) bool {
	return len(n) == 0 || (len(h) >= len(n) && (func() bool {
		for i := 0; i+len(n) <= len(h); i++ {
			if h[i:i+len(n)] == n {
				return true
			}
		}
		return false
	})())
}
