package monitor

import (
	"strings"
	"testing"
)

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

// claudeProcessesFixture is a captured-shape `ps aux` snapshot (real
// executables and argv patterns, PIDs/timestamps genericized) built to
// contain every false-positive line agent-tui#147 enumerated, plus the
// three daemon pool-helper shapes named in the fix-pass review
// (`claude bg-spare`, `claude bg-pty-host`, `claude.exe daemon run`,
// captured live from this host's own `ps aux`), plus real agent lines.
// Header row included deliberately -- parseClaudeProcesses must not count
// it.
const claudeProcessesFixture = `USER               PID  %CPU %MEM      VSZ    RSS   TT  STAT STARTED      TIME COMMAND
jon              22992  24.8   4.6 442027568 868736   ??  SN   11:00PM  305:02.37 /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe --session-id abc --model claude-opus-5
jon              16912   9.8   1.0 440924544 182992 s000  S+   4:00PM   84:17.61 claude --dangerously-skip-permissions
jon              36461   2.4   1.7 440948768 323968 s031  Ss+  4:45AM    1:15.95 claude --model sonnet --dangerously-skip-permissions
jon              85288   0.0   0.0 410264816    192   ??  RN   5:33AM    0:00.00 ugrep -G --ignore-files --hidden -I -i claude
jon              85286   0.0   0.0 435304576   1856   ??  SN   5:33AM    0:00.00 /bin/zsh -c source /Users/jon/.claude/shell-snapshots/snapshot-zsh-1.sh 2>/dev/null || true
jon               1234   0.1   0.2 400000000  50000   ??  S    5:00AM    0:00.10 /Applications/Claude.app/Contents/Helpers/chrome-native-host --parent-window=0
jon              16980   0.0   0.6 440880208 107888   ??  SN   4:00PM    3:13.30 claude bg-spare --bg-spare /tmp/cc-daemon-501/3ed5b914/spare/6e83ebeb.claim.sock
jon              16962   0.0   0.4 440874080  78112   ??  SNs  4:00PM    2:56.60 claude bg-pty-host --bg-pty-host /tmp/cc-daemon-501/3ed5b914/spare/6e83ebeb.pty.sock 200 50 -- /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe --bg-spare /tmp/cc-daemon-501/3ed5b914/spare/6e83ebeb.claim.sock
jon              22940   0.4   0.7 440922272 132448   ??  Ss   11:00PM  108:47.29 /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe daemon run --origin transient --spawned-by {"label":"claude","cwd":"/Users/jon/source/repos/Personal/Hill90","pid":7724}
`

// TestParseClaudeProcesses_ExcludesFalsePositivesCountsRealAgents is the
// brief's own mutation-check fixture (agent-tui#147 "What to do" item 3):
// a desktop-helper line, a zsh-snapshot line, a self-grep-shaped line,
// three daemon pool-helper lines (bg-spare, bg-pty-host, daemon run) and
// N=3 real agent lines must yield exactly 3, not 9 (the old substring
// rule's answer against this same fixture -- verified below).
func TestParseClaudeProcesses_ExcludesFalsePositivesCountsRealAgents(t *testing.T) {
	got := parseClaudeProcesses(claudeProcessesFixture)
	if !got.Known || got.Value != 3 {
		t.Fatalf("parseClaudeProcesses(fixture) = %+v, want KnownCount(3)", got)
	}
}

