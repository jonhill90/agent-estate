package reposcan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNamingDebtDoesNotIncreaseOnMain is the acceptance test named by
// agent-estate#768 item 4's brief: the pinned baseline must already cover
// every current hill90-*/agent-supervisor reference, with zero net-new
// violations, on this branch exactly as it stands. If this fails, either the
// baseline is stale (regenerate with
// scripts/tui/refresh-naming-debt-baseline.sh, after confirming every
// newly-recorded reference really is pre-existing debt and not something
// this change itself introduced) or this change introduced real new debt.
func TestNamingDebtDoesNotIncreaseOnMain(t *testing.T) {
	root := repoRoot(t)
	baseline := filepath.Join(root, "internal", "reposcan", "testdata", "naming_debt_baseline.json")

	violations, err := CheckNamingDebtRatchet(root, baseline)
	if err != nil {
		t.Fatalf("CheckNamingDebtRatchet: %v", err)
	}
	if len(violations) > 0 {
		msgs := make([]string, len(violations))
		for i, v := range violations {
			msgs[i] = v.String()
		}
		t.Fatalf(
			"new hill90-*/agent-supervisor naming reference(s) beyond the pinned baseline "+
				"(agent-estate#768 item 4): %s. Use agent-estate or the tenant's own name "+
				"instead. A genuine historical citation can be exempted inline with the "+
				"marker %q; anything else, don't add it.",
			strings.Join(msgs, "; "), NamingDebtEscapeHatch,
		)
	}
}

// --- Mutation checks: prove the guard actually fires, in both directions,
// against synthetic content -- same discipline as
// cross_repo_references_test.go's own mutation checks.

func TestNamingDebtGuardFiresOnTenantAsEstateIdentity(t *testing.T) {
	got := FindNamingDebtLines("fresh.md", "State now lives under hill90-supervisor.")
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %d: %v", len(got), got)
	}
}

func TestNamingDebtGuardFiresOnCodexVariant(t *testing.T) {
	got := FindNamingDebtLines("fresh.md", "The codex loop still writes to hill90-codex-supervisor.")
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %d: %v", len(got), got)
	}
}

func TestNamingDebtGuardPassesAfterRenamingToEstate(t *testing.T) {
	got := FindNamingDebtLines("fresh.md", "State now lives under agent-estate.")
	if len(got) != 0 {
		t.Fatalf("expected no violations after the rename, got %v", got)
	}
}

func TestNamingDebtGuardFiresOnUnqualifiedDeadRepoSlug(t *testing.T) {
	got := FindNamingDebtLines("fresh.md", "See agent-supervisor for the old scripts.")
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %d: %v", len(got), got)
	}
}

func TestNamingDebtGuardPassesOnQualifiedDeadRepoSlug(t *testing.T) {
	got := FindNamingDebtLines("fresh.md", "See jonhill90/agent-supervisor#100 for the pre-rename history.")
	if len(got) != 0 {
		t.Fatalf("expected a qualified historical pointer to pass, got %v", got)
	}
}

func TestNamingDebtGuardPassesOnTenantsOwnNames(t *testing.T) {
	for _, text := range []string{
		"The app lives in hill90-app.",
		"Its docs repo is hill90-docs.",
		"Set HILL90_APP_REPO to point at it.",
		"hill90-ui and hill90-web are the tenant's own repos.",
		"hill90-boundary marks the line between estate and tenant code.",
	} {
		if got := FindNamingDebtLines("fresh.md", text); len(got) != 0 {
			t.Fatalf("expected the tenant's own reference %q to pass untouched, got %v", text, got)
		}
	}
}

func TestNamingDebtGuardHonoursEscapeHatch(t *testing.T) {
	text := "This estate used to be called agent-supervisor. naming-guard:historical"
	if got := FindNamingDebtLines("fresh.md", text); len(got) != 0 {
		t.Fatalf("expected the escape-hatch marker to exempt the line, got %v", got)
	}
}

