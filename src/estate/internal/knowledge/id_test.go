package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestItemIDHasStablePrefixAndLength(t *testing.T) {
	id := itemID("https://github.com/a/one")
	if !strings.HasPrefix(id, "it-") {
		t.Fatalf("itemID() = %q, want an \"it-\" prefix", id)
	}
	if len(id) != len("it-")+16 {
		t.Fatalf("itemID() = %q, want 16 hex chars after \"it-\"", id)
	}
}

// TestItemIDIsStableForTheSamePermalink is the same-input half of
// #1026's requirement: two calls over the same permalink, however far
// apart, must return the same id -- that is what lets a citation
// written into an issue or a brief still resolve later.
func TestItemIDIsStableForTheSamePermalink(t *testing.T) {
	first := itemID("corpus:item:it-abc123")
	second := itemID("corpus:item:it-abc123")
	if first != second {
		t.Fatalf("itemID() = %q then %q for the same permalink, want equal", first, second)
	}
}

// TestItemIDChangesWhenPermalinkChanges is the other half: an id is a
// function of permalink alone, so two DIFFERENT items must not collide.
func TestItemIDChangesWhenPermalinkChanges(t *testing.T) {
	a := itemID("https://github.com/a/one")
	b := itemID("https://github.com/a/two")
	if a == b {
		t.Fatalf("itemID() collided for two different permalinks: %q", a)
	}
}

// TestItemIDIsStableAcrossTwoGenerateCalls is agent-estate#1026's own
// required regression test: regenerate the compiled index twice from
// the same sources and confirm an id returned by the first Generate
// call still resolves the SAME item (by Permalink) in the second. This
// is the guarantee `query` + `get`'s two-step progressive disclosure
// depends on -- see id.go's own doc comment on why the old wall-clock
// idClock broke it.
func TestItemIDIsStableAcrossTwoGenerateCalls(t *testing.T) {
	dir := t.TempDir()
	loopsDir := filepath.Join(dir, "loops")
	if err := os.MkdirAll(loopsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loopsDir, "a.md"), []byte("# A\n\npara\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		LoopsResearch: loopsDir,
		CorpusDBPath:  filepath.Join(dir, "absent.sqlite3"),
		RunGH: func(args ...string) ([]byte, error) {
			return []byte(`{"full_name":"a/one","html_url":"https://github.com/a/one","description":"first"}` + "\n"), nil
		},
	}

	// Two Generate calls, one second apart -- the exact axis the old
	// idClock varied on and this one must not.
	first := Generate(cfg, time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC))
	second := Generate(cfg, time.Date(2026, 9, 3, 12, 0, 1, 0, time.UTC))

	byPermalink := func(res Result, permalink string) (Item, bool) {
		for _, it := range res.Items {
			if it.Permalink == permalink {
				return it, true
			}
		}
		return Item{}, false
	}

	star1, ok := byPermalink(first, "https://github.com/a/one")
	if !ok {
		t.Fatal("first Generate() produced no github-stars item")
	}
	star2, ok := byPermalink(second, "https://github.com/a/one")
	if !ok {
		t.Fatal("second Generate() produced no github-stars item")
	}
	if star1.ID != star2.ID {
		t.Fatalf("Item.ID for the same permalink changed across regenerates: %q then %q", star1.ID, star2.ID)
	}

	// The same must hold for the id Get() actually resolves against --
	// prove it end to end through Query+Get on a real written index, not
	// just by comparing Item.ID fields in memory.
	path := filepath.Join(dir, "index.json")
	if err := Write(path, first); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, second); err != nil {
		t.Fatal(err)
	}
	item, ok, reason := Get(path, star1.ID)
	if !ok {
		t.Fatalf("Get(%q) after regenerate ok=false, reason=%q", star1.ID, reason)
	}
	if item.Permalink != "https://github.com/a/one" {
		t.Fatalf("Get(%q) after regenerate resolved a different item: %+v", star1.ID, item)
	}
}
