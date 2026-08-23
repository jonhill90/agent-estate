// supervisord -- the supervisor as a single Go binary.
//
// Replaces the send-keys/screen-scrape control plane with subprocess control
// and structured JSON. Delivery is observed, not inferred.
//
//	supervisord status                     -- ledger counts, no side effects
//	supervisord run -task ID -brief FILE   -- dispatch one task, prove the outcome
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"agent-supervisor/daemon/internal/agent"
	"agent-supervisor/daemon/internal/claim"
	"agent-supervisor/daemon/internal/dispatch"
	"agent-supervisor/daemon/internal/ledger"
	"agent-supervisor/daemon/internal/sendmsg"
	"agent-supervisor/daemon/internal/vault"
)

// newAdapter is the one place a -harness/Job.Harness NAME turns into a
// concrete agent.Adapter. Both `supervisord run` (this file) and
// `supervisord batch` (batch.go) call this rather than each constructing
// *agent.Claude or *agent.Codex inline, so adding a third harness later is
// a change in exactly one function, not every call site that dispatches a
// job.
func newAdapter(harness, bin, model, cwd string, timeout time.Duration) (agent.Adapter, error) {
	switch harness {
	case "", "claude":
		if bin == "" {
			bin = "claude"
		}
		return &agent.Claude{
			Bin: bin, Model: model, Cwd: cwd, Timeout: timeout,
			// #494: strict by default. Zero MCP servers unless one is named.
			StrictMCP: true,
		}, nil
	case "codex":
		if bin == "" {
			bin = "codex"
		}
		return &agent.Codex{Bin: bin, Model: model, Cwd: cwd, Timeout: timeout}, nil
	default:
		return nil, fmt.Errorf("unknown -harness %q (want \"claude\" or \"codex\")", harness)
	}
}

// failAdapter is an agent.Adapter whose Run always fails with a fixed error
// -- used by `supervisord batch` when one job's -harness/"harness" name is
// unrecognised, so that ONE bad line reports a per-job dispatch failure
// (through RunGated's own error path) instead of aborting the whole batch
// or silently falling back to a harness the line never asked for.
type failAdapter struct{ err error }

func (f failAdapter) Run(context.Context, string) (*agent.Result, error) { return nil, f.err }

// setSessionID is the one place a -session-id flag turns into the
// resume field the underlying adapter already exposes for exactly this
// (agent.Claude.SessionID / agent.Codex.SessionID -- "" means fresh
// session, non-"" means --resume). Adapter is deliberately narrow
// (Run(ctx, prompt) only, adapter.go's own doc comment), so setting a
// vendor-specific field needs the same type switch dryArgv below already
// uses rather than widening the interface for one flag.
func setSessionID(a agent.Adapter, sessionID string) error {
	switch v := a.(type) {
	case *agent.Claude:
		v.SessionID = sessionID
	case *agent.Codex:
		v.SessionID = sessionID
	default:
		return fmt.Errorf("supervisord send: %T has no session to resume", a)
	}
	return nil
}

// dryArgv asks the adapter itself for the argv it would exec (DryRunArgv,
// defined alongside each adapter's own args()) rather than a second
// hand-written guess at its flags that could silently drift from the real
// one.
func dryArgv(a agent.Adapter, prompt string) []string {
	switch v := a.(type) {
	case *agent.Claude:
		return v.DryRunArgv(prompt)
	case *agent.Codex:
		return v.DryRunArgv(prompt)
	default:
		return []string{fmt.Sprintf("<no DryRunArgv for %T>", a)}
	}
}

func defaultLedger() string {
	if p := os.Getenv("AGENT_SUPERVISOR_LEDGER"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "agent-dotfiles-supervisor", "ledger.sqlite3")
}

