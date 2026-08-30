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
	"runtime"
	"strconv"
	"strings"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
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
	LoadPerCore float64
	FreeMemMB   float64
	InFlight    int
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

func freeMemMB() (float64, error) {
	out, err := exec.Command("sysctl", "-n", "vm.swapusage").Output()
	if err != nil {
		return 0, fmt.Errorf("read swapusage: %w", err)
	}
	// "total = 5120.00M  used = 3925.88M  free = 1194.12M  (encrypted)"
	for _, part := range strings.Split(string(out), "free =") {
		if !strings.Contains(part, "M") {
			continue
		}
		v := strings.TrimSpace(strings.SplitN(strings.TrimSpace(part), "M", 2)[0])
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, nil
		}
	}
	return 0, fmt.Errorf("parse swapusage: no free field in %q", string(out))
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
