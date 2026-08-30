package reposcan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot resolves to this package's own scan root, not the git toplevel.
// Before agent-estate#744's Step 2c merge, this package's git toplevel WAS
// its scan root -- agent-tui was its own repository. Post-merge, `git
// rev-parse --show-toplevel` resolves to the merged agent-estate root, one
// level above where this package (and the testdata this guard has always
// scanned) actually lives. Anchoring at "<toplevel>/tui" restored the exact
// pre-merge scope: same manifest, same allowlist, same tracked-file set,
// just resolved from underneath a directory this package didn't have before.
// agent-estate#865 moved the Go tree again, from "<toplevel>/tui" to
// "<toplevel>/src/tui"; the anchor moves with it for the same reason.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "src", "tui")
}

// TestBareIssueReferencesResolveInThisRepository is the actual guard: every
// bare '#N' citation in a tracked *.md file must resolve to a real issue or
// PR number in RepoSlug, or be explicitly allowlisted with a reason.
func TestBareIssueReferencesResolveInThisRepository(t *testing.T) {
	root := repoRoot(t)
	manifest := filepath.Join(root, "internal", "reposcan", "testdata", "known_references.txt")
	allowlist := filepath.Join(root, "internal", "reposcan", "testdata", "reference_guard_allowlist.json")

	violations, err := ScanRepository(root, manifest, allowlist)
	if err != nil {
		t.Fatalf("ScanRepository: %v", err)
	}
	if len(violations) > 0 {
		msgs := make([]string, len(violations))
		for i, v := range violations {
			msgs[i] = v.String()
		}
		t.Fatalf(
			"bare issue/PR reference(s) that do not resolve in %s: %s. "+
				"Fix: if this cites another repository, qualify it as "+
				"'owner/repo#N'. If the number should exist here but is "+
				"missing from the manifest, run "+
				"scripts/refresh-known-references.sh. If it is a "+
				"legitimate historical citation to a number that no "+
				"longer resolves, add it to "+
				"internal/reposcan/testdata/reference_guard_allowlist.json "+
				"with a reason.",
			RepoSlug, strings.Join(msgs, "; "),
		)
	}
}

// --- Mutation checks: prove the guard actually fires, in both directions.
//
// "A guard nobody has watched refuse anything is not a guard" -- these
// don't scan this repository's real docs; they feed FindBareReferenceViolations
// synthetic content directly so the guard's own pass/fail behavior stays
// under test, independent of whatever this repository's docs currently say.

func TestBareReferenceGuardFiresOnUnresolvedBareNumber(t *testing.T) {
	known := map[int]bool{1: true}
	allowed := map[int]bool{}
	text := fmt.Sprintf("See #%d for context.", 999999)

	got := FindBareReferenceViolations("fixture.md", text, known, allowed)

	if len(got) != 1 {
		t.Fatalf("expected exactly one violation for an unresolved bare reference, got %d: %v", len(got), got)
	}
	if got[0].Ref != "#999999" {
		t.Fatalf("expected violation for #999999, got %q", got[0].Ref)
	}
}

func TestBareReferenceGuardPassesOnQualifiedReference(t *testing.T) {
	known := map[int]bool{} // deliberately empty: 999999 is not "known" here at all
	allowed := map[int]bool{}
	text := "See jonhill90/agent-supervisor#999999 for context."

	got := FindBareReferenceViolations("fixture.md", text, known, allowed)

	if len(got) != 0 {
		t.Fatalf("expected a fully-qualified owner/repo#N reference to pass untouched, got violations: %v", got)
	}
}

func TestBareReferenceGuardPassesOnKnownBareNumber(t *testing.T) {
	known := map[int]bool{202: true}
	allowed := map[int]bool{}
	text := "Fixed by #202."

	got := FindBareReferenceViolations("fixture.md", text, known, allowed)

	if len(got) != 0 {
		t.Fatalf("expected a bare reference to a known number to pass, got violations: %v", got)
	}
}

func TestBareReferenceGuardPassesOnAllowlistedNumber(t *testing.T) {
	known := map[int]bool{}
	allowed := map[int]bool{9999: true}
	text := "Historically #9999, since deleted."

	got := FindBareReferenceViolations("fixture.md", text, known, allowed)

	if len(got) != 0 {
		t.Fatalf("expected an allowlisted bare reference to pass, got violations: %v", got)
	}
}

