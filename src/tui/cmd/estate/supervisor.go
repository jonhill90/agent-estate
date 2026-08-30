package main

import (
	"os"
	"path/filepath"
)

// fallbackSupervisorRepoNames are the checkout directory names tried, in
// order, under $HOME/source/repos/Personal when neither -supervisor-repo/
// $AGENT_SUPERVISOR_REPO nor a git-repo walk-up finds one -- Jon's own
// checkout, on the machine this ships to first. It is a fallback, not a
// requirement: discoverSupervisorRepo trying and failing here is one of
// three ordinary outcomes (the others being "found by walk-up" and "found
// nowhere, open degraded"), never a reason to exit.
//
// jonhill90/agent-supervisor#682 (Track A) queued that repo's rename to
// agent-estate; this list is the single place that edit lands -- add or
// reorder a name here, nothing else in this file changes. "agent-supervisor"
// stays first because the rename has not happened yet (per
// docs/merge-impact-inventory-agent-estate.md row 8: this resolution
// degrades loudly into agent-tui#49 item 1's own "supervisor repo not
// found" path today, so trying the current name first, then the future
// one, changes nothing about today's behavior and needs no code change
// once the rename lands).
var fallbackSupervisorRepoNames = []string{"agent-supervisor", "agent-estate"}

// hasSupervisorScript reports whether dir looks like an agent-supervisor
// checkout -- the same file connect() itself later stats before trying to
// run it (mcp_server.py), checked here first so discovery doesn't hand
// connect() a directory that merely LOOKS like a repo root.
func hasSupervisorScript(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "scripts", "supervisor", "mcp_server.py"))
	return err == nil
}

// discoverSupervisorRepo implements agent-tui#49 item 1's fix: a bare
// `estate` must never exit 1 just because -supervisor-repo was not typed.
// It walks up from start looking for a checkout containing
// scripts/supervisor/mcp_server.py (the file connect() itself requires) --
// that walk-up is already repo-name-agnostic, since it checks for the
// script file, never a directory name. It then falls back to each of
// fallbackSupervisorRepoNames under $HOME, in order. Returns "" -- not an
// error -- when none is found; main() opens in a degraded state rather
// than refusing to start, per the brief's "never exit 1 on a bare launch."
func discoverSupervisorRepo(start string) string {
	dir := start
	for {
		if hasSupervisorScript(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	for _, name := range fallbackSupervisorRepoNames {
		fallback := filepath.Join(home, "source", "repos", "Personal", name)
		if hasSupervisorScript(fallback) {
			return fallback
		}
	}

	return ""
}
