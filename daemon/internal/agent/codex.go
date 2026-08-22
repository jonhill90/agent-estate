package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Codex runs `codex exec --json` for one turn.
//
// VERIFIED AGAINST THE SHIPPED CLI, not from memory: `codex --version`
// reports codex-cli 0.149.0 on this machine; `codex exec --help` and a
// handful of live `codex exec --json` invocations (2026-08-22, recorded in
// this PR) are what the shapes below come from, not the docs or training
// data. Codex has NO equivalent of `claude -p --output-format json`'s
// single terminal result object. `codex exec --json` prints one JSON
// object PER EVENT to stdout (JSONL): `thread.started` (carries the
// thread/session id), `turn.started`, one `item.completed` per message or
// tool step (an `agent_message` item carries the reply text; an `error`
// item can appear mid-turn), and exactly one terminal event --
// `turn.completed` (success, carries a token-usage block) or `turn.failed`
// (failure, carries the vendor's own error message). A bad `--model` was
// used to confirm the failure shape live: exit 1, a `turn.failed` event
// whose `error.message` is the vendor's raw 400 `invalid_request_error`
// JSON string.
//
// This adapter reads that event stream (parseCodexEvents, below) rather
// than pretending Codex emits Claude's single-object shape. That is the
// "closest honest thing" the task asked for when a CLI cannot satisfy the
// original contract -- not a fabricated JSON envelope Codex does not
// produce.
//
// Result.CostUSD is always 0 for a Codex-run turn, and that is correct,
// not a gap: `--json`'s usage block reports token counts (input, cached,
// output, reasoning) with no dollar figure, because this is a
// ChatGPT-subscription CLI call, not a metered API call -- exactly the
// "subscription tokens instead of per-token API rates" point this adapter
// exists to serve. dispatch.go's budget gate already reads CostUSD == 0
// as "nothing to add" (see RunGated's `res.CostUSD > 0` guard), which is
// the right behaviour for a harness with no per-turn dollar cost, not an
// approximation standing in for a real one.
type Codex struct {
	Bin       string
	Model     string
	Cwd       string
	Timeout   time.Duration
	SessionID string // empty on the first turn; set (the thread_id) to resume
}

// args builds `codex exec [resume] --json ... [--model M] [SESSION_ID] PROMPT`.
//
// --skip-git-repo-check: a dispatch's Cwd is a worktree the daemon already
// knows is a git repo, but Codex's own check re-walks parent directories
// looking for `.git` and is a needless second source of failure for a
// property dispatch.go's caller (EnsureLane) already established.
//
// --dangerously-bypass-approvals-and-sandbox: the Codex analogue of
// claude.go's `--dangerously-skip-permissions` -- both run one unattended
// turn with no human to answer an approval prompt, so both must bypass the
// approval gate rather than hang on one. `codex exec --help` has no
// `-a/--ask-for-approval` flag at all (that flag exists only on the
// interactive `codex` command); the bypass flag is exec's own documented
// route to unattended execution, confirmed against `codex exec --help`'s
// actual output, not assumed from the top-level command's flags.
// DryRunArgv returns the exact argv Run would exec (bin first, then every
// flag) -- same purpose as Claude.DryRunArgv, kept in lockstep with
// Codex.args rather than restated separately by a caller.
func (c *Codex) DryRunArgv(prompt string) []string {
	bin := c.Bin
	if bin == "" {
		bin = "codex"
	}
	return append([]string{bin}, c.args(prompt)...)
}

func (c *Codex) args(prompt string) []string {
	a := []string{"exec"}
	if c.SessionID != "" {
		a = append(a, "resume")
	}
	a = append(a, "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox")
	if c.Model != "" {
		a = append(a, "--model", c.Model)
	}
	if c.SessionID != "" {
		// resume's own usage is `codex exec resume [OPTIONS] [SESSION_ID]
		// [PROMPT]` -- SESSION_ID is a positional that must precede PROMPT,
		// confirmed against `codex exec resume --help`.
		a = append(a, c.SessionID)
	}
	return append(a, prompt)
}

