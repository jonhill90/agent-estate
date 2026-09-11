package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkillsRegistry writes lines (already-JSON-encoded, one per line)
// to <repoRoot>/docs/skills-registry.jsonl -- the same
// write-a-fixture-file-then-read-it-back pattern every other source's
// test in this package already uses (writeVaultFact, etc.).
func writeSkillsRegistry(t *testing.T, repoRoot string, lines ...string) {
	t.Helper()
	dir := filepath.Join(repoRoot, "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "skills-registry.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsSourceReadsOneEntry(t *testing.T) {
	root := t.TempDir()
	writeSkillsRegistry(t, root,
		`{"name":"excel-automation","description":"office skills for xlsx","triggers":["excel","spreadsheet"],"repo":"claude-office-skills/skills","path":"excel-automation","revision":"9c4c7d5","last_checked_at":"2026-09-11T01:06:24Z","status":"candidate","status_reason":"not yet evaluated"}`)

	res, items := skillsSource(root)
	if !res.OK || res.Count != 1 {
		t.Fatalf("skillsSource() result = %+v", res)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.Source != "skill-registry" {
		t.Errorf("Source = %q, want skill-registry", it.Source)
	}
	if it.Tier1 != "excel-automation -- office skills for xlsx" {
		t.Errorf("Tier1 = %q", it.Tier1)
	}
	wantPermalink := "https://skills.sh/claude-office-skills/skills/excel-automation"
	if it.Permalink != wantPermalink {
		t.Errorf("Permalink = %q, want %q", it.Permalink, wantPermalink)
	}
	if !strings.Contains(it.Tier2, "triggers: excel, spreadsheet") {
		t.Errorf("Tier2 = %q, want triggers listed", it.Tier2)
	}
	if !strings.Contains(it.Tier2, "last checked 2026-09-11T01:06:24Z") {
		t.Errorf("Tier2 = %q, want the freshness line", it.Tier2)
	}
	if !containsTag(it.StructuralTags, "status:candidate") {
		t.Errorf("StructuralTags = %v, want status:candidate", it.StructuralTags)
	}
	if len(it.SynapticTags) != 2 || it.SynapticTags[0] != "#excel" {
		t.Errorf("SynapticTags = %v, want [#excel #spreadsheet]", it.SynapticTags)
	}
}

func containsTag(tags []string, want string) bool {
	for _, tg := range tags {
		if tg == want {
			return true
		}
	}
	return false
}

// TestSkillsSourceMissingFileIsHonestlyEmpty is skillsSource's own
// honest-absence discipline: a registry that has never been created yet
// (a freshly cloned checkout, before any skill has ever been recorded)
// is a legitimate empty state, not a source failure -- unlike a repoRoot
// that could not be resolved at all. See skillsSource's own doc comment.
func TestSkillsSourceMissingFileIsHonestlyEmpty(t *testing.T) {
	root := t.TempDir() // docs/skills-registry.jsonl deliberately not created
	res, items := skillsSource(root)
	if !res.OK {
		t.Fatalf("skillsSource() with no registry file = %+v, want OK=true (empty, not a failure)", res)
	}
	if res.Count != 0 || items != nil {
		t.Fatalf("skillsSource() with no registry file returned items: %+v", items)
	}
}

func TestSkillsSourceUnresolvedRepoRootIsHonestFailure(t *testing.T) {
	res, items := skillsSource("")
	if res.OK {
		t.Fatal("skillsSource(\"\") reported OK for an unresolved repo root")
	}
	if res.Reason == "" {
		t.Fatal("skillsSource(\"\") gave no reason")
	}
	if items != nil {
		t.Fatal("skillsSource(\"\") returned items for an unresolved repo root")
	}
}

// TestSkillsSourceNonexistentRepoRootIsAlsoAFailure pins the distinction
// TestSkillsSourceMissingFileIsHonestlyEmpty relies on: a repoRoot that is
// a non-empty string but does not actually exist on disk (a misconfigured
// $ESTATE_REPO_ROOT, or a checkout removed after resolution) must fail
// exactly like repoRoot=="" -- it must NOT be mistaken for "a real
// checkout with no registry entries recorded yet," which is the one case
// this source treats as honest-empty rather than a failure.
func TestSkillsSourceNonexistentRepoRootIsAlsoAFailure(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "this-directory-was-never-created")
	res, items := skillsSource(bogus)
	if res.OK {
		t.Fatalf("skillsSource(%q) reported OK=true for a repoRoot that does not exist", bogus)
	}
	if res.Reason == "" {
		t.Fatal("skillsSource() gave no reason for a nonexistent repo root")
	}
	if items != nil {
		t.Fatal("skillsSource() returned items for a nonexistent repo root")
	}
}

// TestSkillsSourceMalformedLineFailsTheWholeSource is deliberate:
// docs/skills-registry.jsonl is hand-curated and committed by this
// repo's own conventions, not live external data -- a malformed row
// means the registry itself needs a fix, so this fails the whole source
// rather than silently skipping one row (unlike, e.g., a single
// unreadable Loops-Research note).
func TestSkillsSourceMalformedLineFailsTheWholeSource(t *testing.T) {
	root := t.TempDir()
	writeSkillsRegistry(t, root, `{not valid json`)

	res, items := skillsSource(root)
	if res.OK {
		t.Fatal("skillsSource() reported OK for a malformed line")
	}
	if !strings.Contains(res.Reason, "line 1") {
		t.Errorf("Reason = %q, want it to name the offending line", res.Reason)
	}
	if items != nil {
		t.Fatal("skillsSource() returned items alongside a malformed-line failure")
	}
}

func TestSkillsSourceUnknownStatusFailsTheWholeSource(t *testing.T) {
	root := t.TempDir()
	writeSkillsRegistry(t, root,
		`{"name":"x","repo":"a/b","status":"maybe-someday"}`)

	res, _ := skillsSource(root)
	if res.OK {
		t.Fatal("skillsSource() reported OK for an unknown status value")
	}
	if !strings.Contains(res.Reason, "maybe-someday") {
		t.Errorf("Reason = %q, want it to name the bad status", res.Reason)
	}
}

// TestSkillsSourceNeverInstallsAnything is the structural claim
// agent-estate#1021 asks this PR to argue, restated as code: skillsSource
// reads a file and returns Items -- no *exec.Cmd, no network call, no
// write, appears anywhere in its call graph. This test cannot prove a
// negative about the whole binary, but it does prove the one thing that
// would make an install possible from THIS function: RunGH/RunGit style
// injected runners exist elsewhere in this package specifically so tests
// never shell out for real (build_commit_test.go, stars_test.go); this
// source takes no such runner at all, because it has no seam to shell out
// through in the first place.
func TestSkillsSourceNeverInstallsAnything(t *testing.T) {
	root := t.TempDir()
	writeSkillsRegistry(t, root,
		`{"name":"x","description":"d","repo":"a/b","status":"adopted","status_reason":"already installed by hand, separately"}`)

	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	res, items := skillsSource(root)
	if !res.OK || len(items) != 1 {
		t.Fatalf("skillsSource() = %+v, %+v", res, items)
	}
	after, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("skillsSource() changed the directory it read: before=%v after=%v", before, after)
	}
}

func TestSkillTier3DeepensPastTier2(t *testing.T) {
	root := t.TempDir()
	writeSkillsRegistry(t, root,
		`{"name":"x","description":"short","repo":"a/b","path":"sub","revision":"deadbeef","last_checked_at":"2026-01-01T00:00:00Z","status":"rejected","status_reason":"asks for network access this repo does not grant"}`)

	_, items := skillsSource(root)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if len(it.Tier3) <= len(it.Tier2) {
		t.Fatalf("Tier3 (%d bytes) is not longer than Tier2 (%d bytes)", len(it.Tier3), len(it.Tier2))
	}
	if !strings.Contains(it.Tier3, "https://github.com/a/b (path: sub)") {
		t.Errorf("Tier3 = %q, want the upstream GitHub location", it.Tier3)
	}
	if !strings.Contains(it.Tier3, "revision: deadbeef") {
		t.Errorf("Tier3 = %q, want the revision", it.Tier3)
	}
}

// TestSkillStatusLineNamesAnUnexplainedRejection is agent-estate#1021's
// own point made concrete: a rejected entry is exactly as valuable as an
// adopted one, but only if the rejection is visible even when nobody has
// filled in a reason yet.
func TestSkillStatusLineNamesAnUnexplainedRejection(t *testing.T) {
	got := skillStatusLine(skillRegistryEntry{Status: "rejected"})
	if !strings.Contains(got, "rejected") || !strings.Contains(got, "no reason recorded") {
		t.Errorf("skillStatusLine() = %q, want it to name both the rejection and the missing reason", got)
	}
}

// TestSkillFreshnessLineNeverSilentlyOmitsAnUncheckedEntry is
// agent-estate#1021's "freshness is the part that will rot" ask, made
// concrete: LastCheckedAt=="" must render as an explicit claim, never as
// an absent line a reader could mistake for "checked, and it's fine."
func TestSkillFreshnessLineNeverSilentlyOmitsAnUncheckedEntry(t *testing.T) {
	got := skillFreshnessLine(skillRegistryEntry{})
	if !strings.Contains(got, "never checked") {
		t.Errorf("skillFreshnessLine(unset) = %q, want it to say never checked", got)
	}
}

// TestSkillPermalinkDisambiguatesTwoSkillsInOneRepo pins the reason
// skillPermalink includes Name, not just Repo: two entries whose Repo is
// identical but whose Name differs (a real, observed shape --
// sbroenne/mcp-server-excel hosts both excel-mcp and excel-cli) must
// produce two different ids, or the second silently overwrites the first
// in anything keyed by Item.ID.
func TestSkillPermalinkDisambiguatesTwoSkillsInOneRepo(t *testing.T) {
	root := t.TempDir()
	writeSkillsRegistry(t, root,
		`{"name":"excel-mcp","repo":"sbroenne/mcp-server-excel","status":"candidate"}`,
		`{"name":"excel-cli","repo":"sbroenne/mcp-server-excel","status":"candidate"}`)

	res, items := skillsSource(root)
	if !res.OK || res.Count != 2 {
		t.Fatalf("skillsSource() result = %+v", res)
	}
	if items[0].ID == items[1].ID {
		t.Fatalf("two skills in the same repo collided onto one id: %s", items[0].ID)
	}
	if items[0].Permalink == items[1].Permalink {
		t.Fatalf("two skills in the same repo collided onto one permalink: %s", items[0].Permalink)
	}
}
