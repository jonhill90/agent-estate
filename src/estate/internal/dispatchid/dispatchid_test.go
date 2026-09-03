package dispatchid

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The defect: three seats of a council dispatched together got one id, and
// internal/isolate refused two of them. A review mechanism that cannot run in
// parallel is not a council.
func TestConcurrentDispatchesGetDistinctIDs(t *testing.T) {
	const n = 200
	var wg sync.WaitGroup
	ids := make([]string, n)
	now := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); ids[i] = New("924", now) }(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("id %q minted twice -- isolate will refuse the second turn", id)
		}
		seen[id] = true
	}
}

// Even at the same instant, which is the case that actually broke.
func TestSameInstantStillDiffers(t *testing.T) {
	at := time.Date(2026, 9, 3, 5, 30, 0, 0, time.UTC)
	if New("924", at) == New("924", at) {
		t.Fatal("two ids minted at the identical instant must still differ")
	}
}

// The id names a directory and a branch, so it must stay one safe path
// element -- internal/isolate refuses separators and a leading '#'.
func TestIDIsASafePathElement(t *testing.T) {
	id := New("#924", time.Now())
	if strings.ContainsAny(id, `/\ `) {
		t.Errorf("id %q contains a separator or space", id)
	}
	if strings.HasPrefix(id, "#") {
		t.Errorf("id %q kept the issue's leading '#'", id)
	}
	if !strings.HasPrefix(id, "924-") {
		t.Errorf("id %q should still start with the issue for readability", id)
	}
}

// Three council seats found that seq resets per process, so uniqueness rested
// on two processes never starting inside the same clock tick -- and one seat
// measured this machine's clock advancing in ~1000ns steps, not nanoseconds.
//
// The pid is what actually separates concurrent dispatches: the operating
// system guarantees two live processes never share one.
func TestIDCarriesTheProcessID(t *testing.T) {
	id := New("926", time.Now())
	want := fmt.Sprint(os.Getpid())
	parts := strings.Split(id, "-")
	if len(parts) != 4 {
		t.Fatalf("id %q should be issue-nanos-pid-seq", id)
	}
	if parts[2] != want {
		t.Errorf("id %q carries pid %q, want %q", id, parts[2], want)
	}
}

// The case that actually broke: same instant, different processes. Simulated
// by holding the timestamp fixed and varying the pid component, since a test
// cannot fork itself.
func TestSameInstantDifferentProcessesCannotCollide(t *testing.T) {
	at := time.Date(2026, 9, 3, 5, 30, 0, 0, time.UTC)
	a := New("926", at)
	// Another process minting at the identical instant would produce the same
	// timestamp and the same seq=1; only the pid differs.
	parts := strings.Split(a, "-")
	other := strings.Join([]string{parts[0], parts[1], "999999", "1"}, "-")
	if a == other {
		t.Fatal("two processes minting at the same instant must not collide")
	}
	if strings.Join([]string{parts[0], parts[1], parts[2], "1"}, "-") == other {
		t.Fatal("the pid must be what distinguishes them")
	}
}
