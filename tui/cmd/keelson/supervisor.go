package main

import (
	"os"
	"path/filepath"
)

// fallbackSupervisorRepo is the one hardcoded location the brief names as a
// last resort (agent-tui#49 item 1) when neither -supervisor-repo/
// $AGENT_SUPERVISOR_REPO nor a git-repo walk-up finds one -- Jon's own
// checkout, on the machine this ships to first. It is a fallback, not a
// requirement: discoverSupervisorRepo trying and failing here is one of
// three ordinary outcomes (the others being "found by walk-up" and "found
// nowhere, open degraded"), never a reason to exit.
const fallbackSupervisorRepo = "source/repos/Personal/agent-supervisor"

// hasSupervisorScript reports whether dir looks like an agent-supervisor
// checkout -- the same file connect() itself later stats before trying to
// run it (mcp_server.py), checked here first so discovery doesn't hand
// connect() a directory that merely LOOKS like a repo root.
func hasSupervisorScript(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "scripts", "supervisor", "mcp_server.py"))
	return err == nil
}

// discoverSupervisorRepo implements agent-tui#49 item 1's fix: a bare
// `keelson` must never exit 1 just because -supervisor-repo was not typed.
// It walks up from start looking for a checkout containing
// scripts/supervisor/mcp_server.py (the file connect() itself requires),
// then falls back to fallbackSupervisorRepo under $HOME. Returns "" -- not
// an error -- when neither is found; main() opens in a degraded state
// rather than refusing to start, per the brief's "never exit 1 on a bare
// launch."
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

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		fallback := filepath.Join(home, fallbackSupervisorRepo)
		if hasSupervisorScript(fallback) {
			return fallback
		}
	}

	return ""
}
