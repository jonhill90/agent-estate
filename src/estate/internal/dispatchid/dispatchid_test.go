package dispatchid

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain lets this same test binary act as its own subprocess helper.
// TestNew_ConcurrentProcesses re-execs os.Args[0] with DISPATCHID_HELPER=1 so
// each child is a genuinely separate OS process -- not a goroutine sharing
// this process's memory and its package-level seq counter -- calling New and
// printing exactly the id it got. This is the technique #933 used and #926
// was faulted for not using: a hand-constructed "other process" id proves
// nothing about what a second live process would actually mint.
func TestMain(m *testing.M) {
	if os.Getenv("DISPATCHID_HELPER") == "1" {
		fmt.Println(New(os.Getenv("DISPATCHID_ISSUE"), time.Now()))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// assertSafe fails t if id could not name a worktree directory and a branch
// -- isolate.safeID refuses anything else, and an id this package hands out
// must satisfy the same rule isolate enforces.
func assertSafe(t *testing.T, id string) {
	t.Helper()
	if id == "" {
		t.Fatalf("empty id")
	}
	if !safeIDPattern.MatchString(id) {
		t.Fatalf("id %q is not a single safe path element", id)
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		t.Fatalf("id %q could escape a worktree root", id)
	}
	if strings.Trim(id, ".") == "" {
		t.Fatalf("id %q names a directory rather than a worktree", id)
	}
}

func TestNew_ReturnsSafeID(t *testing.T) {
	id := New("938", time.Now())
	assertSafe(t, id)
	if !strings.HasPrefix(id, "938-") {
		t.Fatalf("id %q does not carry its issue number", id)
	}
}

func TestNew_StripsIssueHash(t *testing.T) {
	id := New("#938", time.Now())
	if strings.Contains(id, "#") {
		t.Fatalf("id %q kept the issue's leading '#'", id)
	}
}

func TestNew_CarriesTheProcessID(t *testing.T) {
	id := New("938", time.Now())
	parts := strings.Split(id, "-")
	if len(parts) != 4 {
		t.Fatalf("id %q should be issue-nanos-pid-seq, got %d parts", id, len(parts))
	}
	want := fmt.Sprint(os.Getpid())
	if parts[2] != want {
		t.Errorf("id %q carries pid %q, want %q", id, parts[2], want)
	}
}

// The cheap, fast check: concurrent goroutines in one process. It is NOT the
// proof this fix rests on -- see TestNew_ConcurrentProcesses below for why a
// goroutine-only suite would stay green even for a scheme with no cross-
// process guarantee at all (a package-level seq counter alone would pass
// this test and still collide across two real `estate dispatch` processes).
func TestNew_ConcurrentGoroutines(t *testing.T) {
	const n = 200
	ids := make([]string, n)
	now := time.Now()
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) { defer wg.Done(); ids[i] = New("939", now) }(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, id := range ids {
		assertSafe(t, id)
		if seen[id] {
			t.Fatalf("id %q minted twice across concurrent goroutines", id)
		}
		seen[id] = true
	}
}

// TestNew_ConcurrentProcesses is the proof the brief asks for: real,
// independent OS processes -- not goroutines sharing this process's memory,
// and not a hand-constructed peer id (#926's weakness, per #938). Each child
// below re-execs this same test binary and calls New for itself; the
// timestamp each child sees is effectively identical (they are all launched
// within the same instant), so this exercises exactly the case that broke a
// council: several dispatches racing at once.
//
// This test fails against a scheme with no pid component -- e.g. the
// original `<issue>-<unix-seconds>` in main.go before this package existed,
// or #933's timestamp-plus-random-suffix-with-no-shared-arbiter -- because
// nothing then distinguishes two processes that start in the same instant.
// It passes against New because the pid is a kernel-guaranteed distinguisher
// that needs no cross-process coordination at all.
func TestNew_ConcurrentProcesses(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	const n = 8
	type result struct {
		id  string
		err error
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(self)
			cmd.Env = append(os.Environ(),
				"DISPATCHID_HELPER=1",
				"DISPATCHID_ISSUE=940",
			)
			out, err := cmd.Output()
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					err = fmt.Errorf("%w: stderr: %s", err, ee.Stderr)
				}
				results[i] = result{err: err}
				return
			}
			results[i] = result{id: strings.TrimSpace(string(out))}
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("child process %d: %v", i, r.err)
		}
		assertSafe(t, r.id)
		if seen[r.id] {
			t.Fatalf("two separate OS processes minted the same id %q -- this is the exact bug #938 tracks: %d processes requested, ids seen so far: %v", r.id, n, seen)
		}
		seen[r.id] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct ids from %d separate OS processes, got %d", n, n, len(seen))
	}
}

// TestSameInstantStillDiffers documents the failure mode directly: minting
// twice at the identical time.Time value, within one process, must not
// collide -- this is what a second-precision scheme (the original
// implementation) got wrong.
func TestSameInstantStillDiffers(t *testing.T) {
	at := time.Date(2026, 9, 3, 5, 30, 0, 0, time.UTC)
	if New("941", at) == New("941", at) {
		t.Fatal("two ids minted at the identical instant, in the same process, must still differ")
	}
}
