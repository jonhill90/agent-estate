package chat

import (
	"errors"
	"testing"
)

type fakeSource struct {
	threads []Thread
	err     error
}

func (f fakeSource) Threads() ([]Thread, error) { return f.threads, f.err }

// TestFallbackSourceUsesRealWhenConfigured is the "one present" half of
// agent-b3.md's own mutation-check bar: a real Source that returns actual
// threads must be used as-is, never replaced by the fixture.
func TestFallbackSourceUsesRealWhenConfigured(t *testing.T) {
	real := fakeSource{threads: []Thread{{ID: "real-1", Title: "a real thread"}}}
	fb := NewFallbackSource(real)

	threads, err := fb.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].ID != "real-1" {
		t.Fatalf("got %+v, want the real source's own thread untouched", threads)
	}
	if threads[0].Fixture {
		t.Error("a real thread must never come back tagged Fixture")
	}
}

// TestFallbackSourceUsesRealEmptyNotFixture is agent-b3.md's own named
// trap: a real Source that is configured and genuinely found zero threads
// (nil error, empty slice -- NOT ErrNoProjectDir) must render as real
// emptiness, never silently replaced by fixture content.
func TestFallbackSourceUsesRealEmptyNotFixture(t *testing.T) {
	real := fakeSource{threads: nil, err: nil}
	fb := NewFallbackSource(real)

	threads, err := fb.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v, want nil (real emptiness, not an error)", err)
	}
	if len(threads) != 0 {
		t.Fatalf("got %d threads, want 0 -- real's own honest empty answer", len(threads))
	}
}

// TestFallbackSourceSurfacesARealError is the same rule applied to a
// genuine read failure: real ran and hit an actual error (not
// ErrNoProjectDir) -- that must reach the caller as an error, never be
// swallowed into a fixture render that would hide the failure.
func TestFallbackSourceSurfacesARealError(t *testing.T) {
	wantErr := errors.New("boom: disk on fire")
	real := fakeSource{err: wantErr}
	fb := NewFallbackSource(real)

	_, err := fb.Threads()
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v surfaced unchanged", err, wantErr)
	}
}

// TestFallbackSourceFallsBackWhenUnconfigured is the "no real source
// present" half of the mutation-check bar: ErrNoProjectDir -- and ONLY
// that error -- must fall back to the fixture, with every thread tagged
// Fixture so the view can say so.
func TestFallbackSourceFallsBackWhenUnconfigured(t *testing.T) {
	real := fakeSource{err: ErrNoProjectDir}
	fb := NewFallbackSource(real)

	threads, err := fb.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v, want nil (fixture always succeeds)", err)
	}
	if len(threads) == 0 {
		t.Fatal("got 0 threads, want the fixture's own sample threads")
	}
	for i, th := range threads {
		if !th.Fixture {
			t.Errorf("thread %d (%q) not tagged Fixture after falling back", i, th.Title)
		}
	}
}

// TestFallbackSourceNilRealFallsBack covers real == nil (no real Source
// could even be constructed) the same way as a real Source that always
// answers ErrNoProjectDir -- NewFallbackSource's own documented contract.
func TestFallbackSourceNilRealFallsBack(t *testing.T) {
	fb := NewFallbackSource(nil)
	threads, err := fb.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v, want nil", err)
	}
	if len(threads) == 0 || !threads[0].Fixture {
		t.Fatalf("got %+v, want fixture threads", threads)
	}
}
