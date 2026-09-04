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

// TestRepoDocsSourceIDIsStableAcrossCheckoutRoots is agent-estate#1072's
// own regression: two DIFFERENT checkout roots holding byte-identical
// content must mint the SAME id for the same section. Before the fix,
// repoDocsSource's permalink embedded the absolute path passed in as
// repoRoot, so itemID (a pure function of permalink) produced a
// different id per checkout for identical prose -- exactly the failure
// #1072 measured between /tmp/estate-main and a second worktree of the
// same commit. This asserts on the id, not merely that the permalink
// "looks relative" -- a relative-looking string that still differed
// between checkouts would pass a weaker check and still break citations.
func TestRepoDocsSourceIDIsStableAcrossCheckoutRoots(t *testing.T) {
	rootA := writeFixtureRepo(t)
	rootB := writeFixtureRepo(t)
	if rootA == rootB {
		t.Fatal("t.TempDir() gave the same path twice -- test setup is broken, not the fix")
	}

	_, itemsA := repoDocsSource(rootA)
	_, itemsB := repoDocsSource(rootB)
	if len(itemsA) != len(itemsB) {
		t.Fatalf("got %d items from rootA, %d from rootB -- fixtures must be identical", len(itemsA), len(itemsB))
	}

	byTier1A := make(map[string]Item, len(itemsA))
	for _, it := range itemsA {
		byTier1A[it.Tier1] = it
	}
	for _, itB := range itemsB {
		itA, ok := byTier1A[itB.Tier1]
		if !ok {
			t.Fatalf("section %q present in rootB's items but not rootA's", itB.Tier1)
		}
		if itA.ID != itB.ID {
			t.Fatalf("section %q got id %s from rootA (%s) but %s from rootB (%s) -- same section, different checkout, different id",
				itB.Tier1, itA.ID, rootA, itB.ID, rootB)
		}
		if itA.Permalink != itB.Permalink {
			t.Fatalf("section %q got permalink %q from rootA but %q from rootB -- permalink must not embed the checkout root",
				itB.Tier1, itA.Permalink, itB.Permalink)
		}
		if filepath.IsAbs(itB.Permalink) {
			t.Fatalf("permalink %q is an absolute path -- must be repo-relative", itB.Permalink)
		}
	}
}

// TestRepoDocsSourceSplitsScoredTier1FromDisplayedTier1 is agent-estate
// #1113's own regression fixture: Tier1 (displayed) stays the full
// ancestor path, Tier1Scored carries the section's own leaf heading only,
// and Tier1AncestorScored carries the dropped ancestor text -- so the
// scorer and the reader see different things by construction, not by
// accident.
func TestRepoDocsSourceSplitsScoredTier1FromDisplayedTier1(t *testing.T) {
	dir := writeFixtureRepo(t)
	_, items := repoDocsSource(dir)

	var conventions Item
	found := false
	for _, it := range items {
		if it.Tier1 == "fixture-repo — agent orientation — The daemon — Conventions" {
			conventions = it
			found = true
		}
	}
	if !found {
		t.Fatalf("no item with the daemon's own \"Conventions\" heading path: %+v", items)
	}
	if conventions.Tier1Scored != "Conventions" {
		t.Errorf("Tier1Scored = %q, want the leaf heading only (\"Conventions\")", conventions.Tier1Scored)
	}
	if conventions.Tier1AncestorScored != "fixture-repo — agent orientation — The daemon" {
		t.Errorf("Tier1AncestorScored = %q, want every heading above the leaf", conventions.Tier1AncestorScored)
	}
}

func TestFindRepoRootReturnsEmptyWhenNoMarkerExists(t *testing.T) {
	if got := findRepoRoot(t.TempDir()); got != "" {
		t.Fatalf("findRepoRoot() = %q, want \"\" (no AGENTS.md anywhere above a bare temp dir)", got)
	}
}
