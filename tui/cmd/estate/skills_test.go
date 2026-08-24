package main

import (
	"path/filepath"
	"testing"
)

// TestResolveSkillsEvalStatusEmptyWithNoRepo is the state a machine with no
// jonhill90/skills checkout is in -- resolveSkillsEvalStatus must return ""
// so skills.EvalStatusFetcher degrades to today's honest "unevaluated"
// rather than being handed a bogus path (agent-tui#151).
func TestResolveSkillsEvalStatusEmptyWithNoRepo(t *testing.T) {
	if got := resolveSkillsEvalStatus(""); got != "" {
		t.Errorf("resolveSkillsEvalStatus(\"\") = %q, want empty", got)
	}
}

// TestResolveSkillsEvalStatusJoinsTheRepoLayout mirrors
// TestResolveOpenAPISpecFallsBackToTheRepoLayout's own shape (docs_test.go)
// -- a configured -skills-repo resolves to that repo's own
// docs/eval-status.json, whether or not the file exists yet.
func TestResolveSkillsEvalStatusJoinsTheRepoLayout(t *testing.T) {
	repo := "/some/skills-repo"
	want := filepath.Join(repo, skillsEvalStatusRelPath)
	if got := resolveSkillsEvalStatus(repo); got != want {
		t.Errorf("resolveSkillsEvalStatus(%q) = %q, want %q", repo, got, want)
	}
}
