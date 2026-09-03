package mirror

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolated builds a Config whose every tmux call goes to a PRIVATE server in
// a temporary socket directory, with $TMUX unset. No test in this file can
// address the default socket: a bare `tmux kill-server` from a lane destroyed
// this estate three times in one day, and the cleanup below runs exactly that
// verb -- which is only safe because killIsolatedServer refuses to run unless
// the socket directory is a private one this test made.
func isolated(t *testing.T) Config {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed; the tmux half of this package cannot be exercised here")
	}
	sock := shortTempDir(t)
	cfg := Config{
		Enabled:    true,
		Session:    "estate-mirror-test",
		Dir:        t.TempDir(),
		Max:        3,
		Keep:       100,
		Protected:  []string{"Hill90"},
		TmuxTmpdir: sock,
		Heartbeat:  0,
		Now:        time.Now,
	}
	t.Cleanup(func() { killIsolatedServer(t, cfg) })
	return cfg
}

// sockRoot is where isolated tmux sockets live during a test run. It is NOT
// t.TempDir(): a unix socket path is capped near 104 bytes on darwin, and
// t.TempDir()'s path (which embeds the test's own name) blows past that, so
// tmux fails with "File name too long" and every tmux assertion silently
// degrades into "no window was opened". A short, private, per-run directory
// is what makes the isolation usable rather than merely correct.
const sockRoot = "/tmp/estate-mirror-sock"

func shortTempDir(t *testing.T) string {
	t.Helper()
	if err := os.MkdirAll(sockRoot, 0o700); err != nil {
		t.Fatalf("socket root: %v", err)
	}
	dir, err := os.MkdirTemp(sockRoot, "s")
	if err != nil {
		t.Fatalf("socket dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// killIsolatedServer is the ONLY place a destructive server-wide verb appears
// in this package's tests, and it refuses to run against anything but a
// private socket directory this test itself created.
//
// It builds its own command rather than going through tmuxCmd, because
// tmuxCmd now refuses kill-server outright (allowedVerbs). That refusal is
// the point -- the production code may not run this verb at all, so a test
// that needs it must say so in its own file, visibly, behind the two guards
// below.
func killIsolatedServer(t *testing.T, cfg Config) {
	t.Helper()
	if cfg.TmuxTmpdir == "" {
		t.Fatalf("refusing to kill a tmux server with no isolated socket directory")
	}
	if !strings.HasPrefix(cfg.TmuxTmpdir, sockRoot+"/") {
		t.Fatalf("socket directory %q is not one this test made; refusing to kill", cfg.TmuxTmpdir)
	}
	isolatedTmux(cfg, "kill-server").Run()
}

// isolatedTmux is the tests' own tmux builder, carrying tmuxCmd's isolation
// but not its verb allowlist. Only test setup and teardown may use it.
func isolatedTmux(cfg Config, args ...string) *exec.Cmd {
	c := exec.Command("tmux", args...)
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_TMPDIR=") {
			continue
		}
		env = append(env, e)
	}
	c.Env = append(env, "TMUX_TMPDIR="+cfg.TmuxTmpdir)
	return c
}

func meta(id string) Meta {
	return Meta{ID: id, Issue: "1001", Role: "author", Harness: "claude", Worktree: "/tmp/wt/" + id}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading transcript: %v", err)
	}
	return string(b)
}

// waitFor polls until cond holds, so a test never asserts on a tmux server or
// a `tail` that has not finished starting yet. A timeout here is a real
// failure, not flake tolerance: the condition is one the code guarantees.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func windowNames(t *testing.T, cfg Config) []string {
	t.Helper()
	ws, err := windows(cfg)
	if err != nil {
		return nil
	}
	var out []string
	for _, w := range ws {
		out = append(out, w.Name)
	}
	return out
}

// --- the transcript is live, not a post-mortem ---------------------------

