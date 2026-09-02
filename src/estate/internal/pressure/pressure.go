// Package pressure decides whether this host can take more work.
//
// Three independent limits, all of which must pass: load per core, free
// memory, and lanes actually in flight. Every one fails CLOSED -- if a limit
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
	MaxLoadPerCore float64
	MinFreeMemMB   float64
	MaxInFlight    int
}

func Default() Limits {
	return Limits{MaxLoadPerCore: 3.0, MinFreeMemMB: 512, MaxInFlight: 6}
}

type Reading struct {
	LoadPerCore     float64
	FreeMemMB       float64
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

// Check reports whether more work may be dispatched. Any measurement failure
// produces OK=false with the failure named -- blindness is not capacity.
func Check(l *ledger.Ledger, lim Limits) Verdict {
	v := Verdict{OK: true}

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
