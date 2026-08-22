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
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"agent-supervisor/daemon/internal/agent"
	"agent-supervisor/daemon/internal/dispatch"
	"agent-supervisor/daemon/internal/ledger"
	"agent-supervisor/daemon/internal/vault"
)

func defaultLedger() string {
	if p := os.Getenv("AGENT_SUPERVISOR_LEDGER"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "agent-dotfiles-supervisor", "ledger.sqlite3")
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

Delivery is proven by a process exit and a parsed JSON result, never by
reading a terminal. A turn that times out leaves its task NON-terminal on
purpose: unknown is not failed.
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
		lp      = fs.String("ledger", defaultLedger(), "path to ledger.sqlite3")
		task    = fs.String("task", "", "task id (required)")
		brief   = fs.String("brief", "", "path to the brief file (required)")
		lane    = fs.String("lane", "daemon", "lane name")
		cwd     = fs.String("cwd", ".", "working directory for the agent")
		model   = fs.String("model", "sonnet", "model")
		bin     = fs.String("bin", "claude", "agent binary")
		timeout = fs.Duration("timeout", agent.DefaultTimeout, "per-turn timeout")
		dry     = fs.Bool("dry-run", false, "print the command and exit without dispatching")
	)
	fs.Parse(argv)

	if *task == "" || *brief == "" {
		fmt.Fprintln(os.Stderr, "supervisord run: -task and -brief are required")
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

	a := &agent.Claude{
		Bin: *bin, Model: *model, Cwd: abscwd,
		Timeout: *timeout,
		// #494: strict by default. Zero MCP servers unless one is named.
		StrictMCP: true,
	}

	if *dry {
		fmt.Printf("would run: %s -p --output-format json --model %s --dangerously-skip-permissions --strict-mcp-config <brief>\n", *bin, *model)
		fmt.Printf("  cwd:     %s\n", abscwd)
		fmt.Printf("  task:    %s\n", *task)
		fmt.Printf("  brief:   %s (%d bytes)\n", *brief, len(body))
		fmt.Printf("  timeout: %s\n", *timeout)
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

	fmt.Printf("dispatch %s -> %s (cwd=%s, timeout=%s)\n", *task, *model, abscwd, *timeout)
	out := dispatch.Run(ctx, l, a, *task, *lane, prompt)

	fmt.Printf("elapsed: %s\n", out.Elapsed.Round(time.Millisecond))
	if out.Err != nil {
		fmt.Fprintf(os.Stderr, "OUTCOME: NOT DELIVERED -- %v\n", out.Err)
		return 1
	}
	fmt.Printf("OUTCOME: delivered and complete (session=%s turns=%d)\n", out.SessionID, out.Turns)
	return 0
}
