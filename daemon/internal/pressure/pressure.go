// Package pressure refuses to spawn when the HOST cannot take another agent.
//
// STOLEN FROM: gastown internal/daemon/pressure.go — three tiers, load per
// core and free memory, checked before every spawn.
//
// WHY THIS EXISTS HERE: on 2026-08-21 the shell supervisor ran ~26 concurrent
// agents, drove 1-minute load to 27 and swap to 8.7GB of 10.2GB, and made the
// operator's Mac unresponsive to typing. Twice. He had to restart the machine.
// A supervisor that makes its operator's machine unusable has failed, whatever
// it merged.
//
// ONE DELIBERATE DIVERGENCE FROM GASTOWN: their thresholds default to zero,
// which disables the check entirely. Ours default ON. A protection that ships
// disabled did not protect anyone, and this estate has the receipts.
package pressure

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type Limits struct {
	MaxLoadPerCore float64 // 0 disables
	MinFreeMemGB   float64 // 0 disables
}

// Default is deliberately protective. 3.0 load/core is gastown's own
// documented recommendation; 1.5GB free is above the 3%-free state the Mac
// was in when it stopped responding.
func Default() Limits { return Limits{MaxLoadPerCore: 3.0, MinFreeMemGB: 1.5} }

type Result struct {
	OK          bool
	LoadPerCore float64
	FreeMemGB   float64
	Reason      string
}

func (r Result) String() string {
	return fmt.Sprintf("load/core=%.2f freeMem=%.2fGB ok=%v %s", r.LoadPerCore, r.FreeMemGB, r.OK, r.Reason)
}

// Check reads the live host. An unreadable metric is NOT treated as healthy --
// the could-not-measure rule this estate learned the hard way: a check that
// cannot see must not report clean.
func Check(l Limits) Result {
	r := Result{OK: true}

	if l.MaxLoadPerCore > 0 {
		load, err := load1()
		if err != nil {
			return Result{OK: false, Reason: "could not read load average: " + err.Error()}
		}
		r.LoadPerCore = load / float64(runtime.NumCPU())
		if r.LoadPerCore >= l.MaxLoadPerCore {
			r.OK = false
			r.Reason = fmt.Sprintf("load/core %.2f >= %.2f", r.LoadPerCore, l.MaxLoadPerCore)
			return r
		}
	}

	if l.MinFreeMemGB > 0 {
		free, err := freeMemGB()
		if err != nil {
			return Result{OK: false, LoadPerCore: r.LoadPerCore,
				Reason: "could not read free memory: " + err.Error()}
		}
		r.FreeMemGB = free
		if free < l.MinFreeMemGB {
			r.OK = false
			r.Reason = fmt.Sprintf("free memory %.2fGB < %.2fGB", free, l.MinFreeMemGB)
			return r
		}
	}
	r.Reason = "within limits"
	return r
}

func load1() (float64, error) {
	var raw [3]float64
	if n, err := getloadavg(&raw); err == nil && n > 0 {
		return raw[0], nil
	}
	out, err := exec.Command("/usr/bin/uptime").Output()
	if err != nil {
		return 0, err
	}
	s := string(out)
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return 0, fmt.Errorf("unparseable uptime: %q", s)
	}
	f := strings.FieldsFunc(s[i+1:], func(r rune) bool { return r == ',' || r == ' ' })
	if len(f) == 0 {
		return 0, fmt.Errorf("unparseable load: %q", s)
	}
	return strconv.ParseFloat(strings.TrimSpace(f[0]), 64)
}

// freeMemGB uses vm_stat on darwin: free + inactive + speculative pages.
func freeMemGB() (float64, error) {
	out, err := exec.Command("/usr/bin/vm_stat").Output()
	if err != nil {
		return 0, err
	}
	pageSize := float64(syscall.Getpagesize())
	var pages float64
	for _, line := range strings.Split(string(out), "\n") {
		for _, k := range []string{"Pages free:", "Pages inactive:", "Pages speculative:"} {
			if strings.HasPrefix(line, k) {
				v := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, k)), ".")
				n, perr := strconv.ParseFloat(v, 64)
				if perr != nil {
					return 0, perr
				}
				pages += n
			}
		}
	}
	if pages == 0 {
		return 0, fmt.Errorf("vm_stat returned no usable page counts")
	}
	return pages * pageSize / (1024 * 1024 * 1024), nil
}
