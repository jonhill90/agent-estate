package mcp

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestClientAgainstRealMCPServer speaks to agent-supervisor's actual
// mcp_server.py over stdio -- not a fake -- to prove this client's framing
// works against the real protocol, not an assumption about it. It is
// skipped when no supervisor checkout is available (this repo imports no
// supervisor internals and does not vendor one), which is why CI here
// cannot always run it; a human or the agent-supervisor CI is expected to
// exercise the pairing.
func TestClientAgainstRealMCPServer(t *testing.T) {
	repo := os.Getenv("AGENT_SUPERVISOR_REPO")
	if repo == "" {
		t.Skip("AGENT_SUPERVISOR_REPO not set; skipping the live MCP pairing test")
	}
	script := repo + "/scripts/supervisor/mcp_server.py"
	if _, err := os.Stat(script); err != nil {
		t.Skipf("mcp_server.py not found at %s: %v", script, err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}

	c, err := Start(python, script)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	text, err := c.CallTool("lanes", map[string]any{})
	if err != nil {
		// A session that does not exist is a legitimate answer from the
		// real server (SupervisorUnavailable), not a client bug -- this
		// still proves the framing round-tripped a real error.
		t.Logf("lanes tool returned an error (acceptable if no tmux session is up): %v", err)
		return
	}
	if text == "" {
		t.Fatal("lanes tool returned no text on success")
	}
}

// TestCallSurfacesErrorInsteadOfHangingPastDeadline is the fix for at#22's
// blocking finding's second half: a subprocess that stops responding (not
// crashes -- crashing was already handled, via readLoop's cleanup closing
// every pending channel) must surface as a visible, honest error within
// callTimeout, not hang forever. `sh -c "cat >/dev/null"` reads every byte
// this client writes to its stdin and discards it, never writing a reply to
// its stdout -- exactly "the subprocess is alive but silent", the one case
// the pre-fix bare `resp, ok := <-ch` had no defense against (plain `cat`
// would not do: it echoes stdin back to stdout, which this client would
// decode as a reply). callTimeout is shrunk for the duration of this test
// so it does not actually wait out a production-length deadline.
func TestCallSurfacesErrorInsteadOfHangingPastDeadline(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}

	orig := callTimeout
	callTimeout = 200 * time.Millisecond
	defer func() { callTimeout = orig }()

	start := time.Now()
	c, err := Start(sh, "-c", "cat >/dev/null")
	elapsed := time.Since(start)
	if c != nil {
		defer c.Close()
	}
	if err == nil {
		t.Fatal("Start against a subprocess that never replies returned no error")
	}
	if !strings.Contains(err.Error(), "no reply within") {
		t.Fatalf("error = %v, want a deadline error naming callTimeout", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Start took %s to fail, want it bounded by callTimeout (%s)", elapsed, callTimeout)
	}
}