// defaultClaimScript resolves scripts/supervisor/claim.sh -- the SAME
// tested claim mechanism the tmux/cli.py side of this estate already uses
// (see internal/claim's own doc comment for why this reuses it rather
// than reimplementing in Go). $AGENT_SUPERVISOR_REPO is the estate-wide
// convention other tools already use to find this checkout from outside
// it (agent-tui's own CLAUDE.md documents it the same way); reused here
// rather than inventing a second "where is agent-supervisor" variable.
// Empty means unresolved -- cmdRun/cmdBatch refuse outright (not silently
// disable the claim) when an -issue was given but no script path could be
// found, per this task's own "refuse, don't silently proceed" rule.
func defaultClaimScript() string {
	if p := os.Getenv("AGENT_SUPERVISOR_CLAIM_SCRIPT"); p != "" {
		return p
	}
	if repo := os.Getenv("AGENT_SUPERVISOR_REPO"); repo != "" {
		return filepath.Join(repo, "scripts", "supervisor", "claim.sh")
	}
	return ""
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "status":
		os.Exit(cmdStatus(os.Args[2:]))
	case "batch":
		os.Exit(cmdBatch(os.Args[2:]))
	case "run":
		os.Exit(cmdRun(os.Args[2:]))
	case "send":
		os.Exit(cmdSend(os.Args[2:]))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `supervisord -- observed-delivery agent supervisor

  supervisord status [-ledger PATH]
  supervisord run -task ID -brief FILE [-lane L] [-cwd DIR] [-model M]
                  [-timeout DUR] [-ledger PATH] [-dry-run]
                  [-issue N] [-issue-repo OWNER/NAME] [-claim-script PATH]
  supervisord send -session-id ID -message TEXT [-cwd DIR] [-harness H]
                   [-model M] [-timeout DUR]

Delivery is proven by a process exit and a parsed JSON result, never by
reading a terminal. A turn that times out leaves its task NON-terminal on
purpose: unknown is not failed.

-issue N claims the GitHub issue (via scripts/supervisor/claim.sh, the
same mechanism the tmux/cli.py side already uses) before writing anything
to the ledger, and refuses the dispatch outright if the claim fails.
Omitted or 0: no claim is taken, same as before this flag existed -- not
every task this daemon dispatches corresponds to a real issue.

send resumes an EXISTING session (--resume under the hood, agent-tui's
SPEC-shell.md S7 / agent-supervisor#508) rather than starting new ledger
work: no -task, no -brief, no ledger row, no claim. Its outcome is one of
three states on stdout/exit code -- delivered (exit 0), failed (exit 1),
or unknown (exit 3, a timeout left non-terminal on purpose, never stamped
failed).
`)
}

func cmdStatus(argv []string) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	lp := fs.String("ledger", defaultLedger(), "path to ledger.sqlite3")
	fs.Parse(argv)

	l, err := ledger.Open(*lp)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer l.Close()

	counts, err := l.Counts()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	keys := make([]string, 0, len(counts))
	total := 0
	for k, v := range counts {
		keys = append(keys, string(k))
		total += v
	}
	sort.Strings(keys)
	fmt.Printf("ledger: %s\n", *lp)
	for _, k := range keys {
		fmt.Printf("  %-16s %d\n", k, counts[ledger.Status(k)])
	}
	fmt.Printf("  %-16s %d\n", "TOTAL", total)
	return 0
}

func cmdRun(argv []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var (
		lp          = fs.String("ledger", defaultLedger(), "path to ledger.sqlite3")
		task        = fs.String("task", "", "task id (required)")
		brief       = fs.String("brief", "", "path to the brief file (required)")
		lane        = fs.String("lane", "daemon", "lane name")
		cwd         = fs.String("cwd", ".", "working directory for the agent")
		harness     = fs.String("harness", "claude", "which vendor CLI to drive: claude | codex")
		model       = fs.String("model", "", "model (default: \"sonnet\" for -harness claude; each CLI's own default for any other harness)")
		bin         = fs.String("bin", "", "agent binary (default: \"claude\" or \"codex\", per -harness)")
		timeout     = fs.Duration("timeout", agent.DefaultTimeout, "per-turn timeout")
		dry         = fs.Bool("dry-run", false, "print the command and exit without dispatching")
		issue       = fs.Int("issue", 0, "GitHub issue number this task closes (0 = no issue, no claim taken)")
		issueRepo   = fs.String("issue-repo", "", "OWNER/NAME for -issue; empty lets claim.sh resolve it from cwd")
		claimScript = fs.String("claim-script", defaultClaimScript(), "path to scripts/supervisor/claim.sh (required when -issue is set)")
	)
	fs.Parse(argv)
	// -model's default depends on -harness, not a fixed flag literal: a
	// hardcoded "sonnet" default silently reaching codex as --model sonnet
	// (a Claude model name codex does not have) is exactly the multi-harness
	// bug this PR exists to not repeat. Only claude keeps the old implicit
	// default; every other harness gets no --model flag at all unless the
	// caller names one, so its own CLI default applies.
	if *model == "" && (*harness == "" || *harness == "claude") {
		*model = "sonnet"
	}

	if *task == "" || *brief == "" {
		fmt.Fprintln(os.Stderr, "supervisord run: -task and -brief are required")
		return 2
	}
	// An -issue with no resolvable claim.sh refuses outright -- silently
	// skipping the claim would reopen the exact collision gap this flag
	// exists to close (agent-supervisor#28: dispatched twice, once by the
	// Director, once by the supervisor, because nothing recorded a claim).
	if *issue != 0 && *claimScript == "" {
		fmt.Fprintln(os.Stderr, "supervisord run: -issue given but claim.sh could not be resolved -- "+
			"set -claim-script, $AGENT_SUPERVISOR_CLAIM_SCRIPT, or $AGENT_SUPERVISOR_REPO")
		return 2
	}
	body, err := os.ReadFile(*brief)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisord: brief: %v\n", err)
		return 1
	}
	abscwd, err := filepath.Abs(*cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisord: cwd: %v\n", err)
		return 1
	}

	// Memory is LOADED, not left to the agent to remember to read. Jon's rules
	// name the vault canonical; every session so far ignored that and wrote to
	// the harness-native store instead. Here it is prepended to the brief, so
	// there is no path where an agent starts blind and silently re-derives.
	// A missing vault is fatal: starting blind must be loud.
	v, err := vault.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisord: %v\n", err)
		return 1
	}
	pre, err := v.Preamble()
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisord: %v\n", err)
		return 1
	}
	prompt := pre + string(body)

	a, err := newAdapter(*harness, *bin, *model, abscwd, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisord: %v\n", err)
		return 2
	}

	if *dry {
		fmt.Printf("would run (%s): %s\n", *harness, strings.Join(dryArgv(a, "<brief>"), " "))
		fmt.Printf("  cwd:     %s\n", abscwd)
		fmt.Printf("  task:    %s\n", *task)
		fmt.Printf("  brief:   %s (%d bytes)\n", *brief, len(body))
		fmt.Printf("  timeout: %s\n", *timeout)
		if *issue != 0 {
			fmt.Printf("  claim:   issue #%d (repo=%q, script=%s)\n", *issue, *issueRepo, *claimScript)
		}
		return 0
	}

	l, err := ledger.Open(*lp)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer l.Close()

	// Ctrl-C cancels the turn; the task is left non-terminal, same as a
	// timeout. An interrupted run must not look like a failed one.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("dispatch %s -> %s/%s (cwd=%s, timeout=%s)\n", *task, *harness, *model, abscwd, *timeout)
	var issueRef *dispatch.IssueRef
	var gates dispatch.Gates
	if *issue != 0 {
		issueRef = &dispatch.IssueRef{Number: *issue, Repo: *issueRepo}
		gates.Claim = &claim.ScriptGate{ScriptPath: *claimScript}
		fmt.Printf("claim: taking issue #%d before dispatch\n", *issue)
	}
	out := dispatch.RunGated(ctx, l, a, abscwd, *harness, *task, *lane, prompt, issueRef, gates)

	fmt.Printf("elapsed: %s\n", out.Elapsed.Round(time.Millisecond))
	if out.Err != nil {
		fmt.Fprintf(os.Stderr, "OUTCOME: NOT DELIVERED -- %v\n", out.Err)
		return 1
	}
	fmt.Printf("OUTCOME: delivered and complete (session=%s turns=%d)\n", out.SessionID, out.Turns)
	return 0
}

