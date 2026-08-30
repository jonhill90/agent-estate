package board

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestExecRunnerSurfacesATimeoutInsteadOfHangingForever is agent-b3.md's own
// fix: ExecRunner backs BOTH LedgerRunner's sqlite3 reads (ReadTaskRows)
// and, via GitHubRunner(ExecRunner(ghBin)), every `gh` call
// FetchIssues/FetchPRs make in a loop (buildBoardFetch, buildDashboardFetch
// in cmd/agent-tui) -- before this fix, none of the three had ANY bound, so
// a subprocess that stopped responding (a stalled network call, anything
// short of the process actually exiting) blocked the whole fetch's tea.Cmd
// forever. The nav walk (testdata/vhs/full-nav-walk-report.md rows 01/04)
// saw exactly this: Dashboard and Tasks both stuck on "not fetched yet"/
// "(loading)" with no error, ever.
//
// `sh -c "sleep 3"` is a real subprocess that runs for 3s and then exits 0
// -- long enough that, without the fix, this test would observe the full
// 3s and no timeout error (proven by literally reverting the fix and
// re-running this test -- see the PR body's own mutation-check record, not
// re-derived here as a permanent part of the suite: a real "hangs forever"
// subprocess would be irresponsible to commit as a test fixture). With the
// fix, execTimeout is shrunk for the duration of this test so the fetch is
// killed and surfaces a real error well before sleep 3 would have returned
// on its own.
func TestExecRunnerSurfacesATimeoutInsteadOfHangingForever(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}

	orig := execTimeout
	execTimeout = 200 * time.Millisecond
	defer func() { execTimeout = orig }()

	run := ExecRunner(sh)
	start := time.Now()
	_, err = run([]string{"-c", "sleep 3"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("ExecRunner against a subprocess that outlives execTimeout returned no error")
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("error = %v, want a timeout error naming execTimeout", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("ExecRunner took %s to fail, want it bounded by execTimeout (%s), not by the subprocess's own runtime", elapsed, execTimeout)
	}
}
