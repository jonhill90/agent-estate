package dispatchid

import (
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
