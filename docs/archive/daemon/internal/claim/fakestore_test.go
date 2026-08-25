package claim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaimStore is claim.sh's own take/release verbs, reproduced as a stub
// over a plain lock-file directory -- real enough to exercise ScriptGate's
// actual subprocess path (argv, exit code, stdout), and deliberately
// reproducing claim.sh's OWN documented race (check, THEN write, no
// compare-and-swap) so the mutation-check pair in claim_test.go can open or
// close that window rather than asserting against an idealised atomic fake.
//
// The executable it hands back is THIS TEST BINARY re-executed in helper
// mode (storeHelperMain, dispatched from TestMain), not a shell script. That
// matters: the interleaving the direction-(b) control needs is forced by a
// barrier the store itself waits on, and a barrier has to be written in the
// store's own language. A `sleep` in a shell fake only makes the overlap
// LIKELY, which is exactly how that control came to fail 7 runs in 10 under
// load (#504) -- the two attempts were hoped into the same window instead of
// held there.
//
// Configuration reaches the helper through the environment rather than argv,
// because argv belongs to ScriptGate: the child is invoked by the real
// ScriptGate.run with the real `take <issue> <repo> <lane>` argv, and a test
// knob wedged into that would stop testing the shape production sends.
const (
	envStoreDir        = "CLAIM_TEST_FAKE_STORE_DIR"
	envStoreDelay      = "CLAIM_TEST_FAKE_STORE_DELAY"
	envStoreBarrierDir = "CLAIM_TEST_FAKE_STORE_BARRIER_DIR"
	envStoreBarrierN   = "CLAIM_TEST_FAKE_STORE_BARRIER_N"
)

// storeBarrierTimeout bounds the wait so a broken barrier FAILS the control
// instead of hanging until the package timeout kills the whole run with no
// attributable cause. It is not a race window: correctness here does not
// depend on the wait being long enough, only on it being finite.
const storeBarrierTimeout = 30 * time.Second

// storeOpts configures the fake store for one test.
type storeOpts struct {
	// delay sits between the store's first check and its write, widening
	// its check-then-write window. Timing-flavoured, so it is only used
	// where the result does NOT depend on it (direction (a), where the
	// per-issue mutex serializes the two attempts outright).
	delay time.Duration

	// barrierN, when > 1, makes every take announce that it has finished
	// its read phase and then WAIT until barrierN takes have done the same
	// before any of them writes. That is the deterministic form of "both
	// attempts are inside the window at once": it is true by construction
	// under any scheduling, at any load.
	barrierN int
}

// fakeClaimStore returns a path this package's own ScriptGate can be pointed
// at. Env config is installed with t.Setenv, so it is scoped to the test and
// restored afterwards; the child inherits it because exec.Command leaves
// Env nil (see claim.go's execCombined).
func fakeClaimStore(t *testing.T, lockDir string, opts storeOpts) string {
	t.Helper()
	t.Setenv(envStoreDir, lockDir)
	if opts.delay > 0 {
		t.Setenv(envStoreDelay, opts.delay.String())
	}
	if opts.barrierN > 1 {
		t.Setenv(envStoreBarrierDir, t.TempDir())
		t.Setenv(envStoreBarrierN, fmt.Sprint(opts.barrierN))
	}
	return os.Args[0]
}

// TestMain dispatches into the fake store when the test binary is the thing
// ScriptGate exec'd, and runs the ordinary suite otherwise.
func TestMain(m *testing.M) {
	if os.Getenv(envStoreDir) != "" && len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		os.Exit(storeHelperMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// storeHelperMain is the fake store's body, in the child process. Exit codes
// mirror claim.sh's contract as ScriptGate reads it: 0 taken/released, 1
// refused (already claimed), 2 unknown verb, plus 3 for a barrier that never
// completed -- which surfaces as a lost attempt in the control rather than a
// silent pass.
func storeHelperMain(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "fake claim store: want <verb> <issue> [repo] [lane]")
		return 2
	}
	verb, issue := args[0], args[1]
	lane := ""
	if len(args) > 3 {
		lane = args[3]
	}
	file := filepath.Join(os.Getenv(envStoreDir), "issue-"+issue)

	switch verb {
	case "take":
		if holder, taken := readHolder(file); taken {
			fmt.Printf("claimed by %s\n", holder)
			return 1
		}
		// --- the store's check-then-write window opens here ---
		if d, err := time.ParseDuration(os.Getenv(envStoreDelay)); err == nil && d > 0 {
			time.Sleep(d)
		}
		if holder, taken := readHolder(file); taken {
			fmt.Printf("claimed by %s\n", holder)
			return 1
		}
		// The read phase is over and nothing has been written yet: this is
		// the exact instant the barrier has to hold, because a peer released
		// any earlier could still write before this process finished reading
		// and would then be seen by the check above.
		if err := storeBarrier(lane); err != nil {
			fmt.Fprintf(os.Stderr, "fake claim store: %v\n", err)
			return 3
		}
		// --- and closes here: no compare-and-swap, same as the real thing ---
		if err := os.WriteFile(file, []byte(lane), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "fake claim store: %v\n", err)
			return 2
		}
		fmt.Printf("taken by %s\n", lane)
		return 0
	case "release":
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "fake claim store: %v\n", err)
			return 2
		}
		fmt.Println("released")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown verb %s\n", verb)
		return 2
	}
}

func readHolder(file string) (string, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// storeBarrier announces that this attempt has finished reading and blocks
// until every expected peer has announced the same. Both attempts are then
// provably inside the store's check-then-write window together -- the state
// the direction-(b) control asserts about, established by construction
// rather than by winning a scheduling race.
//
// Announcement is a file per lane in a shared directory: a peer's arrival is
// durable, so a peer that arrives BEFORE this process starts waiting still
// counts, and a peer scheduled arbitrarily late is simply waited for. That
// is the property a sleep cannot have.
func storeBarrier(lane string) error {
	dir := os.Getenv(envStoreBarrierDir)
	want := 0
	fmt.Sscanf(os.Getenv(envStoreBarrierN), "%d", &want)
	if dir == "" || want < 2 {
		return nil
	}
	if err := os.WriteFile(filepath.Join(dir, "arrived-"+lane), nil, 0o644); err != nil {
		return fmt.Errorf("barrier announce: %w", err)
	}
	deadline := time.Now().Add(storeBarrierTimeout)
	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("barrier read: %w", err)
		}
		if len(entries) >= want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("barrier timeout: %d of %d attempts reached the store's read phase within %s",
				len(entries), want, storeBarrierTimeout)
		}
		time.Sleep(time.Millisecond)
	}
}
