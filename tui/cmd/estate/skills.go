package main

import (
	"path/filepath"

	"github.com/jonhill90/agent-estate/tui/internal/skills"
)

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

// resolveInvocationsCache turns -skill-invocations-cache/
// $AGENT_TUI_SKILL_INVOCATIONS_CACHE into the path skills.InvocationFetcher
// reads (agent-tui#174). Unlike resolveSkillsEvalStatus, an explicit ""
// here does NOT mean "not configured" -- it means "use this machine's own
// default location" (skills.DefaultInvocationCachePath()), because unlike
// the skills-repo eval store there is nothing to point at except a
// checkout the caller must have; the invocation cache is private,
// per-machine state this binary itself owns the location of. If even the
// default cannot be resolved (no $HOME), "" is returned and
// InvocationFetcher treats that as InvocationsStoreUnreadable for every
// skill -- there being nowhere to read from is a "could not look" fact.
func resolveInvocationsCache(explicit string) string {
	if explicit != "" {
		return explicit
	}
	path, err := skills.DefaultInvocationCachePath()
	if err != nil {
		return ""
	}
	return path
}
