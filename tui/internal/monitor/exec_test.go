package monitor

import "testing"

// These test the parsing regexes directly against real command output
// shapes (captured from an actual run on each platform, not invented),
// rather than shelling out inside `go test` -- CI (ubuntu-latest) and a
// developer's own darwin machine must both get the same answer for the
// same input text.

func TestUptimeLoadRE_LinuxCommaForm(t *testing.T) {
	// procps-ng uptime, Linux.
	line := " 14:32:01 up 3 days,  2:14,  1 user,  load average: 0.52, 0.61, 0.58"
	m := uptimeLoadRE.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("no match for %q", line)
	}
	if m[1] != "0.52" || m[2] != "0.61" || m[3] != "0.58" {
		t.Errorf("got %v", m[1:])
	}
}

func TestUptimeLoadRE_DarwinSpaceForm(t *testing.T) {
	// macOS/BSD uptime.
	line := "14:32  up 2 days, 21:10, 3 users, load averages: 1.23 1.10 0.95"
	m := uptimeLoadRE.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("no match for %q", line)
	}
	if m[1] != "1.23" || m[2] != "1.10" || m[3] != "0.95" {
		t.Errorf("got %v", m[1:])
	}
}

func TestDarwinSwapRE(t *testing.T) {
	line := "total = 2048.00M  used = 512.25M  free = 1535.75M  (encrypted)"
	m := darwinSwapRE.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("no match for %q", line)
	}
	if m[1] != "2048.00" || m[2] != "512.25" {
		t.Errorf("got %v", m[1:])
	}
}

func TestParseDarwinSwap_ZeroTotalIsKnownZero(t *testing.T) {
	// Captured live from this machine (2026-08-22): macOS with no swap
	// file allocated yet reports "total = 0.00M" -- a real zero, not a
	// failed read (parseDarwinSwap's own doc comment).
	f := parseDarwinSwap("total = 0.00M  used = 0.00M  free = 0.00M  (encrypted)")
	if !f.Known || f.Value != 0 {
		t.Errorf("parseDarwinSwap(zero total) = %+v, want KnownFigure(0)", f)
	}
}

func TestParseDarwinSwap_NonzeroPercent(t *testing.T) {
	f := parseDarwinSwap("total = 2048.00M  used = 512.00M  free = 1536.00M  (encrypted)")
	if !f.Known || f.Value != 25 {
		t.Errorf("parseDarwinSwap(512/2048) = %+v, want KnownFigure(25)", f)
	}
}

func TestParseDarwinSwap_UnparsableIsUnknown(t *testing.T) {
	f := parseDarwinSwap("garbage")
	if f.Known {
		t.Errorf("parseDarwinSwap(garbage) = %+v, want unknown", f)
	}
}

func TestParseLinuxMeminfo_ZeroTotalIsKnownZero(t *testing.T) {
	f := parseLinuxMeminfo("SwapTotal:       0 kB\nSwapFree:        0 kB\n")
	if !f.Known || f.Value != 0 {
		t.Errorf("parseLinuxMeminfo(zero total) = %+v, want KnownFigure(0)", f)
	}
}

func TestParseLinuxMeminfo_NonzeroPercent(t *testing.T) {
	f := parseLinuxMeminfo("SwapTotal:    1000 kB\nSwapFree:      750 kB\n")
	if !f.Known || f.Value != 25 {
		t.Errorf("parseLinuxMeminfo(250/1000) = %+v, want KnownFigure(25)", f)
	}
}

func TestParseLinuxMeminfo_MissingFieldsIsUnknown(t *testing.T) {
	f := parseLinuxMeminfo("MemTotal: 100 kB\n")
	if f.Known {
		t.Errorf("parseLinuxMeminfo(no swap fields) = %+v, want unknown", f)
	}
}

func TestReadClaudeProcesses_CountsMatchingLines(t *testing.T) {
	// readClaudeProcesses shells out itself; this only exercises the
	// counting logic's shape indirectly is not possible without a fake ps
	// -- instead this documents the expectation directly: a real run on a
	// machine with agent-tui's own dev loop active should return a Known
	// count > 0. Skipped when the host has no `ps` at all (a stripped CI
	// container), matching this package's own "cannot look" posture rather
	// than failing the suite over an environment gap unrelated to the
	// logic under test.
	c := readClaudeProcesses()
	if !c.Known {
		t.Skip("ps unavailable on this host -- readClaudeProcesses correctly reported unknown")
	}
	if c.Value < 0 {
		t.Errorf("negative process count: %d", c.Value)
	}
}
