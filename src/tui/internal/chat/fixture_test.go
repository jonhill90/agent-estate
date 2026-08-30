package chat

import "testing"

func TestFixtureSourceIsDeterministic(t *testing.T) {
	src := NewFixtureSource()
	a, err := src.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	b, err := src.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("call 1 returned %d threads, call 2 returned %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID || len(a[i].Messages) != len(b[i].Messages) {
			t.Errorf("thread %d differs between calls: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// TestFixtureCoversEveryMessageKind ensures the fixture actually exercises
// everything render.go/model.go must handle -- a fixture that silently
// drifted to only user/agent text would let a broken thought/tool/plan/
// permission render path pass every test in this package undetected.
func TestFixtureCoversEveryMessageKind(t *testing.T) {
	threads, err := NewFixtureSource().Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	seen := map[MessageKind]bool{}
	for _, th := range threads {
		for _, m := range th.Messages {
			seen[m.Kind] = true
		}
	}
	for _, want := range []MessageKind{KindUserText, KindAgentText, KindThought, KindToolCall, KindPlan, KindPermission} {
		if !seen[want] {
			t.Errorf("fixture never produces a %q message", want)
		}
	}
}

// TestFixtureHasAThreadLongerThanAnyRealisticPane is the fixture half of
// agent-tui#20's scrolling acceptance item: at least one thread must have
// enough messages that no reasonable terminal renders it whole, so every
// test and every manual verification pass actually exercises the scroll
// path rather than happening to fit.
func TestFixtureHasAThreadLongerThanAnyRealisticPane(t *testing.T) {
	threads, err := NewFixtureSource().Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	longest := 0
	for _, th := range threads {
		if n := len(RenderTranscript(th.Messages)); n > longest {
			longest = n
		}
	}
	const minRealisticPaneHeight = 15
	if longest <= minRealisticPaneHeight {
		t.Errorf("longest rendered transcript is %d lines, want > %d to force scrolling", longest, minRealisticPaneHeight)
	}
}
