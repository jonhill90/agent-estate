// Package claim closes a real collision gap in the Go daemon: before this
// package existed, daemon/internal/ledger wrote lanes/tasks rows directly
// via raw SQL (INSERT ... ON CONFLICT DO UPDATE, plain UPDATE) with no
// claim/lease logic at all (`grep -rn "claim" daemon/` returned zero hits).
// The tmux/cli.py side of this estate already has one --
// scripts/supervisor/claim.sh -- built after issue #28 was dispatched
// TWICE, once by the Director and once by the supervisor, because nothing
// recorded a claim: both lanes did the full work (one PR merged, one
// closed, about an hour spent twice). The Go daemon dispatching to a lane
// the tmux/cli.py side also thinks it owns is the same incident again,
// just with a second dispatcher written in Go instead of a second shell
// script.
package claim

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Gate takes and releases a claim on a GitHub issue before/after this
// daemon dispatches to it. nil (on dispatch.Gates.Claim) disables the
// check entirely -- not every task this daemon dispatches corresponds to
// a real GitHub issue (supervisord's own -issue flag is optional), and an
// issue number of 0 at the call site already means "nothing to claim";
// Gate is only ever asked about issues that DO have a number.
type Gate interface {
	Take(ctx context.Context, issue int, repo, lane string) error
	Release(ctx context.Context, issue int, repo string) error
}

// ScriptGate shells out to scripts/supervisor/claim.sh -- the SAME tested
// claim mechanism the tmux/cli.py side of this estate already uses
// (GitHub issue assignee; see claim.sh's own header comment), reused
// rather than reimplemented in Go. claim.sh's check/take/release/list
// verbs are pure `gh api` calls with no tmux or ledger dependency at all
// (confirmed by reading them, 2026-08-22) -- only its OWN `stale`/`audit`/
// `reap` verbs, which this package does not call, read tmux/ledger
// liveness signals. That is what makes shelling out here architecturally
// clean rather than a Go program depending on tmux through the back door.
//
// claim.sh's own header is explicit about what its GitHub-assignee
// mechanism does NOT close: "Two dispatchers that both read 'unassigned'
// within the same second will both write and both succeed" -- there is no
// compare-and-swap on a GitHub issue, and this package does not invent
// one. mu below closes a DIFFERENT, narrower gap: two goroutines in THIS
// SAME daemon process dispatching the same issue concurrently (the
// realistic shape of the race a bounded worker pool -- RunPoolGated --
// can actually produce). It cannot and does not close the cross-process
// or cross-machine window claim.sh's own comment already names and
// accepts; that residual sub-second gap is unchanged by this package.
type ScriptGate struct {
	// ScriptPath is claim.sh's own path. Required -- a ScriptGate with an
	// empty ScriptPath refuses every call rather than silently no-opping
	// (the "disable claim checking" seam is dispatch.Gates.Claim == nil,
	// one level up; a caller that constructed a ScriptGate meant to use
	// one).
	ScriptPath string

	mu    sync.Mutex
	locks map[int]*sync.Mutex
}

var _ Gate = (*ScriptGate)(nil)

func (g *ScriptGate) lockFor(issue int) *sync.Mutex {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.locks == nil {
		g.locks = make(map[int]*sync.Mutex)
	}
	l, ok := g.locks[issue]
	if !ok {
		l = &sync.Mutex{}
		g.locks[issue] = l
	}
	return l
}

// Take claims issue in repo (OWNER/NAME; empty lets claim.sh resolve it
// from the working directory, same as calling it by hand) for lane.
// Non-nil error means REFUSE to dispatch -- claim.sh exits 1 when the
// issue is already claimed or not open, and 2 when it could not read the
// issue's state at all (rate limit, network); both are refusals here,
// distinguished only in the error text for whoever reads the daemon's own
// log, never treated as "proceed anyway."
func (g *ScriptGate) Take(ctx context.Context, issue int, repo, lane string) error {
	l := g.lockFor(issue)
	l.Lock()
	defer l.Unlock()
	return g.run(ctx, "take", issue, repo, lane)
}

// Release drops the claim on issue. Called from the same terminal paths
// claim.sh's own header says every OTHER caller in this estate already
// uses (dispatch.sh's pre-send aborts, cli.py's completion paths) --
// dispatch.RunGated calls this from both of ITS terminal stamps (success
// and failure), never from the "left non-terminal on timeout" path, so a
// task whose outcome is still genuinely unknown does not have its claim
// dropped out from under it while a lane may still be working.
func (g *ScriptGate) Release(ctx context.Context, issue int, repo string) error {
	l := g.lockFor(issue)
	l.Lock()
	defer l.Unlock()
	return g.run(ctx, "release", issue, repo, "")
}

func (g *ScriptGate) run(ctx context.Context, verb string, issue int, repo, lane string) error {
	if g.ScriptPath == "" {
		return fmt.Errorf("claim: no ScriptPath configured -- refusing rather than silently allowing")
	}
	args := []string{verb, strconv.Itoa(issue), repo}
	if lane != "" {
		args = append(args, lane)
	}
	_, err := execCombined(ctx, g.ScriptPath, args)
	if err != nil {
		return fmt.Errorf("claim %s #%d: %v", verb, issue, err)
	}
	return nil
}

// execCombined runs path with args and returns its combined stdout+stderr,
// trimmed. Factored out of run() so this package's own tests can drive the
// exact same subprocess call run() does -- without going through
// ScriptGate.Take/Release's per-issue lock -- to reproduce claim.sh's own
// documented race deliberately (see claim_test.go's mutation-check pair).
func execCombined(ctx context.Context, path string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("%v: %s", err, text)
	}
	return text, nil
}