func TestNamingDebtGuardIgnoresCodeSpans(t *testing.T) {
	text := "Example config key: `hill90-supervisor` is not a real citation here."
	if got := FindNamingDebtLines("fresh.md", text); len(got) != 0 {
		t.Fatalf("expected a reference inside a code span to be ignored, got %v", got)
	}
}

// --- Ratchet wiring: prove CheckNamingDebtRatchet itself fires and clears,
// against a scratch git checkout, not just the line-level helper above.

func writeScratchGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
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
	return root
}

func TestNamingDebtRatchetFailsOnNewOffendingReferenceInFreshFile(t *testing.T) {
	root := writeScratchGitRepo(t, map[string]string{
		"README.md": "Everything now runs under agent-estate.\n",
		"NOTES.md":  "New note: deploy target is hill90-supervisor:3.\n",
	})
	baseline := filepath.Join(root, "baseline.json")
	if err := os.WriteFile(baseline, []byte(`{"counts":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := CheckNamingDebtRatchet(root, baseline)
	if err != nil {
		t.Fatalf("CheckNamingDebtRatchet: %v", err)
	}
	if len(got) != 1 || got[0].Path != "NOTES.md" {
		t.Fatalf("expected exactly one violation on NOTES.md, got %v", got)
	}
}

func TestNamingDebtRatchetPassesWhenOffendingReferenceIsRemoved(t *testing.T) {
	root := writeScratchGitRepo(t, map[string]string{
		"README.md": "Everything now runs under agent-estate.\n",
	})
	baseline := filepath.Join(root, "baseline.json")
	if err := os.WriteFile(baseline, []byte(`{"counts":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := CheckNamingDebtRatchet(root, baseline)
	if err != nil {
		t.Fatalf("CheckNamingDebtRatchet: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no violations once the offending reference is gone, got %v", got)
	}
}

func TestNamingDebtRatchetPassesWhenBaselineCoversExistingCount(t *testing.T) {
	root := writeScratchGitRepo(t, map[string]string{
		"LEGACY.md": "This repository used to be agent-supervisor before the rename.\n",
	})
	baseline := filepath.Join(root, "baseline.json")
	if err := os.WriteFile(baseline, []byte(`{"counts":{"LEGACY.md":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := CheckNamingDebtRatchet(root, baseline)
	if err != nil {
		t.Fatalf("CheckNamingDebtRatchet: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected the baseline-covered existing reference to pass, got %v", got)
	}
}

func TestNamingDebtRatchetFailsWhenExistingFileGrowsPastBaseline(t *testing.T) {
	root := writeScratchGitRepo(t, map[string]string{
		"LEGACY.md": "This repository used to be agent-supervisor before the rename.\n" +
			"A second, new mention of agent-supervisor just landed here.\n",
	})
	baseline := filepath.Join(root, "baseline.json")
	if err := os.WriteFile(baseline, []byte(`{"counts":{"LEGACY.md":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := CheckNamingDebtRatchet(root, baseline)
	if err != nil {
		t.Fatalf("CheckNamingDebtRatchet: %v", err)
	}
	if len(got) != 1 || got[0].Path != "LEGACY.md" || got[0].Current != 2 || got[0].Baseline != 1 {
		t.Fatalf("expected LEGACY.md to fail with current=2 baseline=1, got %v", got)
	}
}

func TestNamingDebtCountsScansGoCommentsToo(t *testing.T) {
	root := writeScratchGitRepo(t, map[string]string{
		"f.go": "package p\n\n// state dir is hill90-supervisor, unqualified\nfunc f() {}\n",
	})

	counts, err := NamingDebtCounts(root)
	if err != nil {
		t.Fatalf("NamingDebtCounts: %v", err)
	}
	if counts["f.go"] != 1 {
		t.Fatalf("expected f.go to carry exactly one violation, got %v", counts)
	}
}
