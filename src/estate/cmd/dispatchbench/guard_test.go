package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The load-bearing claim: two turns CANNOT overlap. Not "are not expected to"
// -- 200 goroutines all try, and the observed simultaneous count never leaves
// 1. A `serial` that merely queued would pass a "one at a time" test while
// still running every caller; this asserts the refusal too.
func TestSerialAdmitsExactlyOneTurnAtATime(t *testing.T) {
	var s serial
	var live, peak, ran, refused atomic.Int32
	var mu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Do(func() error {
				n := live.Add(1)
				mu.Lock()
				if n > peak.Load() {
					peak.Store(n)
				}
				mu.Unlock()
				time.Sleep(time.Millisecond)
				live.Add(-1)
				ran.Add(1)
				return nil
			})
			if errors.Is(err, errConcurrentTurn) {
				refused.Add(1)
			} else if err != nil {
				t.Errorf("unexpected error from serial.Do: %v", err)
			}
		}()
	}
	wg.Wait()

	if p := peak.Load(); p != 1 {
		t.Fatalf("two turns overlapped: peak simultaneous = %d, must be 1", p)
	}
	if ran.Load()+refused.Load() != 200 {
		t.Fatalf("accounting lost callers: ran=%d refused=%d", ran.Load(), refused.Load())
	}
	if refused.Load() == 0 {
		t.Fatal("no caller was refused across 200 concurrent attempts; serial is not actually contended, so this test proves nothing about it")
	}
}

// The mutation check for the test above: a `serial` with no gate at all must
// FAIL it. Without this, "peak == 1" could be an artifact of the goroutines
// never actually racing.
func TestSerialWithoutItsGateWouldOverlap(t *testing.T) {
	var live, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n := live.Add(1) // the ungated body, i.e. serial.Do with the counter removed
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			live.Add(-1)
		}()
	}
	wg.Wait()
	if peak.Load() <= 1 {
		t.Fatalf("the ungated body did not overlap either (peak=%d); the concurrency test above cannot distinguish a working gate from an idle scheduler", peak.Load())
	}
}

// Re-entrance is the shape a careless refactor actually takes -- a turn that
// runs a sub-turn -- and it must be refused, not deadlock.
func TestSerialRefusesReentrantTurn(t *testing.T) {
	var s serial
	var inner error
	outer := s.Do(func() error {
		inner = s.Do(func() error { return nil })
		return nil
	})
	if outer != nil {
		t.Fatalf("outer turn should have run: %v", outer)
	}
	if !errors.Is(inner, errConcurrentTurn) {
		t.Fatalf("a re-entrant turn was admitted; got %v", inner)
	}
}

// serial cannot see a second COPY of this binary, which is what the first
// attempt actually ran. The lock can, and this drives it through two real
// processes rather than two goroutines.
func TestSecondBenchmarkProcessCannotStart(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "dispatchbench")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	lock := filepath.Join(t.TempDir(), "run.lock")
	// -hold parks the process holding the lock, so the second one meets a
	// LIVE holder rather than a stale file.
	first := exec.Command(bin, "-lock", lock, "-hold", "10s")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = first.Process.Kill()
		_, _ = first.Process.Wait()
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(lock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first process never wrote its run lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	second := exec.Command(bin, "-lock", lock, "-hold", "1s")
	out, err := second.CombinedOutput()
	if err == nil {
		t.Fatalf("a second benchmark process started while the first held the lock; output:\n%s", out)
	}
	if !strings.Contains(string(out), "already running") {
		t.Fatalf("the second process failed for some other reason than the lock:\n%s", out)
	}
}

// ...and the lock must not wedge the host forever. A holder that is gone is
// stale, and stale is recoverable -- otherwise the first crash ends
// benchmarking on this machine until somebody deletes a file by hand.
func TestStaleLockFromADeadHolderIsBroken(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "run.lock")
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte(strconv.Itoa(dead.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := acquireRunLock(lock)
	if err != nil {
		t.Fatalf("a lock held by a dead pid was not broken: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
}

// An unreadable lock is refused rather than assumed stale: the same
// fail-closed disposition internal/pressure applies to a gauge it cannot read.
func TestUnreadableLockRefusesRatherThanAssumesStale(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "run.lock")
	if err := os.WriteFile(lock, []byte("not a pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireRunLock(lock); err == nil {
		t.Fatal("an unreadable lock file was treated as permission to run")
	}
}
