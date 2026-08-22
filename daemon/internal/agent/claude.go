// Package agent drives an agent CLI as a SUBPROCESS with structured I/O.
//
// WHY THIS EXISTS, and it is the whole point of the daemon:
//
// The shell supervisor drove agents by typing into tmux panes with send-keys
// and then screen-scraping to decide what happened. Every expensive failure
// on 2026-08-21/22 is that one decision:
//
//   - "/clear did not blank agent-supervisor:3's screen -- #494 was NOT
//     dispatched" (four review attempts died here)
//   - lanes renamed and claimed but never given their brief
//   - a brief typed into a prompt box and never submitted (#414)
//   - a task stamped `complete` while its process was still running (#488)
//   - panes wedged such that even C-u would not clear them
//
// None of those are bugs in the guards. They are what happens when delivery
// is INFERRED from pixels instead of OBSERVED from a process.
//
// Here, delivery is a fact: the process either exited 0 with a well-formed
// JSON result object on stdout, or it did not. There is no third state that
// looks like success. `Run` returns an error for every failure mode and never
// a success-shaped zero value -- fail-closed, the same posture
// claude_print_transport.py established when it proved this transport works
// against real dispatches.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout: a turn can run tools, edit code and open a PR. Minutes, not
// seconds. Same value the python transport settled on for the same reason.
const DefaultTimeout = 15 * time.Minute

// Result is the parsed `--output-format json` object. Only the fields the
// daemon actually reads are declared; the CLI emits more.
type Result struct {
	IsError   bool   `json:"is_error"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Text      string `json:"result"`
	NumTurns  int    `json:"num_turns"`
	// CostUSD is the CLI's own reported cost for this turn. Without it the
	// budget cap sums to zero forever and never binds -- found by running the
	// gate and watching it fail to block, not by reading the code.
	CostUSD float64 `json:"total_cost_usd"`
}

// Claude runs `claude -p --output-format json` for one turn.
//
// Bin/Model/Cwd are explicit rather than inherited so a dispatch cannot
// silently pick up the operator's environment -- the same class of bug as
// agent-supervisor#494, where lanes inherited the whole MCP surface because
// nothing said otherwise.
type Claude struct {
	Bin       string
	Model     string
	Cwd       string
	Timeout   time.Duration
	SessionID string // empty on the first turn; set to resume

	// StrictMCP mirrors #494: with no MCPConfig this means zero MCP servers.
	// Never omitted -- omitting it is the bug that PR #495 fixed.
	StrictMCP bool
	MCPConfig string
}

var (
	// ErrNoJSON: the process exited but stdout was not the single result
	// object the --output-format json contract promises. A crash, a changed
	// output shape, or a non-zero exit before any turn ran. NEVER treated as
	// success.
	ErrNoJSON = errors.New("agent: stdout was not a well-formed JSON result")
	// ErrTimeout: the process did not exit in time. The turn's disposition is
	// UNKNOWN, which is not the same as failed -- the caller must not stamp a
	// terminal state from this alone (agent-supervisor#488).
	ErrTimeout = errors.New("agent: turn did not complete before the deadline")
)

func (c *Claude) args(prompt string) []string {
	a := []string{"-p", "--output-format", "json"}
	if c.Model != "" {
		a = append(a, "--model", c.Model)
	}
	a = append(a, "--dangerously-skip-permissions")
	if c.StrictMCP {
		a = append(a, "--strict-mcp-config")
		if c.MCPConfig != "" {
			a = append(a, "--mcp-config", c.MCPConfig)
		}
	}
	if c.SessionID != "" {
		a = append(a, "--resume", c.SessionID)
	}
	return append(a, prompt)
}

// Run executes one turn to completion.
//
// The contract, and the reason this replaces send-keys: a nil error means the
// agent RECEIVED the prompt and produced a result. That is not an inference.
func (c *Claude) Run(ctx context.Context, prompt string) (*Result, error) {
	if c.Bin == "" {
		c.Bin = "claude"
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.Bin, c.args(prompt)...)
	cmd.Dir = c.Cwd
	// Own process group, so the whole tree can be signalled (paperclip).
	setProcGroup(cmd)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("%w after %s", ErrTimeout, c.Timeout)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return nil, fmt.Errorf("%w: exit=%v stderr=%q", ErrNoJSON, err, trunc(stderr.String(), 500))
	}
	var r Result
	if jerr := json.Unmarshal([]byte(out), &r); jerr != nil {
		return nil, fmt.Errorf("%w: %v -- stdout=%q", ErrNoJSON, jerr, trunc(out, 500))
	}
	// A non-zero exit WITH a parseable result is still a failed turn. Report
	// both rather than letting the JSON's presence launder the exit code.
	if err != nil && !r.IsError {
		return &r, fmt.Errorf("agent: exited %v but result claimed success -- treating as failure", err)
	}
	if r.IsError {
		return &r, fmt.Errorf("agent: turn reported is_error subtype=%q", r.Subtype)
	}
	return &r, nil
}

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
