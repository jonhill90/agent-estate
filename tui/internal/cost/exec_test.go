package cost

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestExecRunnerSurfacesATimeoutInsteadOfHangingForever mirrors
// internal/board's own ExecRunner timeout test (agent-b3.md's fix): a
// stalled `npx ccusage` (npm registry stall, a wedged node process, any
// hang short of the process actually exiting) blocked Dashboard's own
// SPEND TODAY fetch (buildDashboardFetch's costFetch call) forever before
// this fix, since ExecRunner had no bound of its own.
//
// `sh -c "sleep 3"` is a real subprocess that runs for 3s and exits 0 --
// long enough that, without the fix, this test would observe the full 3s
// and no timeout error (confirmed by reverting the fix and re-running this
// test -- see the PR body's own mutation-check record). With the fix,
// execTimeout is shrunk for the duration of this test so the fetch is
// killed and surfaces a real error well before sleep 3 would return on its
// own.
func TestExecRunnerSurfacesATimeoutInsteadOfHangingForever(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}

	orig := execTimeout
	execTimeout = 200 * time.Millisecond
	defer func() { execTimeout = orig }()

	run := ExecRunner(sh, "-c")
	start := time.Now()
	_, err = run([]string{"sleep 3"})
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
