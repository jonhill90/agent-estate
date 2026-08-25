package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"agent-supervisor/daemon/internal/agent"
	"agent-supervisor/daemon/internal/budget"
	"agent-supervisor/daemon/internal/claim"
	"agent-supervisor/daemon/internal/dispatch"
	"agent-supervisor/daemon/internal/ledger"
	"agent-supervisor/daemon/internal/pressure"
	"agent-supervisor/daemon/internal/vault"
)

// jobSpec is one line of the batch file (JSON lines).
type jobSpec struct {
	Task  string `json:"task"`
	Lane  string `json:"lane"`
	Brief string `json:"brief"` // path to a brief file
	Cwd   string `json:"cwd"`
	// Harness selects the adapter this ONE job dispatches through --
	// "claude" (default, unchanged) or "codex". Per-line, not per-batch:
	// one -file can mix harnesses, which is the actual point of adding a
	// second adapter (see agent.Adapter's own doc comment).
	Harness string `json:"harness"`
	// Issue and Repo name the GitHub issue this ONE job closes, if any --
	// per-line, same reasoning as Harness: a batch can mix issue-backed
	// jobs with issue-less ones (a demo/proof task has no issue at all).
	// 0/empty means no claim is taken for this job, same as today.
	Issue int    `json:"issue"`
	Repo  string `json:"repo"`
}

