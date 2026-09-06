package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoopsSourceReadsHeadingAndParagraph(t *testing.T) {
	dir := t.TempDir()
	content := "# 00 -- The landscape\n\n## Timeline\n\nSome real paragraph text here.\n"
	if err := os.WriteFile(filepath.Join(dir, "00-landscape.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	res, items := loopsSource(dir)
	if !res.OK || res.Count != 1 {
		t.Fatalf("loopsSource() result = %+v", res)
	}
	if items[0].Tier1 != "00 -- The landscape" {
		t.Errorf("Tier1 = %q", items[0].Tier1)
	}
	if items[0].Tier2 != "Some real paragraph text here." {
		t.Errorf("Tier2 = %q", items[0].Tier2)
	}
}

func TestLoopsSourceMissingDirIsHonest(t *testing.T) {
	res, items := loopsSource(filepath.Join(t.TempDir(), "does-not-exist"))
	if res.OK {
		t.Fatal("loopsSource() reported OK for a missing directory")
	}
	if res.Reason == "" {
		t.Fatal("loopsSource() gave no reason")
	}
	if items != nil {
		t.Fatal("loopsSource() returned items for a missing directory")
	}
}

func TestLoopsSourceIgnoresNonMarkdownFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\npara\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := loopsSource(dir)
	if res.Count != 1 {
		t.Fatalf("loopsSource() count = %d, want 1 (non-.md file must be ignored)", res.Count)
	}
}

// TestLoopsSourceTier3DeepensPastTier2 is agent-estate#1139 defect B's own
// acceptance test: Tier2 is only the note's first paragraph, truncated to
// 400 characters -- Tier3 must carry the note's ENTIRE content, materially
// more than that one paragraph, not a bare "open <path>" pointer. FAILS
// against the reverted pointer-string behaviour (Tier3 shorter than Tier2)
// and PASSES against loopsTier3's full-file rendering.
func TestLoopsSourceTier3DeepensPastTier2(t *testing.T) {
	dir := t.TempDir()
	content := "# 00 -- The landscape\n\n## Timeline\n\nSome real paragraph text here.\n\n" +
		"## Second section\n\nA second paragraph with more distinctive material: quixotropic.\n"
	if err := os.WriteFile(filepath.Join(dir, "00-landscape.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, items := loopsSource(dir)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if len(it.Tier3) <= len(it.Tier2) {
		t.Fatalf("Tier3 (%d bytes) is not longer than Tier2 (%d bytes) -- tier3=%q tier2=%q",
			len(it.Tier3), len(it.Tier2), it.Tier3, it.Tier2)
	}
	if !strings.Contains(it.Tier3, "quixotropic") {
		t.Errorf("Tier3 = %q, want it to carry the second section's own text -- Tier2 only carries the first paragraph", it.Tier3)
	}
}
