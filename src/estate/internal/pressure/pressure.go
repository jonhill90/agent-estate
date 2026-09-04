// Package pressure decides whether this host can take more work.
//
// Four independent limits, all of which must pass: load per core, free
// memory, SWAP IN USE, and lanes actually in flight. Every one fails CLOSED -- if a limit
// cannot be measured, the answer is refuse, never allow. The old supervisor
// had three separate pressure checks that did not consult each other and a
// session cap that the dispatchers actually in use never called; the host
// needed a hard restart twice.
package pressure

import (
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/quota"
)

type Limits struct {
	MaxLoadPerCore       float64
	MinFreeMemMB         float64
	MaxSwapoutsPerSample float64
	MaxWorktrees         int
	MaxInFlight          int
}

func Default() Limits {
	return Limits{MaxLoadPerCore: 3.0, MinFreeMemMB: 512, MaxSwapoutsPerSample: 1, MaxWorktrees: 40, MaxInFlight: 6}
}

type Reading struct {
	LoadPerCore     float64
	FreeMemMB       float64
	SwapoutRate     float64
	Worktrees       int
	InFlight        int
	WeeklyRemaining float64
}

type Verdict struct {
	OK      bool
	Reasons []string
	Reading Reading
}

func loadPerCore() (float64, error) {
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return 0, fmt.Errorf("read loadavg: %w", err)
	}
	f := strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{}"))
	if len(f) == 0 {
		return 0, fmt.Errorf("read loadavg: no fields in %q", string(out))
	}
	l, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parse loadavg %q: %w", f[0], err)
	}
	n := runtime.NumCPU()
	if n == 0 {
		return 0, fmt.Errorf("cpu count is zero")
	}
	return l / float64(n), nil
}

// freeMemMB reports genuinely available RAM.
//
// This read `sysctl -n vm.swapusage` until 2026-09-02 -- SWAP, not memory. On
// a Mac with swap disabled that is 0.00M forever, so the floor was breached on
// every call and `estate dispatch` refused 100% of dispatches on an 18 GB
// machine while reporting "free memory 0MB", which reads as host pressure
// rather than a broken instrument. On a host WITH a large swap file the same
// bug reports gigabytes free while RAM is exhausted -- fail-open, in the guard
// whose whole purpose is preventing a meltdown.
//
// Available memory is free + inactive + speculative pages: inactive pages are
// reclaimable on demand, so counting only "free" would under-report badly and
// reintroduce a permanent refusal by a different route.
func freeMemMB() (float64, error) {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, fmt.Errorf("read vm_stat: %w", err)
	}
	pageSize := 4096.0
	if m := regexp.MustCompile(`page size of (\d+) bytes`).FindSubmatch(out); m != nil {
		if v, err := strconv.ParseFloat(string(m[1]), 64); err == nil && v > 0 {
			pageSize = v
		}
	}
	pages := func(label string) (float64, error) {
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(label) + `:\s+(\d+)\.`)
		m := re.FindSubmatch(out)
		if m == nil {
			return 0, fmt.Errorf("vm_stat has no %q line", label)
		}
		return strconv.ParseFloat(string(m[1]), 64)
	}
	free, err := pages("Pages free")
	if err != nil {
		return 0, err
	}
	inactive, err := pages("Pages inactive")
	if err != nil {
		return 0, err
	}
	spec, err := pages("Pages speculative")
	if err != nil {
		spec = 0 // absent on some versions; not fatal, the other two carry it
	}
	return (free + inactive + spec) * pageSize / (1024 * 1024), nil
}