// cmdBatch dispatches many tasks CONCURRENTLY.
//
// The shell supervisor was effectively serial per tick: one transition, then
// stop. Concurrency here is bounded on purpose -- see dispatch.DefaultConcurrency
// for why the cap exists and what happened without one.
func cmdBatch(argv []string) int {
	fs := flag.NewFlagSet("batch", flag.ExitOnError)
	var (
		lp          = fs.String("ledger", defaultLedger(), "path to ledger.sqlite3")
		file        = fs.String("file", "", "JSON-lines file of {task,lane,brief,cwd,harness} (required)")
		harness     = fs.String("harness", "claude", "default harness for a line with no \"harness\" of its own: claude | codex")
		model       = fs.String("model", "", "model applied to every job (default: \"sonnet\" for a claude job; each CLI's own default otherwise)")
		bin         = fs.String("bin", "", "agent binary (default: \"claude\" or \"codex\", per each job's harness)")
		workers     = fs.Int("workers", 0, "max concurrent agents (0 = cores-2, capped 8)")
		capUSD      = fs.Float64("budget-usd", 50, "HARD spend cap over -budget-window; 0 disables (explicitly)")
		capWin      = fs.Duration("budget-window", 24*time.Hour, "rolling window for the spend cap")
		maxLoad     = fs.Float64("max-load-per-core", 3.0, "refuse to spawn above this load/core; 0 disables")
		minMem      = fs.Float64("min-free-mem-gb", 1.5, "refuse to spawn below this free memory; 0 disables")
		timeout     = fs.Duration("timeout", agent.DefaultTimeout, "per-turn timeout")
		claimScript = fs.String("claim-script", defaultClaimScript(), "path to scripts/supervisor/claim.sh (required if any job carries \"issue\")")
	)
	fs.Parse(argv)
	if *file == "" {
		fmt.Fprintln(os.Stderr, "supervisord batch: -file is required")
		return 2
	}

	f, err := os.Open(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervisord: %v\n", err)
		return 1
	}
	defer f.Close()

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

	var jobs []dispatch.Job
	dec := json.NewDecoder(f)
	for {
		var s jobSpec
		if err := dec.Decode(&s); err != nil {
			break
		}
		body, rerr := os.ReadFile(s.Brief)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "supervisord: %s: %v\n", s.Task, rerr)
			return 1
		}
		cwd, _ := filepath.Abs(s.Cwd)
		// FOUND BY RUNNING IT: the live schema carries UNIQUE(tasks.lane), so a
		// lane holds at most one task. Concurrent jobs therefore need distinct
		// lanes -- "one agent per lane" is the real model, not a workaround.
		// Defaulting every job to "daemon" serialised the batch and failed 3 of 4.
		if s.Lane == "" {
			s.Lane = "d-" + s.Task
		}
		var issueRef *dispatch.IssueRef
		if s.Issue != 0 {
			issueRef = &dispatch.IssueRef{Number: s.Issue, Repo: s.Repo}
		}
		jobs = append(jobs, dispatch.Job{
			TaskID: s.Task, Lane: s.Lane, Brief: pre + string(body), Cwd: cwd,
			Harness: s.Harness, Issue: issueRef,
		})
	}
	if len(jobs) == 0 {
		fmt.Fprintln(os.Stderr, "supervisord batch: no jobs parsed")
		return 2
	}
	// Same refusal as `supervisord run`'s own -issue check: an issue-backed
	// job with no resolvable claim.sh refuses the WHOLE batch outright
	// rather than silently dispatching that one job without a claim.
	for _, j := range jobs {
		if j.Issue != nil && *claimScript == "" {
			fmt.Fprintf(os.Stderr, "supervisord batch: job %s carries issue #%d but claim.sh could not be resolved -- "+
				"set -claim-script, $AGENT_SUPERVISOR_CLAIM_SCRIPT, or $AGENT_SUPERVISOR_REPO\n", j.TaskID, j.Issue.Number)
			return 2
		}
	}

	l, err := ledger.Open(*lp)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer l.Close()

	w := *workers
	if w <= 0 {
		w = dispatch.DefaultConcurrency()
	}
	fmt.Printf("batch: %d jobs, %d concurrent\n", len(jobs), w)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	start := time.Now()
	lim := pressure.Limits{MaxLoadPerCore: *maxLoad, MinFreeMemGB: *minMem}
	if r := pressure.Check(lim); !r.OK {
		fmt.Fprintf(os.Stderr, "supervisord: refusing to start -- %s\n", r.Reason)
		return 1
	} else {
		fmt.Printf("host: %s\n", r)
	}
	gates := dispatch.Gates{Budget: budget.New(budget.Policy{LimitUSD: *capUSD, Window: *capWin}), Pressure: &lim}
	if *claimScript != "" {
		gates.Claim = &claim.ScriptGate{ScriptPath: *claimScript}
	}
	fmt.Printf("gates: cap $%.2f/%s, load/core<%.1f, freeMem>%.1fGB\n", *capUSD, *capWin, *maxLoad, *minMem)

	outs := dispatch.RunPoolGated(ctx, l, func(j dispatch.Job) (agent.Adapter, string) {
		h := j.Harness
		if h == "" {
			h = *harness
		}
		// Same per-harness default as `supervisord run` (main.go): a bare
		// -model default of "sonnet" (a Claude model name) must not reach a
		// codex job just because -model was left unset for the WHOLE batch.
		m := *model
		if m == "" && h == "claude" {
			m = "sonnet"
		}
		a, aerr := newAdapter(h, *bin, m, j.Cwd, *timeout)
		if aerr != nil {
			// newAdapter only fails on an unknown harness name -- a bad
			// batch line, not a dispatch failure. failAdapter turns that
			// into an Adapter whose Run always returns aerr unchanged, so
			// RunGated's own error path (not a panic, not a silently
			// skipped job) is what reports it per-job. The harness
			// returned here is "claude" (the schema's own safe default),
			// NOT the unrecognised `h` -- EnsureLane's harness column has
			// a CHECK constraint (see ledger.go) and a garbage `h` would
			// fail THAT insert too, replacing aerr's clear "unknown
			// -harness" message with a confusing SQL error before the
			// caller ever sees why the job really failed.
			return failAdapter{err: aerr}, "claude"
		}
		return a, h
	}, jobs, w, gates)

	ok, bad := 0, 0
	for _, o := range outs {
		if o.Err != nil {
			bad++
			fmt.Printf("  FAIL %-24s %v\n", o.TaskID, o.Err)
			continue
		}
		ok++
		fmt.Printf("  ok   %-24s %s turns=%d liveness=%s $%.4f\n", o.TaskID, o.Elapsed.Round(time.Millisecond), o.Turns, o.Liveness, o.CostUSD)
	}
	fmt.Printf("batch: %d ok, %d failed, wall %s, spend $%.4f of $%.2f cap\n",
		ok, bad, time.Since(start).Round(time.Millisecond), gates.Budget.SpentUSD(), *capUSD)
	if bad > 0 {
		return 1
	}
	return 0
}
