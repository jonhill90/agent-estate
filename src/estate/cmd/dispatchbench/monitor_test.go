package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/pressure"
)

// hostGaugesWork reports whether this host's memory and paging gauges can be
// read at all. On the Linux CI runner they cannot -- `vm_stat` does not exist
// and `sysctl -n vm.loadavg` fails -- so pressure.Host correctly refuses
// EVERYTHING, and a test asserting "refuses for the right reason" or "allows
// when neutralised" would be measuring the runner rather than the code.
//
// The two tests below therefore bind on macOS, which is the only place this
// benchmark runs, and skip elsewhere. Same idiom, and same reason, as
// internal/pressure's own hostIsMeasurable.
func hostGaugesWork() bool {
	return pressure.Host(pressure.Limits{
		MaxLoadPerCore:       1e9,
		MinFreeMemMB:         0,
		MaxSwapoutsPerSample: 1e9,
		MaxWorktrees:         1e9,
		MaxInFlight:          1e9,
	}).OK
}

// A worker is a tree. Measuring only the root would report a turn that shells
// out as costing nothing, which is the number the whole decision record turns
// on.
func TestSubtreeRSSSumsTheWholeTree(t *testing.T) {
	procs := []procRow{
		{pid: 1, ppid: 0, rssMB: 10},
		{pid: 100, ppid: 1, rssMB: 400},  // the worker
		{pid: 101, ppid: 100, rssMB: 50}, // a tool it spawned
		{pid: 102, ppid: 101, rssMB: 25}, // and one that spawned
		{pid: 200, ppid: 1, rssMB: 900},  // somebody else entirely
	}
	mb, n := subtreeRSSMB(procs, 100)
	if mb != 475 || n != 3 {
		t.Fatalf("want 475MB across 3 processes, got %.0fMB across %d", mb, n)
	}
}

// An exited worker is gone, not an error and not the whole host.
func TestSubtreeRSSOfAnExitedWorkerIsZero(t *testing.T) {
	procs := []procRow{{pid: 1, ppid: 0, rssMB: 10}, {pid: 200, ppid: 1, rssMB: 900}}
	if mb, n := subtreeRSSMB(procs, 100); mb != 0 || n != 0 {
		t.Fatalf("want 0, got %.0fMB across %d", mb, n)
	}
	if mb, _ := subtreeRSSMB(procs, 0); mb != 0 {
		t.Fatalf("no worker should read as 0MB, got %.0f", mb)
	}
}

// The gauge must work on this host, not just in arithmetic. `ps` returning
// nothing parseable is an error rather than a quiet zero -- a zero would read
// as a host using no memory, and the monitor would keep running.
func TestReadProcsSeesThisProcess(t *testing.T) {
	procs, err := readProcs()
	if err != nil {
		t.Fatalf("could not read processes on this host: %v", err)
	}
	mb, n := subtreeRSSMB(procs, os.Getpid())
	if n < 1 || mb <= 0 {
		t.Fatalf("the test binary's own tree measured as %.0fMB across %d processes", mb, n)
	}
}

// The floor gate must fire. Given a floor no host can meet, the harness must
// refuse to start -- the mechanism the first attempt at #1002 lacked.
func TestPreflightRefusesBelowAnImpossibleFloor(t *testing.T) {
	if !hostGaugesWork() {
		t.Skip("host memory/paging gauges are not readable here")
	}
	m, err := newMonitor(filepath.Join(t.TempDir(), "s.jsonl"), benchLimits{MinFreeMemMB: 1e12, MaxSwapoutsPerSample: 1, MaxWorkerRSSMB: 1e9})
	if err != nil {
		t.Fatal(err)
	}
	defer m.sink.Close()
	err = m.Preflight()
	if err == nil {
		t.Fatal("Preflight allowed a run with the free-memory floor set above any real host")
	}
	if !strings.Contains(err.Error(), "below floor") {
		t.Fatalf("Preflight refused without naming the floor: %v", err)
	}
}

