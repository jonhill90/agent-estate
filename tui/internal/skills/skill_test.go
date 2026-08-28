package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, dir, name, frontmatter string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(frontmatter), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestScan_ReadsNameAndDescriptionFromEachSkillDir(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "---\nname: alpha\ndescription: does alpha things\n---\n\nbody\n")
	writeSkill(t, dir, "beta", "---\nname: beta\ndescription: does beta things\n---\n\nbody\n")

	got, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d skills, want 2: %+v", len(got), got)
	}
	if got[0].Dir != "alpha" || got[0].Name != "alpha" || got[0].Description != "does alpha things" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Dir != "beta" || got[1].Name != "beta" || got[1].Description != "does beta things" {
		t.Errorf("got[1] = %+v", got[1])
	}
}

// TestScan_LastEvalAndInvocationCountAreAlwaysNil pins the absence
// discipline this package's own doc comment states -- a caller must never
// see a fabricated "0" for a metric this estate has no source for.
func TestScan_LastEvalAndInvocationCountAreAlwaysNil(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "---\nname: alpha\ndescription: x\n---\n")

	got, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got[0].LastEval != nil {
		t.Errorf("LastEval = %v, want nil", *got[0].LastEval)
	}
	if got[0].InvocationCount != nil {
		t.Errorf("InvocationCount = %v, want nil", *got[0].InvocationCount)
	}
}

// TestScan_SkipsEntriesWithNoSkillMD matches this estate's real layout --
// a README.md or any other file sitting next to skill directories is not
// itself a skill and must not appear as a broken one.
func TestScan_SkipsEntriesWithNoSkillMD(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "---\nname: alpha\ndescription: x\n---\n")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a skill"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty-dir"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	got, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 || got[0].Dir != "alpha" {
		t.Fatalf("got %+v, want exactly [alpha]", got)
	}
}

// TestScan_MalformedFrontmatterStillProducesASkill -- a broken SKILL.md
// must be visible in the list (keyed by Dir, ParseErr set), never silently
// dropped the way a missing SKILL.md is (this package's own doc comment on
// Skill.ParseErr).
func TestScan_MalformedFrontmatterStillProducesASkill(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "broken", "not frontmatter at all\n")

	got, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d skills, want 1: %+v", len(got), got)
	}
	if got[0].Dir != "broken" {
		t.Errorf("Dir = %q, want %q", got[0].Dir, "broken")
	}
	if got[0].ParseErr == "" {
		t.Errorf("ParseErr empty, want a non-empty reason")
	}
}

func TestScan_MissingDirIsARealError(t *testing.T) {
	_, err := Scan(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("Scan on a missing dir returned nil error, want a real one -- \"could not look\" must not read as \"looked, found nothing\"")
	}
}

func TestScanFetcher_WrapsScan(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "---\nname: alpha\ndescription: x\n---\n")

	fetch := ScanFetcher(dir)
	got, err := fetch()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseFrontmatter_NoNameFieldIsAnError(t *testing.T) {
	_, _, perr := parseFrontmatter("---\ndescription: x\n---\n")
	if perr == "" {
		t.Error("expected a non-empty parse error for missing name: field")
	}
}

func TestParseFrontmatter_NeverClosedIsAnError(t *testing.T) {
	_, _, perr := parseFrontmatter("---\nname: x\ndescription: y\n")
	if perr == "" {
		t.Error("expected a non-empty parse error for an unclosed frontmatter fence")
	}
}

func TestParseFrontmatter_NoOpeningFenceIsAnError(t *testing.T) {
	_, _, perr := parseFrontmatter("name: x\ndescription: y\n")
	if perr == "" {
		t.Error("expected a non-empty parse error when the file doesn't start with ---")
	}
}
