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
	"agent-supervisor/daemon/internal/dispatch"
	"agent-supervisor/daemon/internal/ledger"
	"agent-supervisor/daemon/internal/vault"
)

// jobSpec is one line of the batch file (JSON lines).
type jobSpec struct {
	Task  string `json:"task"`
	Lane  string `json:"lane"`
	Brief string `json:"brief"` // path to a brief file
	Cwd   string `json:"cwd"`
}

// cmdBatch dispatches many tasks CONCURRENTLY.
//
// The shell supervisor was effectively serial per tick: one transition, then
// stop. Concurrency here is bounded on purpose -- see dispatch.DefaultConcurrency
// for why the cap exists and what happened without one.
func cmdBatch(argv []string) int {
	fs := flag.NewFlagSet("batch", flag.ExitOnError)
	var (
		lp      = fs.String("ledger", defaultLedger(), "path to ledger.sqlite3")
		file    = fs.String("file", "", "JSON-lines file of {task,lane,brief,cwd} (required)")
		model   = fs.String("model", "sonnet", "model")
		bin     = fs.String("bin", "claude", "agent binary")
		workers = fs.Int("workers", 0, "max concurrent agents (0 = cores-2, capped 8)")
		timeout = fs.Duration("timeout", agent.DefaultTimeout, "per-turn timeout")
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
		jobs = append(jobs, dispatch.Job{
			TaskID: s.Task, Lane: s.Lane, Brief: pre + string(body), Cwd: cwd,
		})
	}
	if len(jobs) == 0 {
		fmt.Fprintln(os.Stderr, "supervisord batch: no jobs parsed")
		return 2
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
	outs := dispatch.RunPool(ctx, l, func(j dispatch.Job) *agent.Claude {
		return &agent.Claude{Bin: *bin, Model: *model, Cwd: j.Cwd, Timeout: *timeout, StrictMCP: true}
	}, jobs, w)

	ok, bad := 0, 0
	for _, o := range outs {
		if o.Err != nil {
			bad++
			fmt.Printf("  FAIL %-24s %v\n", o.TaskID, o.Err)
			continue
		}
		ok++
		fmt.Printf("  ok   %-24s %s turns=%d\n", o.TaskID, o.Elapsed.Round(time.Millisecond), o.Turns)
	}
	fmt.Printf("batch: %d ok, %d failed, wall %s\n", ok, bad, time.Since(start).Round(time.Millisecond))
	if bad > 0 {
		return 1
	}
	return 0
}
