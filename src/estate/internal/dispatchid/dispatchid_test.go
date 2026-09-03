package dispatchid

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/isolate"
)

// TestMain lets this same test binary act as its own subprocess helper.
// TestNew_ConcurrentProcesses re-execs os.Args[0] with DISPATCHID_HELPER=1 so
// each child is a genuinely separate OS process -- not a goroutine sharing
// this process's memory -- calling New and printing exactly the id it got.
//
// The helper branch does NOT set claimDirEnv itself -- it inherits whatever
// TestNew_ConcurrentProcesses put in cmd.Env, which is the parent test's own
// t.TempDir(). That is deliberate: it is what proves the override seam
// actually reaches a child process, not just the parent's own calls to New.
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

// useScratchClaimDir points claimDir() at a fresh t.TempDir() for the
// duration of one test, instead of the real $TMPDIR/estate-dispatch-ids
// every `estate dispatch` invocation uses. Without this, `go test ./...`
// pollutes production dispatch state -- see #936, which counted 247 stale
// claim files left by test runs on one machine.
func useScratchClaimDir(t *testing.T) {
	t.Helper()
	t.Setenv(claimDirEnv, t.TempDir())
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
	useScratchClaimDir(t)
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
	useScratchClaimDir(t)
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
	// Setenv here reaches the children below too: cmd.Env is built from
	// os.Environ() per child, read after this line runs, and TestMain's
	// helper branch (in the child) calls New with no override of its own --
	// see TestMain's doc comment for why that's the point.
	useScratchClaimDir(t)

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

// TestClaimDirSharesIsolateRoot asserts, rather than just narrates in a
// comment, the assumption reviewed on PR #933: this package's cross-process
// guarantee depends on every dispatching process sharing one $TMPDIR, and
// that dependency is inherited from internal/isolate.Root, not introduced
// here. It deliberately does NOT set claimDirEnv -- it checks the real,
// unoverridden default both functions fall back to.
func TestClaimDirSharesIsolateRoot(t *testing.T) {
	t.Setenv(claimDirEnv, "")

	gotClaimDir := filepath.Dir(claimDir())
	gotIsolateRoot := filepath.Dir(filepath.Dir(isolate.Root(t.TempDir())))
	tmp := filepath.Clean(os.TempDir())

	if gotClaimDir != tmp {
		t.Fatalf("claimDir()'s parent = %q, want os.TempDir() %q -- if this changed, the doc comment's claim that this package rides isolate.Root's own $TMPDIR assumption is no longer true and must be re-checked", gotClaimDir, tmp)
	}
	if gotIsolateRoot != tmp {
		t.Fatalf("isolate.Root(...)'s grandparent = %q, want os.TempDir() %q -- isolate.Root's own layout changed; re-verify the shared-$TMPDIR assumption this package's doc comment relies on", gotIsolateRoot, tmp)
	}
}

// TestNew_PrunesStaleClaimsOnly proves the sweep removes only claims older
// than staleAge and leaves a fresh one (like the id New is about to mint)
// alone -- a sweep that raced its own in-flight claim would reintroduce the
// exact collision this package exists to prevent.
func TestNew_PrunesStaleClaimsOnly(t *testing.T) {
	useScratchClaimDir(t)
	dir := claimDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	stale := filepath.Join(dir, "932-stale")
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	old := time.Now().Add(-staleAge - time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	fresh := filepath.Join(dir, "932-fresh")
	if err := os.WriteFile(fresh, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	id, err := New("932")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	assertSafe(t, id)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale claim file survived the sweep: err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh claim file was swept away: %v", err)
	}
}
