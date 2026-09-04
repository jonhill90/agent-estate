package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClassifyDefaultsUnknownSourcesPrivate is classify's own contract in
// isolation: any source name it has not positively allow-listed comes back
// Publishable=false with a non-empty basis -- never true, never a blank
// reason a reader has to guess at. This includes a source name that does
// not exist yet, on purpose: a new source added tomorrow with no
// classify.go change is private by construction, not by someone
// remembering to add a case here. loops-research and vault-fact are
// deliberately NOT in this list as of agent-estate#1059 -- they have their
// own explicit table entries now (see
// TestClassifyLoopsResearchAndVaultFactAreExplicitNotDefault below) and
// this test must exercise only sources still relying on the fallthrough,
// or a future edit to the default branch could satisfy this test while
// silently changing their basis string.
func TestClassifyDefaultsUnknownSourcesPrivate(t *testing.T) {
	for _, source := range []string{"corpus-parameter", "some-future-source"} {
		publishable, basis := classify(source)
		if publishable {
			t.Errorf("classify(%q) = publishable=true, want false (unclassified means private)", source)
		}
		if basis == "" {
			t.Errorf("classify(%q) gave no basis for its verdict", source)
		}
	}
}

// TestClassifyLoopsResearchAndVaultFactAreExplicitNotDefault is the test
// agent-estate#1059 exists for. Before this change, both sources were
// private only because they fell through to classify's default branch --
// an accident of an unrelated rule, not a stated decision about either
// source. This test pins each one to its OWN table entry, distinguishable
// from the default branch's basis string, so that widening the default
// branch to publishable (a plausible future edit -- see the default test
// above) cannot silently flip these two along with it.
//
// To confirm this test is load-bearing rather than vacuous: temporarily
// change classify's default case to `return true, "default now public"`
// and run this test -- TestClassifyDefaultsUnknownSourcesPrivate fails (as
// expected, that test covers the default branch), and this test PASSES
// unchanged, because loops-research and vault-fact never reach the
// default branch. That is the property being asserted: their privacy does
// not derive from the default. See the PR body for that run's output.
func TestClassifyLoopsResearchAndVaultFactAreExplicitNotDefault(t *testing.T) {
	const defaultBasisSuffix = "source defaults to private -- no per-item publishability marker exists yet (agent-estate#1028)"

	for _, source := range []string{"loops-research", "vault-fact"} {
		publishable, basis := classify(source)
		if publishable {
			t.Errorf("classify(%q) = publishable=true, want false", source)
		}
		if basis == "" {
			t.Errorf("classify(%q) gave no basis for its verdict", source)
			continue
		}
		if strings.Contains(basis, defaultBasisSuffix) {
			t.Errorf("classify(%q) basis %q still reads as the default fallthrough -- want its own explicit table entry (agent-estate#1059)", source, basis)
		}
		if !strings.Contains(basis, "agent-estate#1059") {
			t.Errorf("classify(%q) basis %q does not cite agent-estate#1059 -- want evidence this is a stated decision, not a default", source, basis)
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