func TestStdoutReachesTheTranscriptWhileTheTurnIsStillRunning(t *testing.T) {
	cfg := isolated(t)
	m, err := Open(cfg, meta("1001-a"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// No Close yet: this is mid-turn.
	m.Stdout().Write([]byte("first chunk\n"))
	got := read(t, m.Path())
	if !strings.Contains(got, "first chunk") {
		t.Fatalf("mid-turn output is not in the transcript yet:\n%s", got)
	}
	if strings.Contains(got, footerMarker) {
		t.Fatalf("a running turn's transcript already carries the ended footer:\n%s", got)
	}
	m.Stdout().Write([]byte("second chunk\n"))
	got = read(t, m.Path())
	if !strings.Contains(got, "second chunk") {
		t.Fatalf("second mid-turn chunk missing:\n%s", got)
	}
	m.Close("complete", "")
}

func TestHeaderStatesThePaneIsNotATerminalForTheAgent(t *testing.T) {
	cfg := isolated(t)
	m, err := Open(cfg, meta("1001-hdr"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.Close("complete", "")
	got := read(t, m.Path())
	for _, want := range []string{"1001-hdr", "issue:    1001", "role:     author", "harness:  claude", "MIRROR", "nothing you type here reaches the agent"} {
		if !strings.Contains(got, want) {
			t.Fatalf("header missing %q:\n%s", want, got)
		}
	}
}

func TestStderrIsTaggedSoAnErrorIsDistinguishableFromOutput(t *testing.T) {
	cfg := isolated(t)
	m, err := Open(cfg, meta("1001-err"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m.Stdout().Write([]byte("ordinary output\n"))
	m.Stderr().Write([]byte("API Error: overloaded\n"))
	m.Close("failed", "exit status 1")
	got := read(t, m.Path())
	if !strings.Contains(got, "[stderr] API Error: overloaded") {
		t.Fatalf("stderr was not tagged:\n%s", got)
	}
	if strings.Contains(got, "[stderr] ordinary output") {
		t.Fatalf("stdout was wrongly tagged as stderr:\n%s", got)
	}
}

func TestStderrPrefixNeverLandsMidLine(t *testing.T) {
	cfg := isolated(t)
	m, err := Open(cfg, meta("1001-partial"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w := m.Stderr()
	w.Write([]byte("half a "))
	w.Write([]byte("line\n"))
	m.Close("failed", "")
	got := read(t, m.Path())
	if !strings.Contains(got, "[stderr] half a line") {
		t.Fatalf("a line split across writes was not reassembled:\n%s", got)
	}
	if strings.Contains(got, "half a [stderr]") {
		t.Fatalf("a prefix landed mid-line:\n%s", got)
	}
}

// --- a turn dying leaves its pane readable -------------------------------

func TestCloseStampsTheTerminalStateAndLeavesTheWindowAlive(t *testing.T) {
	cfg := isolated(t)
	m, err := Open(cfg, meta("1001-dead"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if m.WindowID() == "" {
		t.Fatalf("no mirror window was opened: %s", m.Note())
	}
	m.Close("failed", "API Error: quota window exhausted")

	got := read(t, m.Path())
	if !strings.Contains(got, footerMarker+" failed") {
		t.Fatalf("footer does not name the terminal state:\n%s", got)
	}
	if !strings.Contains(got, "quota window exhausted") {
		t.Fatalf("footer does not carry the note a human would want:\n%s", got)
	}
	// The window is exactly what a human wants at this moment; Close must not
	// take it away.
	names := windowNames(t, cfg)
	found := false
	for _, n := range names {
		if n == WindowPrefix+"1001-dead" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Close removed the dead turn's window; windows are %v", names)
	}
}

// --- a pane dying must not kill the turn ---------------------------------

func TestKillingTheWindowMidTurnDoesNotDisturbTheTranscript(t *testing.T) {
	cfg := isolated(t)
	m, err := Open(cfg, meta("1001-panekill"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id := m.WindowID()
	if id == "" {
		t.Fatalf("no mirror window was opened: %s", m.Note())
	}
	m.Stdout().Write([]byte("before the pane died\n"))

	// Addressed by window_id (@N), never by index: killing window 4
	// renumbers 5 into 4.
	if !strings.HasPrefix(id, "@") {
		t.Fatalf("window id %q is not a #{window_id}", id)
	}
	if out, kerr := tmuxCmd(cfg, "kill-window", "-t", id).CombinedOutput(); kerr != nil {
		t.Fatalf("kill-window: %v: %s", kerr, out)
	}
	waitFor(t, "the window to be gone", func() bool {
		for _, n := range windowNames(t, cfg) {
			if n == WindowPrefix+"1001-panekill" {
				return false
			}
		}
		return true
	})

	// The turn is still writing. Nothing about the dead pane may change that.
	m.Stdout().Write([]byte("after the pane died\n"))
	m.Close("complete", "")
	got := read(t, m.Path())
	for _, want := range []string{"before the pane died", "after the pane died", footerMarker + " complete"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript lost %q after its pane was killed:\n%s", want, got)
		}
	}
}

// TestWritesSurviveABrokenTranscript is the other direction of the same rule:
// the mirror must not be able to kill a turn even when the mirror itself is
// broken. sink.Write reports success unconditionally, so os/exec's stdout
// copier never aborts and never leaves the child taking SIGPIPE.
func TestWritesSurviveABrokenTranscript(t *testing.T) {
	cfg := isolated(t)
	m, err := Open(cfg, meta("1001-broken"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Simulate the disk going away underneath a running turn.
	m.s.mu.Lock()
	m.s.f.Close()
	m.s.mu.Unlock()

	n, werr := m.Stdout().Write([]byte("this cannot be written anywhere\n"))
	if werr != nil {
		t.Fatalf("a broken transcript returned an error to the turn's stdout copier: %v", werr)
	}
	if n != len("this cannot be written anywhere\n") {
		t.Fatalf("a short write would abort os/exec's copier: wrote %d", n)
	}
	m.Close("complete", "")
}

// --- bounded --------------------------------------------------------------

func TestWindowsAreBoundedAndEndedOnesAreRetiredFirst(t *testing.T) {
	cfg := isolated(t)
	cfg.Max = 3

	// Three turns that have ENDED -- retireable.
	for _, id := range []string{"1001-b1", "1001-b2", "1001-b3"} {
		m, err := Open(cfg, meta(id))
		if err != nil {
			t.Fatalf("Open(%s): %v", id, err)
		}
		m.Close("complete", "")
		// Distinct mtimes so "oldest first" is a real ordering, not a tie.
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(windowNames(t, cfg)); got != 3 {
		t.Fatalf("expected 3 mirror windows, got %d: %v", got, windowNames(t, cfg))
	}

	// A fourth must not make it four.
	m4, err := Open(cfg, meta("1001-b4"))
	if err != nil {
		t.Fatalf("Open(b4): %v", err)
	}
	defer m4.Close("complete", "")
	names := windowNames(t, cfg)
	if len(names) != 3 {
		t.Fatalf("bound of %d was exceeded: %v", cfg.Max, names)
	}
	for _, n := range names {
		if n == WindowPrefix+"1001-b1" {
			t.Fatalf("the OLDEST ended window survived; retired the wrong one: %v", names)
		}
	}
	found := false
	for _, n := range names {
		if n == WindowPrefix+"1001-b4" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the new turn got no window: %v", names)
	}
}

func TestRefusesToRetireALiveTurnsWindow(t *testing.T) {
	cfg := isolated(t)
	cfg.Max = 2

	live := make([]*Mirror, 0, 2)
	for _, id := range []string{"1001-l1", "1001-l2"} {
		m, err := Open(cfg, meta(id))
		if err != nil {
			t.Fatalf("Open(%s): %v", id, err)
		}
		live = append(live, m)
	}
	defer func() {
		for _, m := range live {
			m.Close("complete", "")
		}
	}()

	_, err := Open(cfg, meta("1001-l3"))
	if err == nil {
		t.Fatalf("a third mirror was opened over a bound of 2 full of live turns")
	}
	if !strings.Contains(err.Error(), ErrAtCapacity.Error()) {
		t.Fatalf("wrong refusal: %v", err)
	}
	// The refusal must leave nothing behind -- no orphan transcript for a
	// turn that got no mirror.
	if _, serr := os.Stat(filepath.Join(cfg.Dir, "1001-l3.log")); serr == nil {
		t.Fatalf("a refused mirror still created a transcript")
	}
	// And both live turns keep their windows.
	if got := len(windowNames(t, cfg)); got != 2 {
		t.Fatalf("live turns lost a window: %v", windowNames(t, cfg))
	}
}

// --- liveness without cooperation ------------------------------------------

// deadPID is a pid nothing owns. It is only ever handed to a fake Alive in
// these tests, never signalled.
const deadPID = 999001

// hardKilled opens a mirror the way a dispatch does and then abandons it
// WITHOUT Close, which is exactly what SIGKILL and the OOM killer do: no
// footer is ever written, because defers do not run.
func hardKilled(t *testing.T, cfg Config, id string) {
	t.Helper()
	cfg.OwnerPID = deadPID
	m, err := Open(cfg, meta(id))
	if err != nil {
		t.Fatalf("Open(%s): %v", id, err)
	}
	// Release the file handle without stamping a footer -- a killed process
	// leaves no footer, but it also leaves no open descriptor.
	m.s.close()
}

// TestAHardKilledDispatchDoesNotPoisonTheBoundForever is the review's own
// reproduction, now asserting the opposite outcome.
//
// Before: a transcript with no footer was "live" forever, so two hard-killed
// dispatches at Max=2 made every later dispatch refuse with ErrAtCapacity,
// permanently, curable only by a human removing a window. At the production
// bound that is six deaths and then no dispatch is ever mirrored again.
//
// After: the owning process is gone, so no footer is ever coming, so the
// window is retireable.
func TestAHardKilledDispatchDoesNotPoisonTheBoundForever(t *testing.T) {
	cfg := isolated(t)
	cfg.Max = 2
	// Nothing but the two abandoned turns is dead.
	cfg.Alive = func(pid int) bool { return pid != deadPID }

	for _, id := range []string{"1001-z1", "1001-z2"} {
		hardKilled(t, cfg, id)
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(windowNames(t, cfg)); got != 2 {
		t.Fatalf("setup: expected 2 zombie windows, got %v", windowNames(t, cfg))
	}
	// No footer anywhere -- the old signal is genuinely absent, so this test
	// cannot be passing for the wrong reason.
	for _, id := range []string{"1001-z1", "1001-z2"} {
		if strings.Contains(read(t, filepath.Join(cfg.Dir, id+".log")), footerMarker) {
			t.Fatalf("%s carries a footer; this is not the hard-kill case", id)
		}
	}

	// Three later dispatches in a row must each get a window.
	for i := 0; i < 3; i++ {
		m, err := Open(cfg, meta(fmt.Sprintf("1001-later%d", i)))
		if err != nil {
			t.Fatalf("later dispatch %d was refused a mirror by dead windows: %v", i, err)
		}
		names := windowNames(t, cfg)
		if len(names) > cfg.Max {
			t.Fatalf("bound of %d exceeded: %v", cfg.Max, names)
		}
		found := false
		for _, n := range names {
			if n == WindowPrefix+m.meta.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("later dispatch %d got no window: %v", i, names)
		}
		m.Close("complete", "")
		time.Sleep(10 * time.Millisecond)
	}
}

// TestALiveTurnIsNeverRetiredForLackOfAFooter is the other direction, and the
// one that matters more: a running turn has no footer either, and must never
// be mistaken for a zombie. Its pid is alive and its transcript is fresh, so
// neither the pid signal nor the age backstop may fire.
func TestALiveTurnIsNeverRetiredForLackOfAFooter(t *testing.T) {
	cfg := isolated(t)
	cfg.Max = 2
	asked := 0
	cfg.Alive = func(pid int) bool { asked++; return true }

	live := make([]*Mirror, 0, 2)
	for _, id := range []string{"1001-live1", "1001-live2"} {
		m, err := Open(cfg, meta(id))
		if err != nil {
			t.Fatalf("Open(%s): %v", id, err)
		}
		live = append(live, m)
	}
	defer func() {
		for _, m := range live {
			m.Close("complete", "")
		}
	}()

	if _, err := Open(cfg, meta("1001-live3")); err == nil {
		t.Fatalf("a live turn's window was retired to make room for another")
	} else if !strings.Contains(err.Error(), ErrAtCapacity.Error()) {
		t.Fatalf("wrong refusal: %v", err)
	}
	if got := len(windowNames(t, cfg)); got != 2 {
		t.Fatalf("a live turn lost its window: %v", windowNames(t, cfg))
	}
	if asked == 0 {
		t.Fatalf("the pid signal was never consulted; this test proves nothing about it")
	}
}

// TestAnUncertainProbeIsTreatedAsAlive: the failure directions are not
// symmetric. Retiring a running turn's screen is worse than leaving a dead
// pane up, so anything short of a positive death means live.
func TestAnUncertainProbeIsTreatedAsAlive(t *testing.T) {
	cfg := isolated(t)
	cfg.Max = 1
	cfg.Alive = func(pid int) bool { return true } // "could not tell" answers true
	hardKilled(t, cfg, "1001-unsure")

	if _, err := Open(cfg, meta("1001-next")); err == nil {
		t.Fatalf("a window whose liveness could not be determined was retired anyway")
	}
}

// TestAStaleTranscriptIsEndedEvenIfItsPidLooksAlive is the backstop, and it
// is what stops pid REUSE from reintroducing the permanent poison: a recycled
// pid makes a dead turn look live forever, so age has the final word. The
// margin is deliberate -- MaxAge is twice the caller's own 45m turn timeout,
// so nothing still running can reach it.
func TestAStaleTranscriptIsEndedEvenIfItsPidLooksAlive(t *testing.T) {
	cfg := isolated(t)
	cfg.Max = 1
	cfg.MaxAge = 90 * time.Minute
	cfg.Alive = func(pid int) bool { return true } // pid reuse: it "exists"
	hardKilled(t, cfg, "1001-stale")

	// Age it past MaxAge. The transcript's own last write is the clock.
	old := time.Now().Add(-2 * time.Hour)
	p := filepath.Join(cfg.Dir, "1001-stale.log")
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if !ended(cfg, "1001-stale") {
		t.Fatalf("a transcript untouched for 2 hours is still reported live")
	}
	// Freshly written, same fake-alive pid, must still be live -- the
	// backstop must not be doing all the work.
	hardKilled(t, cfg, "1001-fresh")
	if ended(cfg, "1001-fresh") {
		t.Fatalf("a just-written transcript with a live pid was reported ended")
	}
}

// TestATranscriptWithNoOwnerLineFallsBackToAge covers transcripts written by
// a build before the owner line existed: no pid to ask about, so the age
// backstop is the only signal, and a missing line must not read as pid 0.
func TestATranscriptWithNoOwnerLineFallsBackToAge(t *testing.T) {
	cfg := isolated(t)
	cfg.MaxAge = time.Hour
	cfg.Alive = func(pid int) bool {
		t.Fatalf("the pid probe was called for a transcript that names no pid")
		return false
	}
	p := filepath.Join(cfg.Dir, "1001-legacy.log")
	if err := os.WriteFile(p, []byte("=== estate turn 1001-legacy ===\nno owner line here\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if ended(cfg, "1001-legacy") {
		t.Fatalf("a fresh footerless transcript with no owner line was reported ended")
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if !ended(cfg, "1001-legacy") {
		t.Fatalf("an ancient footerless transcript with no owner line is still live")
	}
}

// TestTheHeaderNamesTheProcessThatWouldWriteTheFooter: the whole liveness
// chain rests on that line existing and being readable.
func TestTheHeaderNamesTheProcessThatWouldWriteTheFooter(t *testing.T) {
	cfg := isolated(t)
	m, err := Open(cfg, meta("1001-owner"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.Close("complete", "")
	pid, ok := ownerPID(read(t, m.Path()))
	if !ok {
		t.Fatalf("the transcript names no owning process:\n%s", read(t, m.Path()))
	}
	if pid != os.Getpid() {
		t.Fatalf("owner-pid is %d, want this process %d", pid, os.Getpid())
	}
}

// TestAHardKilledTurnsTranscriptIsEventuallyPruned: same root cause as the
// bound. prune only ever removed ended transcripts, so a footerless one was
// immortal on disk as well as on screen.
// TestAHardKilledTurnsTranscriptIsEventuallyPruned: same root cause as the
// bound, and the same fix. prune only ever removes ENDED transcripts, so
// while "ended" meant "has a footer", a hard-killed turn's transcript was
// immortal on disk as well as on screen -- it did not even count toward Keep.
//
// Max=1 so each new turn retires the previous window; once no window is
// following a transcript, Keep may remove it.
func TestAHardKilledTurnsTranscriptIsEventuallyPruned(t *testing.T) {
	cfg := isolated(t)
	cfg.Max = 1
	cfg.Keep = 1
	cfg.Alive = func(pid int) bool { return pid != deadPID }

	ids := []string{"1001-k1", "1001-k2", "1001-k3", "1001-k4"}
	for _, id := range ids {
		hardKilled(t, cfg, id)
		time.Sleep(10 * time.Millisecond)
	}
	logs := 0
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			logs++
		}
	}
	// Keep (1) plus the one still on screen. Four hard-killed turns must not
	// leave four transcripts.
	if logs > cfg.Keep+cfg.Max {
		t.Fatalf("hard-killed transcripts are never pruned: %d files with Keep=%d Max=%d", logs, cfg.Keep, cfg.Max)
	}
	if _, serr := os.Stat(filepath.Join(cfg.Dir, "1001-k4.log")); serr != nil {
		t.Fatalf("pruning removed the most recent record: %v", serr)
	}
}

func TestTranscriptsOnDiskAreBounded(t *testing.T) {
	cfg := isolated(t)
	cfg.Max = 2
	cfg.Keep = 2

	for _, id := range []string{"1001-p1", "1001-p2", "1001-p3", "1001-p4"} {
		m, err := Open(cfg, meta(id))
		if err != nil {
			t.Fatalf("Open(%s): %v", id, err)
		}
		m.Close("complete", "")
		time.Sleep(10 * time.Millisecond)
	}
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	logs := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			logs++
		}
	}
	// Keep ended-and-windowless transcripts to Keep; a transcript a window is
	// still following is never pruned, so the live ones may exceed Keep and
	// that is correct.
	if logs > cfg.Keep+cfg.Max {
		t.Fatalf("transcripts are unbounded: %d files on disk with Keep=%d Max=%d", logs, cfg.Keep, cfg.Max)
	}
	if logs == 0 {
		t.Fatalf("pruning removed everything, including live records")
	}
}

func TestPruneNeverRemovesARunningTurnsTranscript(t *testing.T) {
	cfg := isolated(t)
	cfg.Max = 3
	cfg.Keep = 1

	running, err := Open(cfg, meta("1001-running"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer running.Close("complete", "")
	for _, id := range []string{"1001-done1", "1001-done2"} {
		m, oerr := Open(cfg, meta(id))
		if oerr != nil {
			t.Fatalf("Open(%s): %v", id, oerr)
		}
		m.Close("complete", "")
	}
	if _, serr := os.Stat(running.Path()); serr != nil {
		t.Fatalf("a running turn's transcript was pruned: %v", serr)
	}
}

// --- refusals -------------------------------------------------------------

func TestRefusesAProtectedSession(t *testing.T) {
	cfg := isolated(t)
	cfg.Session = "Hill90"
	_, err := Open(cfg, meta("1001-protected"))
	if err == nil {
		t.Fatalf("opened a window in the operator's own protected session")
	}
	if !strings.Contains(err.Error(), "protected session") {
		t.Fatalf("wrong refusal: %v", err)
	}
}

func TestRefusesAnIDThatCouldEscapeTheTranscriptDirectory(t *testing.T) {
	cfg := isolated(t)
	for _, bad := range []string{"../escape", "a/b", "..", "."} {
		if _, err := Open(cfg, meta(bad)); err == nil {
			t.Fatalf("accepted unsafe dispatch id %q", bad)
		}
	}
}

func TestDisabledDoesNothing(t *testing.T) {
	cfg := isolated(t)
	cfg.Enabled = false
	m, err := Open(cfg, meta("1001-off"))
	if err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
	// Every method must be safe on the nil the caller now holds.
	m.Logf("this must not panic")
	m.Stdout().Write([]byte("nor this"))
	m.Stderr().Write([]byte("nor this"))
	m.Close("complete", "")
	if m.Path() != "" || m.WindowID() != "" || m.Note() != "" {
		t.Fatalf("a nil mirror reported state")
	}
	if names := windowNames(t, cfg); len(names) != 0 {
		t.Fatalf("a disabled mirror still opened windows: %v", names)
	}
}

// TestATmuxFailureStillLeavesAReadableTranscript is the durable-before-visible
// rule: the record is made first, so losing the screen never loses the record.
func TestATmuxFailureStillLeavesAReadableTranscript(t *testing.T) {
	cfg := isolated(t)
	// A socket directory that EXISTS (so the silent-fallback refusal does not
	// fire) but that tmux cannot create a socket inside. Every tmux call
	// fails; nothing else changes.
	unwritable := filepath.Join(shortTempDir(t), "ro")
	if err := os.Mkdir(unwritable, 0o500); err != nil {
		t.Fatalf("setup: %v", err)
	}
	cfg.TmuxTmpdir = unwritable

	m, err := Open(cfg, meta("1001-notmux"))
	if err != nil {
		t.Fatalf("a tmux failure must not fail Open: %v", err)
	}
	defer m.Close("complete", "")
	if m.WindowID() != "" {
		t.Fatalf("reported a window id with no working tmux: %q", m.WindowID())
	}
	if m.Note() == "" {
		t.Fatalf("no window and no reason given")
	}
	m.Stdout().Write([]byte("still mirrored to disk\n"))
	got := read(t, m.Path())
	if !strings.Contains(got, "still mirrored to disk") {
		t.Fatalf("transcript did not survive the tmux failure:\n%s", got)
	}
	if !strings.Contains(got, "tail -f "+m.Path()) {
		t.Fatalf("transcript does not tell a reader how to watch it without tmux:\n%s", got)
	}
}

// TestRefusesASocketDirectoryTmuxWouldSilentlyIgnore is invariant 4's real
// failure mode, not its theoretical one. tmux handed a $TMUX_TMPDIR that is
// not a usable directory does NOT error: it falls back to the default socket,
// so a caller that believed it was isolated builds its session on the
// operator's own server and nothing says anything. This happened once while
// this package was being written -- a demo session appeared beside Hill90 --
// and it was caught by luck, not by a check. Now it is a refusal.
func TestRefusesASocketDirectoryTmuxWouldSilentlyIgnore(t *testing.T) {
	cfg := isolated(t)
	for _, bad := range []struct {
		what string
		make func(t *testing.T) string
	}{
		{"a directory that does not exist", func(t *testing.T) string {
			return filepath.Join(shortTempDir(t), "absent")
		}},
		{"a path that is a file", func(t *testing.T) string {
			p := filepath.Join(shortTempDir(t), "afile")
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatalf("setup: %v", err)
			}
			return p
		}},
	} {
		c := cfg
		c.TmuxTmpdir = bad.make(t)
		_, err := Open(c, meta("1001-fallback"))
		if err == nil {
			t.Fatalf("%s was accepted; tmux would have used the DEFAULT socket", bad.what)
		}
		if !strings.Contains(err.Error(), "DEFAULT socket") {
			t.Fatalf("%s: refusal does not name what actually goes wrong: %v", bad.what, err)
		}
	}
}

func TestHeartbeatKeepsAPaneVisiblyAliveWhenAHarnessPrintsNothing(t *testing.T) {
	cfg := isolated(t)
	cfg.Heartbeat = 20 * time.Millisecond
	m, err := Open(cfg, meta("1001-hb"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitFor(t, "a heartbeat line", func() bool {
		b, rerr := os.ReadFile(m.Path())
		return rerr == nil && strings.Contains(string(b), "[estate] still running")
	})
	m.Close("complete", "")
	// The heartbeat must stop with the turn, or a finished pane keeps
	// scrolling forever.
	after := read(t, m.Path())
	time.Sleep(80 * time.Millisecond)
	if read(t, m.Path()) != after {
		t.Fatalf("the heartbeat kept writing after Close")
	}
}

// --- structural guarantees ------------------------------------------------

// --- the control-path guarantee, and exactly how much each half holds -----

// TestNoControlVerbCanRun is the PRIMARY guard, and the only one of the three
// here that cannot be walked around. Every tmux invocation in this package is
// built by tmuxCmd, and tmuxCmd refuses any verb outside allowedVerbs -- so a
// control path does not run regardless of which file wrote it or how the verb
// string was assembled. Review of #1003 defeated the source scan below twice
// (a sibling file, and "send"+"-"+"keys"); neither evasion survives this.
func TestNoControlVerbCanRun(t *testing.T) {
	cfg := isolated(t)
	if err := ensureSession(cfg); err != nil {
		t.Fatalf("ensureSession: %v", err)
	}
	m, err := Open(cfg, meta("1001-noctl"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.Close("complete", "")

	// Every way this package could type into, or destroy more than, one
	// window -- including a verb built at runtime, which no source scan sees.
	built := "send" + "-" + "keys"
	for _, verb := range []string{built, "paste-buffer", "load-buffer", "set-buffer", "run-shell", "respawn-pane", "respawn-window", "kill-server", "kill-session", "kill-pane"} {
		out, rerr := tmuxCmd(cfg, verb, "-t", target(cfg.Session), "hello", "Enter").CombinedOutput()
		if rerr == nil {
			t.Fatalf("tmuxCmd ran %q: %s", verb, out)
		}
		if !strings.Contains(rerr.Error(), "refusing tmux verb") {
			t.Fatalf("%q failed for the wrong reason (it must be REFUSED, not merely broken): %v", verb, rerr)
		}
	}
	// And the refusal is a refusal, not collateral damage: the session and
	// its mirror window are still there afterwards.
	if got := len(windowNames(t, cfg)); got != 1 {
		t.Fatalf("a refused verb disturbed the session: %v", windowNames(t, cfg))
	}
	// The one destructive verb the bound genuinely needs is still allowed --
	// an allowlist that refused everything would pass this test by being
	// useless.
	if !allowedVerbs["kill-window"] {
		t.Fatalf("kill-window is no longer allowed; the bound cannot be enforced")
	}
}

// TestNoControlVerbIsWrittenAnywhereInThePackage is defence in depth over
// TestNoControlVerbCanRun, and it is worth being exact about its reach:
//
//   - It reads EVERY non-test .go file in this package directory, not just
//     mirror.go. A control path in a sibling file used to pass green.
//   - It matches literal strings only. A verb assembled at runtime
//     ("send" + "-" + "keys") is INVISIBLE to it. That hole is deliberate
//     and is not closed here -- it is closed by tmuxCmd's allowlist, which
//     rejects the verb when the command is built.
//   - _test.go files are excluded because this file itself names every
//     banned verb, on purpose, to prove they are refused.
func TestNoControlVerbIsWrittenAnywhereInThePackage(t *testing.T) {
	for name, body := range packageSources(t) {
		for _, banned := range []string{"send-keys", "paste-buffer", "load-buffer", "set-buffer", "run-shell", "respawn-pane", "respawn-window"} {
			if strings.Contains(body, banned) {
				t.Fatalf("%s uses %q -- this package is a viewer, not a control path", name, banned)
			}
		}
	}
}

// TestThePackageNeverAddressesAWholeServerOrSession guards invariant 4 at the
// source level, across every file in the package. Same literal-only reach as
// the test above; the enforcing half is allowedVerbs.
func TestThePackageNeverAddressesAWholeServerOrSession(t *testing.T) {
	srcs := packageSources(t)
	for name, body := range srcs {
		for _, banned := range []string{"kill-server", "kill-session", "kill-pane"} {
			if strings.Contains(body, banned) {
				t.Fatalf("%s uses %q; nothing here may address more than one window", name, banned)
			}
		}
	}
	if !strings.Contains(srcs["mirror.go"], "kill-window") {
		t.Fatalf("kill-window vanished from mirror.go -- the bound is no longer enforced")
	}
}

// packageSources returns every non-test .go file in this package directory,
// comment-stripped. The scan is directory-wide because a guard that reads one
// file cannot honestly claim to cover a package.
func packageSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatalf("reading %s: %v", name, rerr)
		}
		out[name] = stripComments(string(b))
	}
	if len(out) == 0 {
		t.Fatalf("the package scan found no source files -- the guard is measuring nothing")
	}
	return out
}

// TestEveryTargetIsExactMatched: tmux prefix-matches a bare session name, so
// `-t estate` can resolve to a different session that merely starts the same
// way. Every target this package builds goes through target(), which prefixes
// `=`.
func TestEveryTargetIsExactMatched(t *testing.T) {
	if got := target("estate"); got != "=estate" {
		t.Fatalf("target() does not exact-match: %q", got)
	}
	cfg := isolated(t)
	c := tmuxCmd(cfg, "list-windows", "-t", target(cfg.Session))
	joined := strings.Join(c.Args, " ")
	if !strings.Contains(joined, "-t =estate-mirror-test") {
		t.Fatalf("target was not exact-matched in the built command: %s", joined)
	}
}

// TestTmuxCmdIsolatesTheSocketAndDropsTMUX proves the test isolation is real
// rather than assumed: with TmuxTmpdir set, the child's environment names
// that directory and carries no inherited $TMUX pointing at a live server.
func TestTmuxCmdIsolatesTheSocketAndDropsTMUX(t *testing.T) {
	cfg := Config{TmuxTmpdir: "/tmp/estate-mirror-socket-test"}
	t.Setenv("TMUX", "/private/tmp/tmux-501/default,1234,0")
	// An allowlisted verb, because a refused one is returned as a command
	// that never runs and so carries no environment to inspect.
	c := tmuxCmd(cfg, "list-windows")
	var sawDir bool
	for _, e := range c.Env {
		if e == "TMUX_TMPDIR=/tmp/estate-mirror-socket-test" {
			sawDir = true
		}
		if strings.HasPrefix(e, "TMUX=") {
			t.Fatalf("an inherited $TMUX survived into an isolated tmux call: %q", e)
		}
	}
	if !sawDir {
		t.Fatalf("TMUX_TMPDIR was not set on the isolated call: %v", c.Env)
	}
}

// TestWindowsOnlyEverSeesItsOwnWindows: a window without the estate's prefix
// belongs to somebody else and must never become a reap candidate, whatever
// session it sits in.
func TestWindowsOnlyEverSeesItsOwnWindows(t *testing.T) {
	cfg := isolated(t)
	if err := ensureSession(cfg); err != nil {
		t.Fatalf("ensureSession: %v", err)
	}
	if out, err := tmuxCmd(cfg, "new-window", "-d", "-t", target(cfg.Session)+":", "-n", "someone-elses-work", "sleep", "60").CombinedOutput(); err != nil {
		t.Fatalf("setup window: %v: %s", err, out)
	}
	m, err := Open(cfg, meta("1001-neighbour"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer m.Close("complete", "")

	ws, err := windows(cfg)
	if err != nil {
		t.Fatalf("windows: %v", err)
	}
	for _, w := range ws {
		if !strings.HasPrefix(w.Name, WindowPrefix) {
			t.Fatalf("windows() returned a window this package does not own: %q", w.Name)
		}
	}
	if len(ws) != 1 {
		t.Fatalf("expected exactly the one mirror window, got %d", len(ws))
	}
	// And the neighbour is untouched by everything above.
	out, err := tmuxCmd(cfg, "list-windows", "-t", target(cfg.Session), "-F", "#{window_name}").Output()
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	if !strings.Contains(string(out), "someone-elses-work") {
		t.Fatalf("a window this package does not own disappeared:\n%s", out)
	}
}

// stripComments removes // line comments so a source-scanning guard reads the
// code rather than the prose about it -- this file's own doc comments name
// several of the banned verbs on purpose.
func stripComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if i := strings.Index(line, "// "); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
