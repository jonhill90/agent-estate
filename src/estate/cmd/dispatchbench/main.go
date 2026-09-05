// Command dispatchbench measures the dispatch-mode counterfactual that
// agent-estate#1002 asks for: the SAME workload run as stateless subprocess
// dispatches (what `estate dispatch` does today) and as turns in one
// persistent tmux lane (what it does not), compared on tokens, dollars where
// a first-party figure exists, peak resident memory, and wall-clock.
//
// It is a measuring instrument, not part of the daemon. `estate` does not
// call it and must not: it spends real money and puts real load on the host,
// so it is run deliberately by a person, and the numbers it produced are
// written down in docs/decisions/0001-dispatch-mode-counterfactual.md rather
// than re-derived by anyone who wonders.
//
// WHAT MAKES THIS SAFE TO RUN UNATTENDED, given that the first attempt at it
// had to be stopped by hand while the host paged 29GB:
//
//   - Concurrency of one, twice over. `runLock` refuses a second copy of this
//     binary on the host; `serial` refuses a second concurrent turn inside
//     one copy. Both are enforced in guard.go and driven by guard_test.go.
//   - The floor is checked by this harness, never by the worker. monitor.go
//     samples free memory and active paging through the estate's own
//     pressure.Host and cancels the run's context on a breach. The worker has
//     no vote in whether it keeps running.
//   - Every worker is a child of this process or a pane on a private tmux
//     socket, and both are torn down on every exit path.
//
// Usage:
//
//	go run ./cmd/dispatchbench -turns 10 -out /tmp/bench
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/harness"
)

type turnResult struct {
	Arm      string `json:"arm"`
	Turn     int    `json:"turn"`
	Prompt   string `json:"prompt"`
	WallMS   int64  `json:"wall_ms"`
	Answered bool   `json:"answered"`
	Error    string `json:"error,omitempty"`

	// CostUSD is the harness's OWN dollar figure and is nil when the harness
	// states none. The persistent arm has no such figure per turn: an
	// interactive session emits no cost envelope, and this program will not
	// invent one by multiplying tokens against a price table -- the same
	// refusal internal/harness makes for codex.
	CostUSD *float64 `json:"cost_usd"`

	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	TokenSource         string `json:"token_source"`

	Memory aggregate `json:"memory"`
}

// CachedShare is the fraction of this turn's PROMPT tokens that were served
// from cache. Fresh input and cache creation are both bought; cache reads are
// the cheap ones. This is the ratio #1002 says is the finding if it degrades
// as a persistent lane's context grows.
func (r turnResult) CachedShare() (float64, bool) {
	total := r.InputTokens + r.CacheReadTokens + r.CacheCreationTokens
	if total == 0 {
		return 0, false
	}
	return float64(r.CacheReadTokens) / float64(total), true
}

type runResult struct {
	StartedAt   string       `json:"started_at"`
	FinishedAt  string       `json:"finished_at"`
	Turns       int          `json:"turns_requested"`
	Workload    string       `json:"workload"`
	Limits      benchLimits  `json:"limits"`
	Model       string       `json:"model"`
	StoppedWhy  string       `json:"stopped_why,omitempty"`
	Results     []turnResult `json:"results"`
	HostOverall aggregate    `json:"host_overall"`
	LaneCost    string       `json:"lane_cost_report,omitempty"`
}