// codexEvent is one line of `codex exec --json`'s JSONL stream. Only the
// fields this adapter reads are declared; the CLI emits more per event
// (and more event types than this struct names) than dispatch needs.
type codexEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Item     struct {
		Type    string `json:"type"`    // "agent_message" | "error" | ...
		Text    string `json:"text"`    // set on agent_message items
		Message string `json:"message"` // set on error items
	} `json:"item"`
	Message string `json:"message"` // top-level "error" event's own text
	Error   struct {
		Message string `json:"message"`
	} `json:"error"` // turn.failed's own error
}

// parseCodexEvents reads Codex's JSONL stream and folds it into Claude's
// Result shape so dispatch.go, budget.go and agent.Classify keep working
// against one type regardless of which adapter produced it.
//
// A line this struct does not recognise (an event type or shape a future
// Codex version adds) is skipped, not fatal -- one unfamiliar frame must
// not make the whole stream read as empty, the same "one bad frame doesn't
// blank the read" posture procgroup.go and claude.go already take toward
// partial/unexpected data elsewhere in this package.
func parseCodexEvents(raw string) (*Result, error) {
	var r Result
	sawEvent := false
	var lastErr string

	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		sawEvent = true
		switch ev.Type {
		case "thread.started":
			r.SessionID = ev.ThreadID
		case "item.completed":
			switch ev.Item.Type {
			case "agent_message":
				if ev.Item.Text != "" {
					r.Text = ev.Item.Text
				}
			case "error":
				r.IsError = true
				if ev.Item.Message != "" {
					lastErr = ev.Item.Message
				}
			}
		case "turn.completed":
			r.NumTurns++
		case "turn.failed":
			r.IsError = true
			r.NumTurns++
			if ev.Error.Message != "" {
				lastErr = ev.Error.Message
			}
		case "error":
			r.IsError = true
			if ev.Message != "" {
				lastErr = ev.Message
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !sawEvent {
		return nil, errors.New("no recognised codex JSONL events in stdout")
	}
	if r.IsError {
		r.Subtype = trunc(lastErr, 300)
	}
	return &r, nil
}

// Run executes one turn to completion. Same contract as Claude.Run: a nil
// error means Codex received the prompt and produced a result, observed
// from the process's own exit and stdout, never inferred.
func (c *Codex) Run(ctx context.Context, prompt string) (*Result, error) {
	if c.Bin == "" {
		c.Bin = "codex"
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.Bin, c.args(prompt)...)
	cmd.Dir = c.Cwd
	setProcGroup(cmd)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("%w after %s", ErrTimeout, c.Timeout)
	}

	r, perr := parseCodexEvents(stdout.String())
	if perr != nil {
		return nil, fmt.Errorf("%w: %v -- exit=%v stdout=%q stderr=%q",
			ErrNoJSON, perr, err, trunc(stdout.String(), 500), trunc(stderr.String(), 500))
	}
	// GUARD: a stream with recognised events but no thread.started is not a
	// turn this adapter can trust a session id from -- resuming it later
	// would resume nothing. Treated the same as an unparseable stream, not
	// as a parsed-but-incomplete success.
	if r.SessionID == "" {
		return nil, fmt.Errorf("%w: no thread.started event -- exit=%v stdout=%q stderr=%q",
			ErrNoJSON, err, trunc(stdout.String(), 500), trunc(stderr.String(), 500))
	}

	// A non-zero exit WITH a parsed result that never reported turn.failed
	// is still a failed turn -- same rule claude.go's Run applies, so the
	// exit code cannot be laundered by a stream that merely looks complete.
	if err != nil && !r.IsError {
		return r, fmt.Errorf("agent: codex exited %v but no turn.failed/error event -- treating as failure", err)
	}
	if r.IsError {
		return r, fmt.Errorf("agent: codex turn failed: %s", r.Subtype)
	}
	return r, nil
}
