package knowledge

import (
	"os"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge/goldenset"
)

// TestEveryRepoDocsFileHasANaturalCase closes agent-estate#1176: 12 of 12
// repo-docs files having a natural-language case (agent-estate#1169) was
// true on fdb27f3 and enforced by nothing -- the four existing tests in
// goldenset/natural_cases_test.go all check properties of the cases
// (parse, question+rationale present, all repo-docs, ids distinct from
// cases.json); none of them ever looks at the file list itself. Add a
// 13th document under docs/ and coverage silently drops to 12 of 13 with
// every one of those four tests still green.
//
// This test derives the repo-docs file set the SAME WAY the indexer does
// -- by calling repoDocsSource against this checkout's own repo root,
// exactly as docs.go's real caller (generate.go) does -- rather than
// hardcoding a list of filenames. A hardcoded list is the identical drift
// defect one level up: it would need a human to remember to extend it
// every time a file is added, which is the exact failure this test
// exists to remove. It then reads every ExpectedIdentifier out of
// goldenset.LoadNatural() (agent-estate#1073's own stratum, already
// asserted repo-docs-only by TestNaturalCasesAreAllRepoDocs) and checks
// that every indexed file's relative path is the prefix of at least one
// case's identifier -- goldenset.go's own doc comment on ExpectedIdentifier
// records that a repo-docs case's identifier is always "<relative
// path>#<anchor>", so a prefix match before the "#" is the file, not a
// guess at the format.
//
// DECISION (agent-estate#1176 asks for one, argued here, not just in the
// PR body): this test FAILS the build on any uncovered file, strict and
// mechanical, rather than merely logging a report. The alternative --
// report without failing -- was rejected because this repo has already
// recorded, in its own AGENTS.md ("the failure mode this codebase
// produces most" / "two more, learned expensively"), that "a tool that
// fails closed and that nothing calls is a documentation rule with a
// binary attached": a coverage check that runs in `go test ./...` but
// only prints is exactly that shape, and it is the reason
// agent-estate#1158 exists at all. The cost accepted by choosing "fail"
// is real and stated plainly: this test fires on the *next* person who
// adds any file under docs/ (or edits AGENTS.md's own path), possibly
// mid-task and unrelated to what they were doing, and they must add a
// natural-language case (or a documented exception) before `go test
// ./...` goes green again. That cost is accepted because the alternative
// -- a guard nobody has ever seen fail -- is worse than an occasional
// unrelated red build; agent-estate#1176's own text makes the same
// point: "a guard nobody has seen fail is a guard nobody knows works."
//
// This test does NOT require a case per section (118 sections exist;
// agent-estate#1140 and agent-estate#1138 both had to re-author
// carelessly-written per-section cases, so "more cases" is not the goal
// here) and it does not add or edit any case itself -- if it fails
// against the current tree, that is agent-estate#1176's own finding to
// report, not something for this change to fix by authoring a case.
func TestEveryRepoDocsFileHasANaturalCase(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := findRepoRoot(wd)
	if repoRoot == "" {
		t.Fatal("findRepoRoot() found no AGENTS.md above this package -- cannot derive the repo-docs file set")
	}

	res, items := repoDocsSource(repoRoot)
	if !res.OK {
		t.Fatalf("repoDocsSource(%q) failed: %+v", repoRoot, res)
	}

	indexedFiles := map[string]bool{}
	for _, it := range items {
		// Permalink is "<relative path>#<anchor>" (docs.go); the file is
		// everything before the first "#".
		rel, _, _ := strings.Cut(it.Permalink, "#")
		indexedFiles[rel] = true
	}
	if len(indexedFiles) == 0 {
		t.Fatal("repoDocsSource returned no items against this checkout -- cannot exercise coverage")
	}

	natural, err := goldenset.LoadNatural()
	if err != nil {
		t.Fatal(err)
	}
	coveredFiles := map[string]bool{}
	for _, c := range natural {
		if c.ExpectedSource != goldenset.SourceRepoDocs {
			continue // TestNaturalCasesAreAllRepoDocs already guards this stratum is repo-docs-only
		}
		rel, _, _ := strings.Cut(c.ExpectedIdentifier, "#")
		coveredFiles[rel] = true
	}

	var uncovered []string
	for rel := range indexedFiles {
		if !coveredFiles[rel] {
			uncovered = append(uncovered, rel)
		}
	}
	if len(uncovered) > 0 {
		t.Errorf("repo-docs file(s) with no natural-language case in goldenset/natural_cases.json: %v -- add a case whose expected_identifier targets one of this file's own sections (agent-estate#1176)", uncovered)
	}
}
