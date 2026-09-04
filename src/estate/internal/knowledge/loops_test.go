package knowledge

import (
	"os"
	"path/filepath"
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