// sendReport is the one JSON shape `send` ever prints to stdout -- a
// Python WriteSource (scripts/supervisor/supervisor_view.py's
// SessionSendSource, agent-supervisor#508) parses this rather than
// scraping the human-readable OUTCOME lines cmdRun prints, the same
// "structured I/O, not a screen" posture claude.go's own doc comment
// states as this whole daemon's reason to exist.
type sendReport struct {
	Status    string  `json:"status"` // "delivered" | "failed" | "unknown", sendmsg.Status's own vocabulary
	SessionID string  `json:"session_id,omitempty"`
	Turns     int     `json:"turns,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
	Error     string  `json:"error,omitempty"`
}

func cmdSend(argv []string) int {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	var (
		sessionID = fs.String("session-id", "", "harness session id to resume (required)")
		message   = fs.String("message", "", "message to send (required)")
		cwd       = fs.String("cwd", ".", "working directory for the agent")
		harness   = fs.String("harness", "claude", "which vendor CLI to drive: claude | codex")
		model     = fs.String("model", "", "model (default: \"sonnet\" for -harness claude; each CLI's own default for any other harness)")
		bin       = fs.String("bin", "", "agent binary (default: \"claude\" or \"codex\", per -harness)")
		timeout   = fs.Duration("timeout", agent.DefaultTimeout, "per-turn timeout")
	)
	fs.Parse(argv)
	if *model == "" && (*harness == "" || *harness == "claude") {
		*model = "sonnet"
	}

	if *sessionID == "" || *message == "" {
		fmt.Fprintln(os.Stderr, "supervisord send: -session-id and -message are required")
		return 2
	}

	abscwd, err := filepath.Abs(*cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisord: cwd: %v\n", err)
		return 1
	}

	a, err := newAdapter(*harness, *bin, *model, abscwd, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisord: %v\n", err)
		return 2
	}
	if err := setSessionID(a, *sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "supervisord: %v\n", err)
		return 2
	}

	// Ctrl-C leaves the outcome unknown, same posture as `run` -- an
	// interrupted send must not look like a failed one.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result := sendmsg.Send(ctx, a, *message)

	report := sendReport{Status: string(result.Status), SessionID: result.SessionID, Turns: result.Turns, CostUSD: result.CostUSD}
	if result.Err != nil {
		report.Error = result.Err.Error()
	}
	enc, _ := json.Marshal(report)
	fmt.Println(string(enc))

	switch result.Status {
	case sendmsg.StatusDelivered:
		return 0
	case sendmsg.StatusUnknown:
		// A distinct exit code from "failed": a caller that only checks
		// exit != 0 still must not treat this the same as a confirmed
		// failure without reading the JSON body's own status field.
		return 3
	default: // StatusFailed
		return 1
	}
}