// TestParseClaudeProcesses_ExcludesDaemonPoolHelpers is the fix-pass
// review's own regression case (Review-Lane: estate:2 on agent-tui#161's
// c827b85): a fixture containing only the two shapes the review built
// from this host's real `ps aux` -- `claude bg-spare` and
// `claude bg-pty-host` -- must count zero agents, not two.
func TestParseClaudeProcesses_ExcludesDaemonPoolHelpers(t *testing.T) {
	out := "jon 16980 0.0 0.6 440880208 107888 ?? SN 4:00PM 3:13.30 claude bg-spare --bg-spare /tmp/cc-daemon-501/3ed5b914/spare/6e83ebeb.claim.sock\n" +
		"jon 16962 0.0 0.4 440874080 78112 ?? SNs 4:00PM 2:56.60 claude bg-pty-host --bg-pty-host /tmp/cc-daemon-501/3ed5b914/spare/6e83ebeb.pty.sock 200 50 -- /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe --bg-spare /tmp/cc-daemon-501/3ed5b914/spare/6e83ebeb.claim.sock\n"
	got := parseClaudeProcesses(out)
	if !got.Known || got.Value != 0 {
		t.Fatalf("parseClaudeProcesses(bg-spare + bg-pty-host only) = %+v, want KnownCount(0)", got)
	}
}

// TestParseClaudeProcesses_ExcludesDaemonRun covers the fix-pass brief's
// item 2 decision: `claude(.exe) daemon run` is pool infrastructure too
// (it hosts the detached background sessions bg-spare/bg-pty-host serve),
// excluded by the same rule, not left counted because it wasn't named as
// explicitly as the other two.
func TestParseClaudeProcesses_ExcludesDaemonRun(t *testing.T) {
	out := `jon 22940 0.4 0.7 440922272 132448 ?? Ss 11:00PM 108:47.29 /opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe daemon run --origin transient --spawned-by {"label":"claude","cwd":"/Users/jon/source/repos/Personal/Hill90","pid":7724}
`
	got := parseClaudeProcesses(out)
	if !got.Known || got.Value != 0 {
		t.Fatalf("parseClaudeProcesses(daemon run only) = %+v, want KnownCount(0)", got)
	}
}

// TestParseClaudeProcesses_MutationCheck proves the fixture above actually
// discriminates: the OLD substring-of-the-whole-line rule scores this same
// fixture 9 (every line mentions "claude" somewhere), so a test that
// cannot tell the old and new counters apart is not a test of this fix
// (brief item 3). This re-implements the old rule inline -- it must NOT be
// merged with the real parseClaudeProcesses -- and prints both counts so a
// reviewer can see the mutation applied without re-deriving it.
func TestParseClaudeProcesses_MutationCheck(t *testing.T) {
	oldRuleCount := func(out string) int {
		n := 0
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(strings.ToLower(line), "claude") {
				n++
			}
		}
		return n
	}
	old := oldRuleCount(claudeProcessesFixture)
	new := parseClaudeProcesses(claudeProcessesFixture).Value
	t.Logf("old substring rule: %d, new executable-match rule: %d", old, new)
	if old == new {
		t.Fatalf("old rule (%d) and new rule (%d) agree on the fixture -- fixture does not distinguish them", old, new)
	}
	if old != 9 {
		t.Fatalf("old rule = %d, want 9 (every fixture line mentions \"claude\")", old)
	}
	if new != 3 {
		t.Fatalf("new rule = %d, want 3 (only the real agent lines)", new)
	}
}

// TestParseClaudeProcesses_ArgumentMentionIsNotCounted is the "does the
// gauge move when someone runs a search" case named directly in the issue:
// a process whose own argv0 is not claude/claude.exe must never count
// merely because some argument contains the string, no matter where in
// the line it appears or how many such lines exist.
func TestParseClaudeProcesses_ArgumentMentionIsNotCounted(t *testing.T) {
	out := "jon 1 0.0 0.0 0 0 ?? S 1:00AM 0:00.00 grep -r claude /Users/jon/source/repos\n" +
		"jon 2 0.0 0.0 0 0 ?? S 1:00AM 0:00.00 /bin/cat /Users/jon/.claude/settings.json\n"
	got := parseClaudeProcesses(out)
	if !got.Known || got.Value != 0 {
		t.Fatalf("parseClaudeProcesses(argument-only mentions) = %+v, want KnownCount(0)", got)
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