// Host reports whether the HOST ITSELF is healthy enough to carry more work
// -- load, free memory and active paging -- and nothing else. No ledger, no
// quota, no worktree count.
//
// WHY THIS IS SEPARATE FROM Check. Check answers "may the estate dispatch
// another turn", which is a question about the estate as well as the machine:
// it refuses at the in-flight cap and at the worktree ceiling. A caller that
// is ALREADY the in-flight turn -- a benchmark harness watching its own
// worker, cmd/dispatchbench -- would be refused by its own existence if it
// asked Check, and would then have to grow a second reader of vm_stat to get
// an answer. A second reader is exactly how the estate ends up with two
// gauges that disagree, which is the failure #999 was.
//
// Same discipline as Check, because it is the same code: every limit fails
// CLOSED. A reading that cannot be taken is OK=false with the failure named.
// Reading.Worktrees, InFlight and WeeklyRemaining are left zero here and are
// NOT claims that those are zero -- a caller that needs them must call Check.
func Host(lim Limits) Verdict {
	v := Verdict{OK: true}
	hostLimits(&v, lim)
	return v
}

// Check reports whether more work may be dispatched. Any measurement failure
// produces OK=false with the failure named -- blindness is not capacity.
func Check(l *ledger.Ledger, lim Limits) Verdict {
	v := Verdict{OK: true}

	hostLimits(&v, lim)

	// WORKTREES ARE A BACKSTOP, not a resource reading. On 2026-09-03 the estate
	// reached 176 dispatch worktrees -- one per turn, never removed, because
	// nothing cleaned up at a terminal state. That alone did not exhaust RAM
	// (worktrees are disk), but an unbounded count is the visible symptom of an
	// unbounded loop and it degrades every git operation the estate performs.
	// This limit exists so a runaway is refused even when the memory gauge is
	// wrong, which it was.
	if n, err := worktreeCount(); err != nil {
		v.OK = false
		v.Reasons = append(v.Reasons, "could not count worktrees: "+err.Error())
	} else {
		v.Reading.Worktrees = n
		if n > lim.MaxWorktrees {
			v.OK = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("%d worktrees above ceiling %d -- terminal turns are not cleaning up", n, lim.MaxWorktrees))
		}
	}

	// Budget is a limit like any other, and the one whose blindness actually
	// cost a week. A reading that cannot be taken refuses.
	if r, err := quota.Read(time.Now()); err != nil {
		v.OK = false
		v.Reasons = append(v.Reasons, "could not measure token budget: "+err.Error())
	} else {
		v.Reading.WeeklyRemaining = r.WeeklyRemaining()
		if ok, why := quota.Allow(r); !ok {
			v.OK = false
			v.Reasons = append(v.Reasons, why)
		}
	}

	inflight, err := l.InFlight()
	if err != nil {
		v.OK = false
		v.Reasons = append(v.Reasons, "could not read ledger: "+err.Error())
	} else {
		v.Reading.InFlight = len(inflight)
		if len(inflight) >= lim.MaxInFlight {
			v.OK = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("%d lanes in flight, cap is %d", len(inflight), lim.MaxInFlight))
		}
	}
	return v
}

// hostLimits applies the three limits that are about the machine rather than
// the estate, in the order Check has always applied them, so Host and Check
// cannot drift into two different readings of the same host.
func hostLimits(v *Verdict, lim Limits) {
	if lpc, err := loadPerCore(); err != nil {
		v.OK = false
		v.Reasons = append(v.Reasons, "could not measure load: "+err.Error())
	} else {
		v.Reading.LoadPerCore = lpc
		if lpc >= lim.MaxLoadPerCore {
			v.OK = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("load %.2f/core at or above limit %.2f", lpc, lim.MaxLoadPerCore))
		}
	}

	if mb, err := freeMemMB(); err != nil {
		v.OK = false
		v.Reasons = append(v.Reasons, "could not measure memory: "+err.Error())
	} else {
		v.Reading.FreeMemMB = mb
		if mb < lim.MinFreeMemMB {
			v.OK = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("free memory %.0fMB below floor %.0fMB", mb, lim.MinFreeMemMB))
		}
	}

	// ACTIVE PAGING is the limit freeMemMB cannot see, and its absence cost the
	// host on 2026-09-03. freeMemMB counts free+inactive+speculative as
	// available -- defensible, since inactive pages are reclaimable -- but on a
	// host already paging, those pages are exactly what the OS is fighting
	// over. The gate read "free 4835MB, within limits" with 332MB genuinely
	// free; dispatch continued, memory ran out, macOS killed the tmux server,
	// and the Director and the operator's own session died with it.
	//
	// The FIRST fix for this gated on `vm.swapusage used` and was wrong in the
	// opposite direction: `used` is a HIGH-WATER MARK. macOS does not page back
	// in eagerly, so an hour after the crisis the host read 619MB used while
	// `memory_pressure` reported 52% free and Swapouts had not moved in five
	// seconds. That gate would have refused dispatch forever on a healthy
	// machine -- the same permanent-refusal bug as the original vm.swapusage
	// read, reintroduced by its own fix.
	//
	// Swapouts is a cumulative counter, so the DELTA over a short sample is the
	// only honest question: is this host paging right NOW. A swap-disabled host
	// never increments it and passes correctly.
	if rate, err := swapoutRate(swapSampleWindow, swapouts); err != nil {
		v.OK = false
		v.Reasons = append(v.Reasons, "could not measure paging: "+err.Error())
	} else {
		v.Reading.SwapoutRate = rate
		if rate >= lim.MaxSwapoutsPerSample {
			v.OK = false
			v.Reasons = append(v.Reasons, fmt.Sprintf("%.0f swapouts during a %s sample -- host is actively paging", rate, swapSampleWindow))
		}
	}
}

