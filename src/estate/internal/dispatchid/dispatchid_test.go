package dispatchid

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// TestMain lets this same test binary act as its own subprocess helper.
// TestNew_ConcurrentProcesses re-execs os.Args[0] with DISPATCHID_HELPER=1 so
// each child is a genuinely separate OS process -- not a goroutine sharing
// this process's memory -- calling New and printing exactly the id it got.
func TestMain(m *testing.M) {
	if os.Getenv("DISPATCHID_HELPER") == "1" {
		id, err := New(os.Getenv("DISPATCHID_ISSUE"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(id)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// assertSafe fails t if id could not name a worktree directory and a branch
// -- isolate.Create refuses anything else, so an id this package hands out
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
	id, err := New("929")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	assertSafe(t, id)
	if !strings.HasPrefix(id, "929-") {
		t.Fatalf("id %q does not carry its issue number", id)
	}
}

// TestNew_ConcurrentGoroutines is the cheap, fast check. It is NOT the proof
// this fix rests on -- see TestNew_ConcurrentProcesses below for why a
// goroutine-only suite would have stayed green all night while the real bug
// (no shared state across separate `estate dispatch` invocations) went
// untouched.
func TestNew_ConcurrentGoroutines(t *testing.T) {
	const n = 50
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			ids[i], errs[i] = New("930")
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("New (goroutine %d): %v", i, err)
		}
		assertSafe(t, ids[i])
		if seen[ids[i]] {
			t.Fatalf("duplicate id %q across concurrent goroutines", ids[i])
		}
		seen[ids[i]] = true
	}
}

// TestNew_ConcurrentProcesses is the proof the brief asks for: it fails
// against the previous implementation (issue + time.Now().UTC().Unix()),
// because that scheme has one-second resolution and every child below is
// launched inside the same second, from three concurrent seats' worth of
// real OS processes -- not goroutines inside one process, which would share
// the state a per-process counter or a package-level mutex sits in and so
// would never exercise the bug this package exists to fix.
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
				"DISPATCHID_ISSUE=931",
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
			t.Fatalf("two separate OS processes minted the same id %q -- this is the exact bug: %d ids requested, ids seen so far: %v", r.id, n, seen)
		}
		seen[r.id] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct ids from %d processes, got %d", n, n, len(seen))
	}
}