func TestBareReferenceGuardIgnoresCodeSpans(t *testing.T) {
	known := map[int]bool{}
	allowed := map[int]bool{}
	text := "Example syntax: `#999999` is not a real citation."

	got := FindBareReferenceViolations("fixture.md", text, known, allowed)

	if len(got) != 0 {
		t.Fatalf("expected a reference inside a code span to be ignored, got violations: %v", got)
	}
}

// --- agent-tui#157: the Go-comment extension. extractGoComments must scan
// comment text and ONLY comment text -- a bare '#N' inside a Go string
// literal is routinely not a citation at all (a rendered UI marker,
// board's own "#1 " card-number prefix; a hex colour in a JSON fixture),
// and flagging those would make the guard un-runnable in this repository.

func TestExtractGoCommentsFindsBareReferenceInLineComment(t *testing.T) {
	src := []byte(`package p

// fixed by #999999, not yet qualified
func f() {}
`)
	comments, err := extractGoComments(src)
	if err != nil {
		t.Fatalf("extractGoComments: %v", err)
	}
	got := FindBareReferenceViolations("fixture.go", comments, map[int]bool{}, map[int]bool{})
	if len(got) != 1 || got[0].Ref != "#999999" {
		t.Fatalf("expected exactly one violation for the line-comment reference, got %v", got)
	}
	if got[0].Line != 3 {
		t.Fatalf("expected the violation on line 3 (comment line, positions preserved), got line %d", got[0].Line)
	}
}

func TestExtractGoCommentsFindsBareReferenceInBlockComment(t *testing.T) {
	src := []byte(`package p

/*
See #999999 for context.
*/
func f() {}
`)
	comments, err := extractGoComments(src)
	if err != nil {
		t.Fatalf("extractGoComments: %v", err)
	}
	got := FindBareReferenceViolations("fixture.go", comments, map[int]bool{}, map[int]bool{})
	if len(got) != 1 || got[0].Ref != "#999999" {
		t.Fatalf("expected exactly one violation inside the block comment, got %v", got)
	}
}

// TestExtractGoCommentsIgnoresStringLiterals is the regression this
// extension exists to prevent: internal/board's own tests assert against a
// REAL running Program's rendered output, which legitimately contains bare
// '#N' card-number markers (view.go's fmt.Sprintf("#%d", ...)) -- those are
// not citations and scanning string literals for them would make the guard
// fire on the application's own correct behaviour, not a defect.
func TestExtractGoCommentsIgnoresStringLiterals(t *testing.T) {
	src := []byte(`package p

func f() string {
	// this comment is fine, cites nothing
	return "card #999999 rendered"
}
`)
	comments, err := extractGoComments(src)
	if err != nil {
		t.Fatalf("extractGoComments: %v", err)
	}
	got := FindBareReferenceViolations("fixture.go", comments, map[int]bool{}, map[int]bool{})
	if len(got) != 0 {
		t.Fatalf("expected the string literal's bare reference to be ignored, got violations: %v", got)
	}
}

func TestExtractGoCommentsPreservesLineNumbersAroundMultiLineStrings(t *testing.T) {
	src := []byte(`package p

func f() string {
	return "line one #999999" +
		"line two"
}

// #999999 real citation, three lines below the multi-line expression above
func g() {}
`)
	comments, err := extractGoComments(src)
	if err != nil {
		t.Fatalf("extractGoComments: %v", err)
	}
	got := FindBareReferenceViolations("fixture.go", comments, map[int]bool{}, map[int]bool{})
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation (the comment, not either string literal), got %v", got)
	}
	if got[0].Line != 8 {
		t.Fatalf("expected the violation on line 8, got line %d -- line numbers drifted", got[0].Line)
	}
}

// TestScanRepositoryScansGoCommentsToo is the wiring check: ScanRepository
// itself, not just extractGoComments in isolation, must reach *.go files.
// Uses this repository's own real manifest/allowlist against a scratch
// checkout so a genuinely unresolved bare reference in a Go comment is
// caught end to end.
func TestScanRepositoryScansGoCommentsToo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "known.txt"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "allow.json"), []byte(`{"allowed":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	goSrc := "package p\n\n// unresolved reference #999999\nfunc f() {}\n"
	if err := os.WriteFile(filepath.Join(root, "f.go"), []byte(goSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	got, err := ScanRepository(root, filepath.Join(root, "known.txt"), filepath.Join(root, "allow.json"))
	if err != nil {
		t.Fatalf("ScanRepository: %v", err)
	}
	if len(got) != 1 || got[0].Path != "f.go" || got[0].Ref != "#999999" {
		t.Fatalf("expected ScanRepository to catch the Go-comment reference, got %v", got)
	}
}
