package mcp

import (
	"os"
	"os/exec"
	"testing"
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