const swapSampleWindow = 2 * time.Second

// SampleWindow is the window Reading.SwapoutRate is measured over. Callers
// that print the reading must print this with it: SwapoutRate is pages PER
// WINDOW, not per second, and the number is meaningless without the window it
// was counted in.
func SampleWindow() time.Duration { return swapSampleWindow }

// swapoutRate reports how many pages the host swapped OUT during the window.
//
// Cumulative counters sampled twice: the delta answers "is it paging now",
// where `vm.swapusage used` only answers "has it ever paged badly". Zero on a
// swap-disabled host, which is a true reading rather than a broken instrument.
//
// `sample` is a parameter rather than a direct `swapouts()` call so the delta
// arithmetic can be driven from a test without a paging host. Without that
// seam the function had no coverage at all: a mutant returning a constant 0 --
// the 2026-09-03 fail-open, restored -- left the whole suite green.
func swapoutRate(window time.Duration, sample func() (float64, error)) (float64, error) {
	a, err := sample()
	if err != nil {
		return 0, err
	}
	time.Sleep(window)
	b, err := sample()
	if err != nil {
		return 0, err
	}
	// A counter that went BACKWARDS is a failed measurement, not a quiet host.
	// This returned 0 -- the PASSING value -- until #999's review pointed out
	// that a fail-open branch in a fail-closed function is the exact shape of
	// the bug this file exists to close. Realistically unreachable (two vm_stat
	// reads two seconds apart cannot straddle a reboot inside one process), so
	// refusing here costs nothing on a healthy host and makes the package
	// header's claim -- every limit fails closed -- literally true.
	if b < a {
		return 0, fmt.Errorf("swapout counter went backwards (%.0f then %.0f); the reading cannot be trusted", a, b)
	}
	return b - a, nil
}

func swapouts() (float64, error) {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, fmt.Errorf("read vm_stat: %w", err)
	}
	return parseSwapouts(out)
}

func parseSwapouts(out []byte) (float64, error) {
	m := regexp.MustCompile(`(?mi)^Swapouts:\s+(\d+)`).FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("vm_stat has no Swapouts line")
	}
	return strconv.ParseFloat(string(m[1]), 64)
}

// worktreeCount reports how many git worktrees this repository has.
//
// Deliberately counts every worktree rather than only the stale ones: a turn
// that legitimately holds a worktree still consumes the budget, so a repo full
// of live work refuses new dispatch just as a repo full of garbage does. The
// operator can tell the two apart; the gate does not need to.
func worktreeCount() (int, error) {
	out, err := exec.Command("git", "worktree", "list", "--porcelain").Output()
	if err != nil {
		return 0, fmt.Errorf("git worktree list: %w", err)
	}
	return countWorktreeLines(out), nil
}

func countWorktreeLines(out []byte) int {
	n := 0
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(ln, "worktree ") {
			n++
		}
	}
	return n
}
