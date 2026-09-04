package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureAgentsMD = `# fixture-repo — agent orientation

Intro prose before any ## heading -- must not become its own item.

## The daemon

### Before you ask Jon anything -- read this first

Capture exit codes directly. ` + "`cmd | tail`" + ` gives you tail's status.

### Conventions

Daemon-side conventions live here.

## The TUI

### Conventions

TUI-side conventions -- a different section than the daemon's own
"Conventions" heading above, deliberately reusing the same heading text.
`

func writeFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(fixtureAgentsMD), 0o644); err != nil {
		t.Fatal(err)
	}
	docsDir := filepath.Join(dir, "docs", "product")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nested := "# fixture — nested doc\n\n## A nested section\n\nBody text under a nested docs/ file.\n"
	if err := os.WriteFile(filepath.Join(docsDir, "SPEC.md"), []byte(nested), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRepoDocsSourceIndexesByHeadingNotByFile(t *testing.T) {
	dir := writeFixtureRepo(t)
	res, items := repoDocsSource(dir)
	if !res.OK {
		t.Fatalf("repoDocsSource() failed: %+v", res)
	}
	// AGENTS.md: "The daemon" and "The TUI" (the two ## headings) carry no
	// prose of their own before their first ### subheading, so they
	// produce no item (a heading with nothing under it has nothing to
	// answer with) -- only "Before you ask..." and "Conventions" (under
	// "The daemon") and "Conventions" (under "The TUI") do, 3 items;
	// docs/product/SPEC.md's "A nested section" is a 4th. The bare intro
	// prose before any heading, and the level-1 titles themselves, must
	// never become items of their own.
	if res.Count != 4 {
		t.Fatalf("got %d items, want 4 (one per heading with prose, not one per file): %+v", res.Count, items)
	}
	for _, it := range items {
		if it.Source != "repo-docs" {
			t.Errorf("item %+v has Source %q, want repo-docs", it, it.Source)
		}
		if !it.Publishable {
			t.Errorf("item %+v is not Publishable -- repo docs are public source", it)
		}
	}
}

// TestRepoDocsSourceDisambiguatesSameHeadingTextAcrossFiles is the
// "AGENTS.md has two 'Conventions' sections" case docs.go's own comment
// names -- both must survive as distinct items with distinct ids.
func TestRepoDocsSourceDisambiguatesSameHeadingTextAcrossFiles(t *testing.T) {
	dir := writeFixtureRepo(t)
	_, items := repoDocsSource(dir)

	var conventions []Item
	for _, it := range items {
		if strings.Contains(it.Tier1, "Conventions") {
			conventions = append(conventions, it)
		}
	}
	if len(conventions) != 2 {
		t.Fatalf("got %d 'Conventions' items, want 2: %+v", len(conventions), conventions)
	}
	if conventions[0].ID == conventions[1].ID {
		t.Fatalf("both 'Conventions' sections got the same id %q", conventions[0].ID)
	}
	if conventions[0].Permalink == conventions[1].Permalink {
		t.Fatalf("both 'Conventions' sections got the same permalink %q", conventions[0].Permalink)
	}
}

// TestRepoDocsSourceCarriesTheExitCodeRule is agent-estate#1034's own
// motivating case: the rule that could not previously be found by
// `estate knowledge query` must now be present, verbatim, in some item's
// Tier2.
func TestRepoDocsSourceCarriesTheExitCodeRule(t *testing.T) {
	dir := writeFixtureRepo(t)
	_, items := repoDocsSource(dir)

	found := false
	for _, it := range items {
		if strings.Contains(it.Tier2, "Capture exit codes directly") {
			found = true
		}
	}
	if !found {
		t.Fatal("no item's Tier2 carries AGENTS.md's own exit-code rule")
	}
}

func TestRepoDocsSourceEmptyRootIsHonestNotEmpty(t *testing.T) {
	res, items := repoDocsSource("")
	if res.OK {
		t.Fatal("repoDocsSource(\"\") reported OK for an unresolved repo root")
	}
	if res.Reason == "" {
		t.Fatal("repoDocsSource(\"\") gave no reason")
	}
	if items != nil {
		t.Fatal("repoDocsSource(\"\") returned items despite being unreadable")
	}
}

func TestRepoDocsSourceNoAgentsOrDocsIsHonest(t *testing.T) {
	res, _ := repoDocsSource(t.TempDir())
	if res.OK {
		t.Fatal("repoDocsSource() reported OK for a root with no AGENTS.md or docs/")
	}
	if res.Reason == "" {
		t.Fatal("repoDocsSource() gave no reason for the missing files")
	}
}

func TestSplitMarkdownSectionsFoldsDeeperHeadingsIntoBody(t *testing.T) {
	md := "# Title\n\n## Top\n\nintro\n\n### Sub\n\nsub body\n"
	secs := splitMarkdownSections(md, 2) // ##-only: ### folds into ## body
	if len(secs) != 1 {
		t.Fatalf("got %d sections, want 1: %+v", len(secs), secs)
	}
	if !strings.Contains(secs[0].Body, "### Sub") || !strings.Contains(secs[0].Body, "sub body") {
		t.Fatalf("Body = %q, want the ### heading and its text folded in", secs[0].Body)
	}

	secs3 := splitMarkdownSections(md, 3) // ##+###: ### gets its own section
	if len(secs3) != 2 {
		t.Fatalf("got %d sections at maxLevel=3, want 2: %+v", len(secs3), secs3)
	}
}

func TestHeadingAnchorDisambiguatesSameLeafTextByAncestors(t *testing.T) {
	a := headingAnchor([]string{"fixture-repo — agent orientation", "The daemon", "Conventions"})
	b := headingAnchor([]string{"fixture-repo — agent orientation", "The TUI", "Conventions"})
	if a == b {
		t.Fatalf("headingAnchor collided for two different ancestor paths: %q", a)
	}
}

func TestFindRepoRootWalksUpToAGENTSMD(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "src", "estate")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findRepoRoot(nested); got != root {
		t.Fatalf("findRepoRoot(%q) = %q, want %q", nested, got, root)
	}
}

func TestFindRepoRootReturnsEmptyWhenNoMarkerExists(t *testing.T) {
	if got := findRepoRoot(t.TempDir()); got != "" {
		t.Fatalf("findRepoRoot() = %q, want \"\" (no AGENTS.md anywhere above a bare temp dir)", got)
	}
}
