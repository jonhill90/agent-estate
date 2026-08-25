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

// claudeExecutableRE matches `ps aux`'s COMMAND column against the actual
// executable name, not any occurrence anywhere in argv (agent-tui#147: the
// old rule counted any line containing the substring "claude" anywhere,
// which meant the desktop app's chrome-native-host helper -- a different
// product that merely lives under /Applications/Claude.app -- a zsh
// wrapper spawned only to source ~/.claude/shell-snapshots/..., and even a
// grep/ugrep whose own arguments searched for "claude" all counted as
// agents; that last one meant the gauge moved when someone ran a search).
// "claude.exe" is included deliberately, not excluded as a Windows binary
// mismatched to this doc's platform: it is the literal executable name
// @anthropic-ai/claude-code's Node CLI installs as on this machine
// (`/opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe`,
// confirmed live via `ps aux | grep -i claude`), and every process running
// it is a real agent.
var claudeExecutableRE = regexp.MustCompile(`(?i)(^|/)claude(\.exe)?$`)

// isClaudePoolHelper reports whether a claudeExecutableRE-matched line is
// one of the background-session daemon's own pool helpers rather than an
// agent: `claude bg-spare`/`claude.exe --bg-spare` (an idle spare process
// the daemon keeps warm so a new background session doesn't pay cold-start
// cost) and `claude bg-pty-host`/`claude.exe --bg-pty-host` (the pty
// bridge a background session runs under), plus `claude(.exe) daemon run`
// (the daemon process that owns the pool itself). Named explicitly on the
// issue thread (agent-tui#147, comment at 09:37:22Z, ~90s before this
// fix's first commit) as "daemon pool infrastructure, not agents" --
// confirmed against this host's own `ps aux`, where exactly these three
// shapes appear alongside real agent lines. All three scale with the
// pool's own size (roughly constant, independent of active work) rather
// than with agents actually doing anything, so counting them inflates the
// gauge by a near-constant offset right at the halt threshold this issue
// exists to protect.
//
// Rule chosen, stated deliberately (agent-tui#147 fix-pass item 2):
// excluded by argv[1] (the subcommand token), matched with any leading
// dashes stripped so both the bare-subcommand form (`claude bg-spare ...`)
// and the flag form (`claude.exe --bg-spare ...`) match the same rule,
// never by a substring of the whole line -- the same discipline
// claudeExecutableRE already applies to argv[0]. `daemon run` DOES count
// as a pool helper here, not an agent: it hosts detached background
// sessions rather than doing work of its own, the same reasoning that
// excludes bg-spare/bg-pty-host. This is a real trade-off, stated rather
// than hidden: `daemon run` is also the process an over-eager reaper would
// want to kill to tear down the whole pool, and excluding it from *this*
// gauge does not itself protect it from that -- this fix only says it
// should not read as an agent for the halt-threshold count, not that it is
// safe to kill.
var claudePoolHelperRE = regexp.MustCompile(`(?i)^-*(bg-spare|bg-pty-host)$`)

func isClaudePoolHelper(fields []string) bool {
	if len(fields) < 12 {
		return false
	}
	if claudePoolHelperRE.MatchString(fields[11]) {
		return true
	}
	if strings.EqualFold(fields[11], "daemon") && len(fields) >= 13 && strings.EqualFold(fields[12], "run") {
		return true
	}
	return false
}

// readClaudeProcesses counts `ps aux` lines whose own executable is
// "claude"/"claude.exe" -- `ps aux` is the one invocation both BSD/macOS's
// and GNU's ps accept identically, avoiding the `-eo`/`-Ao` flag split
// between them (Host's own doc comment on what this figure does and does
// not claim). Parsing is factored into parseClaudeProcesses below so it is
// testable against a captured fixture, the same split readSwap already
// uses for parseDarwinSwap/parseLinuxMeminfo.
func readClaudeProcesses() Count {
	out, err := exec.Command("ps", "aux").Output()
	if err != nil {
		return Count{}
	}
	return parseClaudeProcesses(string(out))
}

// parseClaudeProcesses counts `ps aux` lines whose COMMAND column's
// argv[0] -- the first whitespace-delimited token, stripped of any
// leading path -- matches claudeExecutableRE. Because this reads argv[0]
// specifically and never the rest of the command line, a process whose
// *arguments* merely mention "claude" (a search for the string, a path
// under ~/.claude/) is never counted, and this package never shells a
// `grep`/`ugrep` of its own into the pipeline to begin with -- there is no
// self-match to guard against here, only the argv[0]-vs-argv confusion the
// old substring rule made (agent-tui#147's own issue thread walked back an
// earlier claim that the monitor counted its own grep; it doesn't, because
// it has none -- this comment states the corrected reasoning directly
// rather than repeating the retracted one).
func parseClaudeProcesses(out string) Count {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 11 {
			continue
		}
		if claudeExecutableRE.MatchString(fields[10]) && !isClaudePoolHelper(fields) {
			n++
		}
	}
	return KnownCount(n)
}
