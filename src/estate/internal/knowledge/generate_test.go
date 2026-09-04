package knowledge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGenerateAddsSourceTagForFiltering is agent-estate#1069's own
// reported gap: source tags were colon-less ("repo-docs", "github-stars"),
// so they could never be an exact-tag filter the way "kind:directive"
// already is -- a query for "repo-docs tmux" scored "repo" and "doc" as
// ordinary search terms instead of excluding every github-stars item.
//
// This reproduces the issue's own failure shape end to end: a
// github-stars item and a repo-docs item both carry the word "tmux", so
// an unscoped query for "tmux" returns both, but "source:repo-docs tmux"
// must return only the repo-docs item -- proving source: FILTERS rather
// than merely existing as a tag. Before generate.go's addSourceTag, no
// item carried a "source:" tag at all, so extractTagFilters would report
// "source:repo-docs" as an unknown tag and Query would return
// StateNoMatch instead of narrowing to the one item.
func TestGenerateAddsSourceTagForFiltering(t *testing.T) {
	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	agentsMD := "# AGENTS.md\n\n## tmux rule\n\nNever address the default tmux socket in a test.\n"
	if err := os.WriteFile(filepath.Join(repoRoot, "AGENTS.md"), []byte(agentsMD), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		RepoRoot:     repoRoot,
		CorpusDBPath: filepath.Join(dir, "absent.sqlite3"),
		RunGH: func(args ...string) ([]byte, error) {
			return []byte(`{"full_name":"omerxx/tmux-sessionx","html_url":"https://github.com/omerxx/tmux-sessionx","description":"tmux session manager"}` + "\n"), nil
		},
	}
	res := Generate(cfg, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))

	var sawStarsSourceTag, sawDocsSourceTag bool
	for _, it := range res.Items {
		want := "source:" + it.Source
		found := false
		for _, tag := range it.StructuralTags {
			if tag == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("item %s (source=%s) missing structural tag %q -- got %v", it.ID, it.Source, want, it.StructuralTags)
		}
		if it.Source == "github-stars" {
			sawStarsSourceTag = true
		}
		if it.Source == "repo-docs" {
			sawDocsSourceTag = true
		}
	}
	if !sawStarsSourceTag || !sawDocsSourceTag {
		t.Fatalf("fixture did not produce both sources: stars=%v docs=%v", sawStarsSourceTag, sawDocsSourceTag)
	}

	// existing colon-less tags must survive untouched (issue's own "must
	// not happen" list).
	for _, it := range res.Items {
		switch it.Source {
		case "github-stars":
			if !hasTag(it.StructuralTags, "github-stars") {
				t.Fatalf("github-stars item lost its colon-less tag: %v", it.StructuralTags)
			}
		case "repo-docs":
			if !hasTag(it.StructuralTags, "repo-docs") {
				t.Fatalf("repo-docs item lost its colon-less tag: %v", it.StructuralTags)
			}
		}
	}

	path := filepath.Join(dir, "index.json")
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}

	// Unscoped: both a github-stars item and a repo-docs item can match
	// "tmux" (the star's own description, the doc's own rule) -- this is
	// the pollution the issue measured, kept here only as the baseline
	// the scoped assertion below is proving a fix against.
	unscoped := Query(path, "tmux", 0, true)
	if unscoped.State != StateMatched {
		t.Fatalf("unscoped tmux query State = %q, want %q (reason=%q)", unscoped.State, StateMatched, unscoped.Reason)
	}

	// Scoped: source:repo-docs must FILTER to only repo-docs items, never
	// merely tag them -- this is the assertion that fails on the
	// pre-#1069 code (source:repo-docs was an unknown tag, so this
	// reported StateNoMatch) and passes once addSourceTag runs.
	scoped := Query(path, "source:repo-docs tmux", 0, true)
	if scoped.State != StateMatched {
		t.Fatalf("scoped State = %q, want %q (reason=%q)", scoped.State, StateMatched, scoped.Reason)
	}
	for _, m := range scoped.Matches {
		if m.Source != "repo-docs" {
			t.Fatalf("source:repo-docs filter let through a non-repo-docs match: %+v", m)
		}
	}
	if len(scoped.Matches) == 0 {
		t.Fatal("source:repo-docs tmux matched nothing -- filter excluded the real repo-docs item too")
	}
}

// TestGenerateRecordsGeneratedBy reproduces agent-estate#1082's own
// measured gap: Generate's output must carry GeneratedBy.Commit, resolved
// through the exact same seam ResolveBuildCommit exercises directly. Before
// #1082, Result had no GeneratedBy field at all -- this is the field this
// issue adds, wired at the one place (Generate) every caller's index goes
// through.
func TestGenerateRecordsGeneratedBy(t *testing.T) {
	cfg := Config{
		RepoRoot:     "/repo",
		CorpusDBPath: filepath.Join(t.TempDir(), "absent.sqlite3"),
		RunGH: func(args ...string) ([]byte, error) {
			return nil, os.ErrNotExist
		},
		RunGit: func(args ...string) ([]byte, error) {
			if args[len(args)-1] == "--porcelain" {
				return []byte(""), nil
			}
			return []byte("deadbeefcafedeadbeefcafedeadbeefcafedead\n"), nil
		},
	}
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	res := Generate(cfg, now)
	if res.GeneratedBy.Commit != "deadbeefcafedeadbeefcafedeadbeefcafedead" {
		t.Fatalf("GeneratedBy.Commit = %q, want the resolved HEAD", res.GeneratedBy.Commit)
	}
	if !res.GeneratedBy.BuiltAt.Equal(now) {
		t.Fatalf("GeneratedBy.BuiltAt = %v, want %v", res.GeneratedBy.BuiltAt, now)
	}
}

// TestGenerateRecordsUnknownGeneratedByWhenUnresolvable is the dirty-tree/
// no-checkout arm of the same requirement: never a guessed commit.
func TestGenerateRecordsUnknownGeneratedByWhenUnresolvable(t *testing.T) {
	cfg := Config{
		RepoRoot:     "", // no checkout resolved
		CorpusDBPath: filepath.Join(t.TempDir(), "absent.sqlite3"),
		RunGH: func(args ...string) ([]byte, error) {
			return nil, os.ErrNotExist
		},
	}
	res := Generate(cfg, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	if res.GeneratedBy.Commit != unknownCommit {
		t.Fatalf("GeneratedBy.Commit = %q, want %q", res.GeneratedBy.Commit, unknownCommit)
	}
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// TestGenerateReportsEverySourceHonestlyWhenAllFail is the whole "a
// source that cannot be read must say so" requirement, exercised
// end-to-end: five unreachable sources produce five SourceResults, all
// OK=false with a Reason, never a silently empty Result.
func TestGenerateReportsEverySourceHonestlyWhenAllFail(t *testing.T) {
	cfg := Config{
		VaultDir:      "",
		CorpusDBPath:  filepath.Join(t.TempDir(), "absent.sqlite3"),
		LoopsResearch: filepath.Join(t.TempDir(), "absent"),
		RepoRoot:      filepath.Join(t.TempDir(), "absent-repo"),
		RunGH: func(args ...string) ([]byte, error) {
			return nil, os.ErrNotExist
		},
	}
	res := Generate(cfg, time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC))
	if len(res.Sources) != 5 {
		t.Fatalf("got %d sources, want 5", len(res.Sources))
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