// ...and it must NOT fire on a healthy host, or the benchmark could never run
// and the test above would be satisfied by a Preflight that always refuses.
func TestPreflightAllowsWhenTheHostIsFine(t *testing.T) {
	if !hostGaugesWork() {
		t.Skip("host memory/paging gauges are not readable here")
	}
	m, err := newMonitor(filepath.Join(t.TempDir(), "s.jsonl"), benchLimits{MinFreeMemMB: 0, MaxSwapoutsPerSample: 1e9, MaxWorkerRSSMB: 1e9})
	if err != nil {
		t.Fatal(err)
	}
	defer m.sink.Close()
	if err := m.Preflight(); err != nil {
		t.Fatalf("Preflight refused with every limit neutralised: %v", err)
	}
}

// Samples are streamed to disk, not accumulated. The first attempt's binary
// reached 1753MB; whatever else this one does, it does not hold its own
// measurements.
func TestSamplesAreWrittenAsTheyAreTaken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	m, err := newMonitor(path, defaultBenchLimits())
	if err != nil {
		t.Fatal(err)
	}
	m.Watch("stateless", 3, 0)
	m.record(sample{T: "now", WorkerRSSMB: 12})
	m.sink.Close()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"arm":"stateless"`) || !strings.Contains(string(b), `"turn":3`) {
		t.Fatalf("the sample was not attributed to the turn being watched:\n%s", b)
	}
	if a := m.Aggregate("stateless", 3); a.PeakWorkerRSSMB != 12 || a.Samples != 1 {
		t.Fatalf("aggregate did not pick the sample up: %+v", a)
	}
}

// Invariant 4, as a test. tmux with no TMUX_TMPDIR addresses the DEFAULT
// socket -- the operator's own sessions and the estate's live one -- which a
// lane in this package must be unable to reach.
func TestLaneRefusesToAddressAnUnisolatedSocket(t *testing.T) {
	if err := assertIsolated(""); err == nil {
		t.Fatal("an empty TMUX_TMPDIR was accepted; that is the default socket")
	}
	if err := assertIsolated("/etc"); err == nil {
		t.Fatal("a TMUX_TMPDIR outside the system temp dir was accepted")
	}
	if err := assertIsolated(t.TempDir()); err != nil {
		t.Fatalf("a private temp dir was rejected, so no lane could ever start: %v", err)
	}
}

// ...and the verb allowlist, so a future edit cannot reach for a verb this
// package has no business running.
func TestLaneRefusesVerbsOutsideItsAllowlist(t *testing.T) {
	l := &lane{tmpdir: t.TempDir(), socket: "test", session: "s"}
	for _, verb := range []string{"kill-session", "respawn-pane", "switch-client", ""} {
		if _, err := l.tmuxCmd(t.Context(), verb); err == nil {
			t.Fatalf("verb %q was allowed", verb)
		}
	}
	if _, err := l.tmuxCmd(t.Context(), "capture-pane"); err != nil {
		t.Fatalf("an allowed verb was refused: %v", err)
	}
}

// The socket path a lane creates must fit inside the unix socket length cap.
// The first run of this program did not: macOS's per-user $TMPDIR left tmux
// with "File name too long" and the persistent arm never started at all --
// which would have been written up as "the lane could not be measured" for a
// reason that had nothing to do with the host.
func TestALanesSocketPathFitsWithinTheUnixSocketLimit(t *testing.T) {
	root := tmuxTmpdirRoots()[len(tmuxTmpdirRoots())-1]
	dir, err := os.MkdirTemp(root, "dbench-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := assertIsolated(dir); err != nil {
		t.Fatalf("the root this program actually uses is not accepted as isolated: %v", err)
	}
	// tmux appends "/tmux-<uid>/<socket>" to TMUX_TMPDIR.
	sock := filepath.Join(dir, "tmux-501", "dispatchbench")
	if len(sock) > 100 {
		t.Fatalf("socket path is %d bytes, which tmux will refuse: %s", len(sock), sock)
	}
}
