package reposcan

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
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