func main() {
	var (
		turns    = flag.Int("turns", 10, "turns per arm")
		armsFlag = flag.String("arms", "stateless,persistent", "which arms to run, in order")
		outDir   = flag.String("out", "", "directory for results (default: a fresh temp dir)")
		lockPath = flag.String("lock", filepath.Join(os.TempDir(), "estate-dispatchbench.lock"), "run lock path")
		hold     = flag.Duration("hold", 0, "take the run lock, sleep, exit -- for guard_test.go only")
		floorMB  = flag.Float64("floor-free-mb", defaultBenchLimits().MinFreeMemMB, "abort below this much free memory")
		maxSwap  = flag.Float64("max-swapouts", defaultBenchLimits().MaxSwapoutsPerSample, "abort at or above this many swapouts per 2s sample")
		maxRSS   = flag.Float64("max-worker-mb", defaultBenchLimits().MaxWorkerRSSMB, "abort if one worker's process tree exceeds this")
		timeout  = flag.Duration("turn-timeout", 4*time.Minute, "give up on a single turn after this long")
		ctxFile  = flag.String("context-file", "", "file copied into each scratch dir as CLAUDE.md, so the cached prefix is production-shaped")
		model    = flag.String("model", "", "pass --model to both arms (default: the harness's own default)")
		keep     = flag.Bool("keep", false, "leave the scratch dirs and session transcripts in place, for cross-checking one source of numbers against another")
	)
	flag.Parse()

	if err := run(*turns, *armsFlag, *outDir, *lockPath, *hold, benchLimits{
		MinFreeMemMB:         *floorMB,
		MaxSwapoutsPerSample: *maxSwap,
		MaxWorkerRSSMB:       *maxRSS,
	}, *timeout, *ctxFile, *model, *keep); err != nil {
		fmt.Fprintln(os.Stderr, "dispatchbench: "+err.Error())
		os.Exit(1)
	}
}

func run(turns int, armsFlag, outDir, lockPath string, hold time.Duration, lim benchLimits, timeout time.Duration, ctxFile, model string, keep bool) error {
	lock, err := acquireRunLock(lockPath)
	if err != nil {
		return err
	}
	defer lock.Release()

	if hold > 0 {
		time.Sleep(hold)
		return nil
	}
	if turns < 1 {
		return fmt.Errorf("-turns must be at least 1")
	}

	if outDir == "" {
		outDir, err = os.MkdirTemp("", "dispatchbench-out-")
		if err != nil {
			return err
		}
	} else if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	fmt.Printf("results:  %s\n", outDir)

	mon, err := newMonitor(filepath.Join(outDir, "samples.jsonl"), lim)
	if err != nil {
		return err
	}

	// One context for the whole run. The monitor holds its cancel func and is
	// the only caller of it; a floor breach therefore kills the current turn
	// AND stops every later one, without the run loop having to check.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mon.Start(cancel)

	// Ctrl-C is the operator doing by hand what the monitor does
	// automatically, and must tear down the same way.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigc
		mon.abort("interrupted by signal")
	}()

	if err := mon.Preflight(); err != nil {
		mon.Close()
		return fmt.Errorf("refusing to start: %w", err)
	}

	res := runResult{
		StartedAt: nowStamp(),
		Turns:     turns,
		Limits:    lim,
		Model:     model,
		Workload:  workloadDescription(turns),
	}

	var s serial
	prompts := workloadPrompts(turns)

	for _, arm := range strings.Split(armsFlag, ",") {
		arm = strings.TrimSpace(arm)
		if arm == "" {
			continue
		}
		var armRes []turnResult
		var laneCost string
		switch arm {
		case "stateless":
			armRes, err = runStateless(ctx, &s, mon, prompts, timeout, ctxFile, model, keep)
		case "persistent":
			armRes, laneCost, err = runPersistent(ctx, &s, mon, prompts, timeout, ctxFile, model, keep)
			res.LaneCost = laneCost
		default:
			mon.Close()
			return fmt.Errorf("unknown arm %q", arm)
		}
		res.Results = append(res.Results, armRes...)
		if err != nil {
			res.StoppedWhy = fmt.Sprintf("arm %q stopped: %v", arm, err)
			break
		}
	}
	if why := mon.AbortReason(); why != "" {
		res.StoppedWhy = why
	}

	res.HostOverall = mon.Close()
	res.FinishedAt = nowStamp()

	b, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, "results.json"), b, 0o644); err != nil {
		return err
	}
	summary := summarise(res)
	if err := os.WriteFile(filepath.Join(outDir, "summary.md"), []byte(summary), 0o644); err != nil {
		return err
	}
	fmt.Print("\n" + summary)
	if res.StoppedWhy != "" {
		return fmt.Errorf("run did not complete: %s", res.StoppedWhy)
	}
	return nil
}

// --- the stateless arm -------------------------------------------------

