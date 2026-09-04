package knowledge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestClassifyDefaultsUnknownSourcesPrivate is classify's own contract in
// isolation: any source name it has not positively allow-listed comes back
// Publishable=false with a non-empty basis -- never true, never a blank
// reason a reader has to guess at. This includes a source name that does
// not exist yet, on purpose: a fifth source added tomorrow with no
// classify.go change is private by construction, not by someone
// remembering to add a case here.
func TestClassifyDefaultsUnknownSourcesPrivate(t *testing.T) {
	for _, source := range []string{"vault-fact", "corpus-parameter", "loops-research", "some-future-source"} {
		publishable, basis := classify(source)
		if publishable {
			t.Errorf("classify(%q) = publishable=true, want false (unclassified means private)", source)
		}
		if basis == "" {
			t.Errorf("classify(%q) gave no basis for its verdict", source)
		}
	}
}

// TestClassifyGithubStarsIsTheOnlyPublicSource pins the one allow-listed
// case so a future edit to classify's switch cannot silently widen it
// without this test naming exactly what changed.
func TestClassifyGithubStarsIsTheOnlyPublicSource(t *testing.T) {
	publishable, basis := classify("github-stars")
	if !publishable {
		t.Fatal("classify(\"github-stars\") = publishable=false, want true")
	}
	if basis == "" {
		t.Fatal("classify(\"github-stars\") gave no basis")
	}
}

// TestGenerateNeverPublishesAnItemWithNoBasis is the end-to-end guard
// #1028 asks for, run over a real Generate() call across all four
// sources: every item in the Result must carry a non-empty PublishBasis,
// and Publishable=true must never appear without one. This is the test to
// break and watch fail -- flip classify's default branch in classify.go to
// `return true, ""` (unclassified source, no reason) and this test must
// fail; restore it and this test must pass again. See the PR body for
// both runs.
func TestGenerateNeverPublishesAnItemWithNoBasis(t *testing.T) {
	dir := t.TempDir()
	loopsDir := filepath.Join(dir, "loops")
	if err := os.MkdirAll(loopsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loopsDir, "a.md"), []byte("# A\n\npara\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeVaultFact(t, dir, "fact", fixtureVaultFact)

	cfg := Config{
		VaultDir:      dir,
		CorpusDBPath:  filepath.Join(dir, "absent.sqlite3"), // fails, exercises the SourceResult path only
		LoopsResearch: loopsDir,
		RunGH: func(args ...string) ([]byte, error) {
			return []byte(`{"full_name":"a/one","html_url":"https://github.com/a/one"}` + "\n"), nil
		},
	}
	res := Generate(cfg, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	if len(res.Items) == 0 {
		t.Fatal("test setup produced no items -- guard below would pass vacuously")
	}

	sawPublic, sawPrivate := false, false
	for _, it := range res.Items {
		if it.PublishBasis == "" {
			t.Fatalf("item %s (%s) has Publishable=%v with no PublishBasis", it.ID, it.Source, it.Publishable)
		}
		if it.Publishable {
			sawPublic = true
			if it.Source != "github-stars" {
				t.Errorf("item %s (%s) is Publishable=true, but only github-stars is classified public today", it.ID, it.Source)
			}
		} else {
			sawPrivate = true
		}
	}
	if !sawPublic || !sawPrivate {
		t.Fatalf("test setup did not exercise both classes: sawPublic=%v sawPrivate=%v", sawPublic, sawPrivate)
	}
}
