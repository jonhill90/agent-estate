package reclaim

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PSProbe is the real Probe: it asks `ps` what it knows about a pid right
// now. It shells out rather than reading /proc because macOS has no /proc --
// matching internal/pressure's own sysctl/vm_stat calls, this estate runs on
// one OS today.
func PSProbe(pid int) (ProcessInfo, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "pid=,etime=,comm=").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// ps exits non-zero when the pid does not exist -- that is the
			// answer, not a failure to ask the question.
			return ProcessInfo{Exists: false}, nil
		}
		return ProcessInfo{}, fmt.Errorf("ps -p %d: %w", pid, err)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return ProcessInfo{Exists: false}, nil
	}
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return ProcessInfo{}, fmt.Errorf("ps -p %d: unexpected output %q", pid, line)
	}
	info := ProcessInfo{Exists: true, Comm: strings.Join(fields[2:], " ")}
	// A failure to parse etime is not fatal to the probe -- StartedAt just
	// stays zero, and Assess already treats zero as "unknown", never as
	// "just started".
	if dur, perr := parseEtime(fields[1]); perr == nil {
		info.StartedAt = time.Now().Add(-dur)
	}
	return info, nil
}

// parseEtime reads ps's own elapsed-time format: [[dd-]hh:]mm:ss.
func parseEtime(s string) (time.Duration, error) {
	days := 0
	rest := s
	if i := strings.Index(s, "-"); i >= 0 {
		d, err := strconv.Atoi(s[:i])
		if err != nil {
			return 0, fmt.Errorf("etime %q: bad day component", s)
		}
		days = d
		rest = s[i+1:]
	}
	parts := strings.Split(rest, ":")
	var h, m, sec int
	var err error
	switch len(parts) {
	case 3:
		if h, err = strconv.Atoi(parts[0]); err != nil {
			return 0, fmt.Errorf("etime %q: bad hours", s)
		}
		if m, err = strconv.Atoi(parts[1]); err != nil {
			return 0, fmt.Errorf("etime %q: bad minutes", s)
		}
		if sec, err = strconv.Atoi(parts[2]); err != nil {
			return 0, fmt.Errorf("etime %q: bad seconds", s)
		}
	case 2:
		if m, err = strconv.Atoi(parts[0]); err != nil {
			return 0, fmt.Errorf("etime %q: bad minutes", s)
		}
		if sec, err = strconv.Atoi(parts[1]); err != nil {
			return 0, fmt.Errorf("etime %q: bad seconds", s)
		}
	default:
		return 0, fmt.Errorf("etime %q: unrecognised format", s)
	}
	return time.Duration(days)*24*time.Hour + time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second, nil
}

// BootTime reads when this host last booted, straight from the kernel. A
// ledger record older than this cannot name a still-running process -- pids
// are assigned fresh every boot. See the package doc comment's REBOOT note.
func BootTime() (time.Time, error) {
	out, err := exec.Command("sysctl", "-n", "kern.boottime").Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("read kern.boottime: %w", err)
	}
	m := regexp.MustCompile(`sec\s*=\s*(\d+)`).FindSubmatch(out)
	if m == nil {
		return time.Time{}, fmt.Errorf("kern.boottime: no sec field in %q", strings.TrimSpace(string(out)))
	}
	sec, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("kern.boottime: bad sec field: %w", err)
	}
	return time.Unix(sec, 0), nil
}