// runStateless is what `estate dispatch` does today, and it runs through
// internal/harness rather than a copy of it, so what is measured is the
// production path and not a lookalike.
func runStateless(ctx context.Context, s *serial, mon *monitor, prompts []string, timeout time.Duration, ctxFile, model string, keep bool) ([]turnResult, error) {
	dir, cleanup, err := scratchDir("stateless", len(prompts), ctxFile)
	if err != nil {
		return nil, err
	}
	if keep {
		fmt.Printf("  keeping stateless scratch dir: %s\n", dir)
	} else {
		defer cleanup()
	}

	h, err := harness.Lookup("claude")
	if err != nil {
		return nil, err
	}

	var out []turnResult
	for i, prompt := range prompts {
		if err := ctx.Err(); err != nil {
			return out, fmt.Errorf("run cancelled before turn %d", i+1)
		}
		if err := mon.Preflight(); err != nil {
			return out, err
		}

		r := turnResult{Arm: "stateless", Turn: i + 1, Prompt: prompt, TokenSource: "claude -p --output-format json envelope"}
		err := s.Do(func() error {
			tctx, tcancel := context.WithTimeout(ctx, timeout)
			defer tcancel()
			turn, err := h.Start(tctx, dir, prompt)
			if err != nil {
				return err
			}
			defer turn.Cleanup()
			if model != "" {
				turn.Cmd.Args = append(turn.Cmd.Args, "--model", model)
			}
			// stdout is a `claude -p` envelope -- kilobytes -- and
			// internal/harness's parsers take a []byte, so it is held in
			// memory deliberately. Nothing else in this program does.
			var stdout, stderr bytes.Buffer
			turn.Cmd.Stdout = &stdout
			turn.Cmd.Stderr = &stderr
			start := time.Now()
			if err := turn.Cmd.Start(); err != nil {
				return err
			}
			mon.Watch("stateless", i+1, turn.Cmd.Process.Pid)
			waitErr := turn.Cmd.Wait()
			r.WallMS = time.Since(start).Milliseconds()
			mon.Watch("stateless", i+1, 0)
			if waitErr != nil {
				return fmt.Errorf("%w: %s", waitErr, strings.TrimSpace(stderr.String()))
			}
			if _, err := turn.Result(stdout.Bytes()); err != nil {
				return err
			}
			r.Answered = true
			sp, err := turn.Spend(stdout.Bytes())
			if err != nil {
				return fmt.Errorf("turn answered but its spend envelope was unreadable: %w", err)
			}
			r.CostUSD = sp.CostUSD
			r.InputTokens = deref(sp.InputTokens)
			r.OutputTokens = deref(sp.OutputTokens)
			r.CacheReadTokens = deref(sp.CacheReadTokens)
			r.CacheCreationTokens = deref(sp.CacheCreationTokens)
			return nil
		})
		if err != nil {
			r.Error = err.Error()
		}
		r.Memory = mon.Aggregate("stateless", i+1)
		out = append(out, r)
		fmt.Printf("  stateless turn %d/%d  %s\n", i+1, len(prompts), oneLine(r))
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// --- the persistent arm ------------------------------------------------

func runPersistent(ctx context.Context, s *serial, mon *monitor, prompts []string, timeout time.Duration, ctxFile, model string, keep bool) ([]turnResult, string, error) {
	dir, cleanup, err := scratchDir("persistent", len(prompts), ctxFile)
	if err != nil {
		return nil, "", err
	}
	if keep {
		fmt.Printf("  keeping persistent scratch dir: %s\n", dir)
	} else {
		defer cleanup()
	}

	// Short root deliberately -- see tmuxTmpdirRoots for the socket-path
	// length that forces it.
	tmpdir, err := os.MkdirTemp(tmuxTmpdirRoots()[len(tmuxTmpdirRoots())-1], "dbench-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(tmpdir)

	started := time.Now()
	var extra []string
	if model != "" {
		extra = append(extra, "--model", model)
	}
	l, err := startLane(ctx, dir, tmpdir, "dispatchbench", extra)
	if err != nil {
		return nil, "", fmt.Errorf("could not start the persistent lane: %w", err)
	}
	defer l.Close()

	var out []turnResult
	for i, prompt := range prompts {
		if err := ctx.Err(); err != nil {
			return out, "", fmt.Errorf("run cancelled before turn %d", i+1)
		}
		if err := mon.Preflight(); err != nil {
			return out, "", err
		}
		mon.Watch("persistent", i+1, l.panePid)

		r := turnResult{Arm: "persistent", Turn: i + 1, Prompt: prompt, TokenSource: "Claude Code session transcript"}
		err := s.Do(func() error {
			start := time.Now()
			if err := l.Send(ctx, prompt); err != nil {
				return err
			}
			t, err := awaitTurn(ctx, dir, started, i+1, timeout)
			r.WallMS = time.Since(start).Milliseconds()
			if err != nil {
				return err
			}
			r.Answered = true
			in, o, cr, cc := t.Totals()
			r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheCreationTokens = in, o, cr, cc
			return nil
		})
		if err != nil {
			r.Error = err.Error()
		}
		r.Memory = mon.Aggregate("persistent", i+1)
		out = append(out, r)
		fmt.Printf("  persistent turn %d/%d  %s\n", i+1, len(prompts), oneLine(r))
		if err != nil {
			return out, "", err
		}
	}
	mon.Watch("persistent", 0, 0)

	// /cost is the CLI's OWN dollar figure for the session -- the same
	// client-side arithmetic `claude -p` prints as total_cost_usd, which is
	// why it is comparable with the stateless arm's number and why this
	// program does not compute one itself. It is captured verbatim from the
	// pane and reported verbatim; if it cannot be read, it is reported absent.
	cost := laneCostReport(ctx, l)
	return out, cost, nil
}

func laneCostReport(ctx context.Context, l *lane) string {
	if err := l.Send(ctx, "/cost"); err != nil {
		return ""
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		pane, err := l.Capture(ctx)
		if err != nil {
			return ""
		}
		if strings.Contains(pane, "Total cost") {
			var keep []string
			for _, ln := range strings.Split(pane, "\n") {
				t := strings.TrimSpace(ln)
				if strings.HasPrefix(t, "Total cost") || strings.HasPrefix(t, "Total duration") || strings.HasPrefix(t, "Total code changes") || strings.HasPrefix(t, "Usage by model") || strings.HasPrefix(t, "claude-") {
					keep = append(keep, t)
				}
			}
			return strings.Join(keep, "\n")
		}
	}
	return ""
}

// awaitTurn waits for the lane's own transcript to show turn n finished.
// Polling the RECORD rather than the screen: the pane is a rendering, and a
// spinner that has stopped is not the same claim as `stop_reason: end_turn`.
func awaitTurn(ctx context.Context, dir string, since time.Time, n int, timeout time.Duration) (transcriptTurn, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return transcriptTurn{}, err
		}
		if time.Now().After(deadline) {
			return transcriptTurn{}, fmt.Errorf("turn %d did not finish within %s", n, timeout)
		}
		path, err := findTranscript(dir, since)
		if err == nil {
			turns, err := parseTranscriptFile(path)
			if err == nil && len(turns) >= n && turns[n-1].Complete() {
				return turns[n-1], nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// findTranscript locates the session file Claude Code is writing for a given
// working directory. The project directory name is a mangling of the cwd that
// this program deliberately does not reimplement -- it reads each candidate's
// own recorded cwd instead, which cannot drift from whatever the CLI does.
func findTranscript(dir string, since time.Time) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	if err != nil {
		return "", err
	}
	best := ""
	var bestMod time.Time
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil || st.ModTime().Before(since) {
			continue
		}
		if !transcriptIsFor(m, dir) {
			continue
		}
		if best == "" || st.ModTime().After(bestMod) {
			best, bestMod = m, st.ModTime()
		}
	}
	if best == "" {
		return "", fmt.Errorf("no session transcript for %s yet", dir)
	}
	return best, nil
}

func transcriptIsFor(path, dir string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	head := string(buf[:n])
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		real = dir
	}
	return strings.Contains(head, `"cwd":"`+dir+`"`) || strings.Contains(head, `"cwd":"`+real+`"`)
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
