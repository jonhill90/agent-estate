package knowledge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGenerateReportsEverySourceHonestlyWhenAllFail is the whole "a
// source that cannot be read must say so" requirement, exercised
// end-to-end: four unreachable sources produce four SourceResults, all
// OK=false with a Reason, never a silently empty Result.
func TestGenerateReportsEverySourceHonestlyWhenAllFail(t *testing.T) {
	cfg := Config{
		VaultDir:      "",
		CorpusDBPath:  filepath.Join(t.TempDir(), "absent.sqlite3"),
		LoopsResearch: filepath.Join(t.TempDir(), "absent"),
		RunGH: func(args ...string) ([]byte, error) {
			return nil, os.ErrNotExist
		},
	}
	res := Generate(cfg, time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC))
	if len(res.Sources) != 4 {
		t.Fatalf("got %d sources, want 4", len(res.Sources))
	}
	for _, s := range res.Sources {
		if s.OK {
			t.Errorf("source %s reported OK when it should have failed", s.Name)
		}
		if s.Reason == "" {
			t.Errorf("source %s failed with no reason", s.Name)
		}
	}
	if len(res.Items) != 0 {
		t.Fatalf("got %d items from all-failed sources, want 0", len(res.Items))
	}
	if res.Note == "" || res.StalenessRule == "" {
		t.Fatal("Result missing its own derived/staleness statement")
	}
}

// TestGenerateOneFailingSourceDoesNotStopTheOthers confirms independence
// between sources -- a dead vault must not blank out real GitHub stars.
func TestGenerateOneFailingSourceDoesNotStopTheOthers(t *testing.T) {
	dir := t.TempDir()
	loopsDir := filepath.Join(dir, "loops")
	if err := os.MkdirAll(loopsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loopsDir, "a.md"), []byte("# A\n\npara\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		VaultDir:      "", // fails
		CorpusDBPath:  filepath.Join(dir, "absent.sqlite3"),
		LoopsResearch: loopsDir, // succeeds
		RunGH: func(args ...string) ([]byte, error) {
			return []byte(`{"full_name":"a/one","html_url":"x"}` + "\n"), nil
		},
	}
	res := Generate(cfg, time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC))

	var loopsOK, starsOK bool
	for _, s := range res.Sources {
		if s.Name == "loops-research" && s.OK {
			loopsOK = true
		}
		if s.Name == "github-stars" && s.OK {
			starsOK = true
		}
	}
	if !loopsOK || !starsOK {
		t.Fatalf("a failing source suppressed a working one: %+v", res.Sources)
	}
	if len(res.Items) != 2 {
		t.Fatalf("got %d items, want 2 (one star, one loops note)", len(res.Items))
	}
}

// TestGenerateIDsNeverCollideAcrossSources is the same guarantee id_test
// checks in isolation, exercised across every source sharing one clock.
func TestGenerateIDsNeverCollideAcrossSources(t *testing.T) {
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
			return []byte(
				`{"full_name":"a/one","html_url":"x"}` + "\n" +
					`{"full_name":"a/two","html_url":"y"}` + "\n",
			), nil
		},
	}
	res := Generate(cfg, time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC))
	seen := map[string]bool{}
	for _, it := range res.Items {
		if seen[it.ID] {
			t.Fatalf("duplicate id %q across sources", it.ID)
		}
		seen[it.ID] = true
	}
}
