package main

import "path/filepath"

// skillsEvalStatusRelPath is where jonhill90/skills keeps skills#230's own
// eval-status.json, relative to that repo's root.
const skillsEvalStatusRelPath = "docs/eval-status.json"

// resolveSkillsEvalStatus turns -skills-repo/$AGENT_TUI_SKILLS_REPO into a
// path to eval-status.json, mirroring resolveOpenAPISpec's own shape
// (docs.go): repo == "" resolves to "", the same "not configured" state
// skills.EvalStatusFetcher already degrades on without erroring
// (agent-tui#151's own scope line -- "degrade to today's honest
// 'unevaluated' when the file is absent rather than erroring"). Unlike
// resolveOpenAPISpec this never needs to distinguish "typo'd an explicit
// path" from "never set one": there is no separate -skills-eval-status
// flag, only -skills-repo, so any non-empty result already came from a
// checkout path the caller gave.
func resolveSkillsEvalStatus(repo string) string {
	if repo == "" {
		return ""
	}
	return filepath.Join(repo, skillsEvalStatusRelPath)
}
