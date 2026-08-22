package monitor

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// HostRunner retrieves a real Host reading. ExecHostRunner is the only
// implementation this package ships -- it shells out to `uptime`/`ps`
// (both platforms) and `sysctl`(darwin)/`/proc/meminfo`(linux) for swap,
// never fabricating a figure any of those cannot supply: each sub-read
// below is independent and leaves its own Host field's Known bit false on
// any failure, rather than failing the whole call.
type HostRunner func() (Host, error)

// ExecHostRunner builds a HostRunner using this host's own commands/proc
// files -- real machine reads. Cores is filled from runtime.NumCPU()
// in-process (Host's own doc comment on why that field carries no Known
// bit); every other field comes from a subprocess or file read that can,
// and does, fail independently on a machine missing one of these tools
// (e.g. a container with no `uptime`).
func ExecHostRunner() HostRunner {
	return func() (Host, error) {
		h := Host{Cores: runtime.NumCPU()}
		h.LoadAvg1, h.LoadAvg5, h.LoadAvg15 = readLoadAvg()
		h.SwapUsedPercent = readSwap()
		h.ClaudeProcesses = readClaudeProcesses()
		return h, nil
	}
}

// uptimeLoadRE matches uptime's own "load average(s): N, N, N" trailer --
// deliberately tolerant of both the comma-separated form (Linux's
// procps-ng uptime) and the plain-space form (macOS/BSD uptime), since
// this package runs on both (AGENTS.md's CI is ubuntu-latest; Jon's own
// machine is darwin).
var uptimeLoadRE = regexp.MustCompile(`load averages?:\s*([\d.]+),?\s+([\d.]+),?\s+([\d.]+)`)

func readLoadAvg() (Figure, Figure, Figure) {
	out, err := exec.Command("uptime").Output()
	if err != nil {
		return Figure{}, Figure{}, Figure{}
	}
	m := uptimeLoadRE.FindStringSubmatch(string(out))
	if m == nil {
		return Figure{}, Figure{}, Figure{}
	}
	var figs [3]Figure
	for i, s := range m[1:] {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			figs[i] = KnownFigure(v)
		}
	}
	return figs[0], figs[1], figs[2]
}

// darwinSwapRE matches `sysctl -n vm.swapusage`'s own
// "total = N.NNM  used = N.NNM  free = N.NNM  (encrypted)" line.
var darwinSwapRE = regexp.MustCompile(`total\s*=\s*([\d.]+)M\s+used\s*=\s*([\d.]+)M`)

// readSwap reads used/total swap as a percentage. darwin has no
// /proc/meminfo, so it shells out to sysctl; every other platform this
// package expects to run on (linux, per AGENTS.md's ubuntu-latest CI)
// reads /proc/meminfo directly, no subprocess needed. The actual parsing
// is factored into parseDarwinSwap/parseLinuxMeminfo below so it is
// testable against captured real output, not only exercised live.
func readSwap() Figure {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("sysctl", "-n", "vm.swapusage").Output()
		if err != nil {
			return Figure{}
		}
		return parseDarwinSwap(string(out))
	}

	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return Figure{}
	}
	return parseLinuxMeminfo(string(data))
}

func parseDarwinSwap(out string) Figure {
	m := darwinSwapRE.FindStringSubmatch(out)
	if m == nil {
		return Figure{}
	}
	total, errT := strconv.ParseFloat(m[1], 64)
	used, errU := strconv.ParseFloat(m[2], 64)
	if errT != nil || errU != nil {
		return Figure{}
	}
	if total == 0 {
		// macOS's dynamic swap file genuinely reports "total = 0.00M" when
		// no swap has been allocated yet (observed live on this machine,
		// not assumed) -- that is a real, known answer ("no swap in use"),
		// not a failed read. KnownFigure(0), not Figure{}: this package's
		// whole "unknown, not zero" rule cuts both ways -- a genuine zero
		// must not be reported as unknown either.
		return KnownFigure(0)
	}
	return KnownFigure(used / total * 100)
}

func parseLinuxMeminfo(data string) Figure {
	var total, free float64
	haveTotal, haveFree := false, false
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "SwapTotal:":
			if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
				total, haveTotal = v, true
			}
		case "SwapFree:":
			if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
				free, haveFree = v, true
			}
		}
	}
	if !haveTotal || !haveFree {
		return Figure{}
	}
	if total == 0 {
		// A container with no swap configured at all reports SwapTotal: 0
		// -- a real "no swap" answer, not a failed read (parseDarwinSwap's
		// own doc comment documents the same case for macOS's dynamic swap
		// file).
		return KnownFigure(0)
	}
	return KnownFigure((total - free) / total * 100)
}

// readClaudeProcesses counts `ps aux` lines mentioning "claude"
// (case-insensitive) -- `ps aux` is the one invocation both BSD/macOS's and
// GNU's ps accept identically, avoiding the `-eo`/`-Ao` flag split between
// them (Host's own doc comment on what this figure does and does not
// claim).
func readClaudeProcesses() Count {
	out, err := exec.Command("ps", "aux").Output()
	if err != nil {
		return Count{}
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(strings.ToLower(line), "claude") {
			n++
		}
	}
	return KnownCount(n)
}
