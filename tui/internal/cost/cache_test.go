package cost

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCached_CollapsesConcurrentCallers is agent-tui#145's mutation check:
// three independent callers asking inside the same ttl window (the exact
// shape railModel/costModel/dashboardModel put on a shared costFetch) must
// produce exactly one underlying fetch, not three. Without Cached this
// test fails with calls == 3 -- see this file's own
// TestCached_WithoutWrapper_EachCallerFetchesIndependently, which proves
// the counterfactual explicitly rather than leaving it to be taken on
// faith.
func TestCached_CollapsesConcurrentCallers(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	fetch := Fetcher(func() (Snapshot, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return KnownSnapshotForTest(), nil
	})

	cached := Cached(fetch, 5*time.Minute, time.Now)

	var wg sync.WaitGroup
	results := make([]Snapshot, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			snap, err := cached()
			if err != nil {
				t.Errorf("caller %d: unexpected error %v", i, err)
			}
			results[i] = snap
		}(i)
	}

	// Give all three goroutines a chance to call in and either become the
	// in-flight fetch or queue behind it before releasing the fetch.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("underlying fetch called %d times for 3 concurrent callers, want 1", got)
	}
	for i, snap := range results {
		if !snap.Known {
			t.Errorf("caller %d: got unknown snapshot, want the shared known result", i)
		}
	}
}

// TestCached_WithoutWrapper_EachCallerFetchesIndependently is the reverse
// half of the mutation check: the same three-caller shape against the RAW
// fetch (no Cached wrapper) really does produce three calls, proving the
// storm this fix closes is real and not an artifact of the test itself.
func TestCached_WithoutWrapper_EachCallerFetchesIndependently(t *testing.T) {
	var calls int32
	fetch := Fetcher(func() (Snapshot, error) {
		atomic.AddInt32(&calls, 1)
		return KnownSnapshotForTest(), nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fetch()
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("raw fetch called %d times for 3 independent callers, want 3 (the storm this test documents)", got)
	}
}

// TestCached_RefetchesAfterTTL proves Cached does not cache forever --
// costModel/railModel/dashboardModel each still expect a fresh number
// after their own 5-minute tick, and Cached must let that real refetch
// through once ttl has elapsed rather than serving a permanently stale
// Snapshot.
func TestCached_RefetchesAfterTTL(t *testing.T) {
	var calls int32
	fetch := Fetcher(func() (Snapshot, error) {
		atomic.AddInt32(&calls, 1)
		return KnownSnapshotForTest(), nil
	})

	now := time.Now()
	clock := func() time.Time { return now }
	cached := Cached(fetch, time.Minute, clock)

	if _, err := cached(); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := cached(); err != nil {
		t.Fatalf("second call (within ttl): %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls within ttl = %d, want 1 (cached)", got)
	}

	now = now.Add(2 * time.Minute)
	if _, err := cached(); err != nil {
		t.Fatalf("third call (past ttl): %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls after ttl elapsed = %d, want 2 (one real refetch)", got)
	}
}

// KnownSnapshotForTest is a minimal Known Snapshot for this file's own
// tests -- not exported outside _test.go, deliberately: no other package
// should construct a Snapshot by hand instead of going through a real
// Fetcher or Compose.
func KnownSnapshotForTest() Snapshot {
	return Snapshot{Known: true}
}
