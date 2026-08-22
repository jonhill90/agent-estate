package claim

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeArgvEcho writes a stub standing in for claim.sh that just prints its
// own argv, one per line, and exits 0 -- for tests about what ScriptGate
// SENDS, not what a real claim.sh does with it.
func fakeArgvEcho(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake binary is a POSIX shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claim-echo")
	script := "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\"; done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake script: %v", err)
	}
	return path
}

func TestTake_ArgvShape_WithRepoAndLane(t *testing.T) {
	g := &ScriptGate{ScriptPath: fakeArgvEcho(t)}
	// The fake echoes argv to its own stdout; Take swallows a non-error
	// exit's output, so drive run() directly to inspect it -- Take/Release
	// are thin locking wrappers around it (see claim.go).
	out, err := runCapture(g, "take", 42, "o/r", "lane-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"take", "42", "o/r", "lane-1"}
	got := strings.Split(strings.TrimSpace(out), "\n")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("argv = %v, want %v", got, want)
	}
}

func TestTake_ArgvShape_NoRepo(t *testing.T) {
	g := &ScriptGate{ScriptPath: fakeArgvEcho(t)}
	out, err := runCapture(g, "take", 7, "", "lane-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// repo is still sent as an (empty) positional slot -- claim.sh reads
	// $3 either way; omitting the arg entirely would shift lane into repo's
	// position instead.
	want := []string{"take", "7", "", "lane-1"}
	got := strings.Split(strings.TrimSpace(out), "\n")
	if len(got) != len(want) || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("argv = %v, want %v (repo sent as an empty positional, lane still 4th positional)", got, want)
	}
}

// runCapture is a test-only helper that calls the same code path Take does,
// but returns the fake script's stdout for inspection instead of discarding
// it on success.
func runCapture(g *ScriptGate, verb string, issue int, repo, lane string) (string, error) {
	args := []string{verb, strconv.Itoa(issue), repo}
	if lane != "" {
		args = append(args, lane)
	}
	return execCombined(context.Background(), g.ScriptPath, args)
}

func TestScriptGate_NoScriptPathRefuses(t *testing.T) {
	g := &ScriptGate{}
	if err := g.Take(context.Background(), 1, "", "lane"); err == nil {
		t.Fatal("Take with no ScriptPath returned nil error, want a refusal")
	}
}

// fakeClaimStore is claim.sh's own take/release verbs, reproduced as a
// stub over a plain lock-file directory -- real enough to exercise
// ScriptGate's actual subprocess path (argv, exit code, stdout), and
// deliberately reproducing claim.sh's OWN documented race (check, THEN
// write, no compare-and-swap) via a configurable sleep between the two,
// so the mutation-check tests below can actually widen or close that
// window rather than asserting against an idealised atomic fake.
func fakeClaimStore(t *testing.T, lockDir string, raceDelay time.Duration) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake binary is a POSIX shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claim-store")
	sleep := ""
	if raceDelay > 0 {
		sleep = "sleep " + raceDelay.String() + "\n"
	}
	script := `#!/bin/sh
verb="$1"; issue="$2"; repo="$3"; lane="${4:-}"
file="` + lockDir + `/issue-$issue"
case "$verb" in
  take)
    if [ -f "$file" ]; then echo "claimed by $(cat "$file")"; exit 1; fi
` + sleep + `    if [ -f "$file" ]; then echo "claimed by $(cat "$file")"; exit 1; fi
    echo "$lane" > "$file"
    echo "taken by $lane"
    exit 0 ;;
  release)
    rm -f "$file"
    echo "released"
    exit 0 ;;
  *) echo "unknown verb $verb" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claim store: %v", err)
	}
	return path
}

// TestTakeThenTake_SecondRefuses is the ordinary, non-concurrent case:
// once an issue is taken, a second Take for the same issue must refuse,
// naming who holds it.
func TestTakeThenTake_SecondRefuses(t *testing.T) {
	lockDir := t.TempDir()
	g := &ScriptGate{ScriptPath: fakeClaimStore(t, lockDir, 0)}
	if err := g.Take(context.Background(), 1, "", "lane-a"); err != nil {
		t.Fatalf("first Take: %v", err)
	}
	err := g.Take(context.Background(), 1, "", "lane-b")
	if err == nil {
		t.Fatal("second Take succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), "lane-a") {
		t.Fatalf("refusal %q does not name the holder", err.Error())
	}
}

func TestRelease_ThenTakeAgain_Succeeds(t *testing.T) {
	lockDir := t.TempDir()
	g := &ScriptGate{ScriptPath: fakeClaimStore(t, lockDir, 0)}
	if err := g.Take(context.Background(), 1, "", "lane-a"); err != nil {
		t.Fatalf("Take: %v", err)
	}
	if err := g.Release(context.Background(), 1, ""); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := g.Take(context.Background(), 1, "", "lane-b"); err != nil {
		t.Fatalf("Take after Release: %v", err)
	}
}

// MUTATION-CHECK, direction (a) — the guard fires: two goroutines dispatch
// the SAME issue near-simultaneously through the real ScriptGate.Take
// (per-issue mutex engaged). Exactly one must succeed; the other must
// refuse with a real reason. raceDelay widens the fake store's own
// check-then-write window well past normal goroutine scheduling jitter,
// so this is not passing by accident of timing.
func TestConcurrentTake_SameIssue_ExactlyOneWins(t *testing.T) {
	lockDir := t.TempDir()
	g := &ScriptGate{ScriptPath: fakeClaimStore(t, lockDir, 50*time.Millisecond)}

	var wg sync.WaitGroup
	results := make([]error, 2)
	lanes := []string{"lane-a", "lane-b"}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = g.Take(context.Background(), 99, "", lanes[i])
		}(i)
	}
	wg.Wait()

	wins, losses := 0, 0
	for _, err := range results {
		if err == nil {
			wins++
		} else {
			losses++
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("wins=%d losses=%d (want exactly 1 each) -- results=%v", wins, losses, results)
	}
}

// MUTATION-CHECK, direction (b) — the guard does not pass by accident:
// with the per-issue serialization BYPASSED (calling the unlocked verb
// runner directly, the same shape as if ScriptGate.Take never took its
// lock), the exact same near-simultaneous dispatch against the exact same
// fake store DOES let both attempts through -- proving direction (a)'s
// single-winner result comes from the mutex, not from the fake store
// happening to be safe on its own (it is not; this is its own documented
// race, reproduced deliberately, same as claim.sh's real header comment
// describes for the underlying GitHub API it wraps).
func TestConcurrentTake_SameIssue_WithoutTheGuard_RaceReproduces(t *testing.T) {
	lockDir := t.TempDir()
	scriptPath := fakeClaimStore(t, lockDir, 50*time.Millisecond)

	var wg sync.WaitGroup
	results := make([]error, 2)
	lanes := []string{"lane-a", "lane-b"}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Bypasses ScriptGate.Take's lockFor(issue) entirely -- calls
			// the same subprocess run the real Take wraps, unserialized.
			_, results[i] = execCombined(context.Background(), scriptPath, []string{"take", "99", "", lanes[i]})
		}(i)
	}
	wg.Wait()

	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 2 {
		t.Fatalf("wins=%d, want 2 (both should have won without the mutex -- "+
			"if this fails, the fake store stopped reproducing the race this "+
			"test exists to prove ScriptGate's mutex actually closes)", wins)
	}
}
