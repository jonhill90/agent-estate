package knowledge

// This file drives the real tea.Program via charmbracelet/x/exp/teatest --
// the same discipline every other package in this module's own
// model_teatest_test.go files use.

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func run(t *testing.T, m Model) *teatest.TestModel {
	t.Helper()
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 24))
	t.Cleanup(func() { _ = tm.Quit() })
	return tm
}

func waitFor(t *testing.T, tm *teatest.TestModel, want string) []byte {
	t.Helper()
	var b bytes.Buffer
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		buf := make([]byte, 65536)
		n, _ := tm.Output().Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if bytes.Contains(b.Bytes(), []byte(want)) {
			return b.Bytes()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitFor %q: not seen after 8s. Output so far:\n%s", want, b.String())
	return nil
}

// TestInitialFrameShowsFactsFromTheFakeFetch drives a real Program against
// a fake Fetcher (no real filesystem read -- adapter discipline,
// AGENTS.md).
func TestInitialFrameShowsFactsFromTheFakeFetch(t *testing.T) {
	fetch := func() ([]IndexEntry, error) {
		return []IndexEntry{{Slug: "my-fact", Title: "My fact", Description: "a real description"}}, nil
	}
	tm := run(t, New(fetch, nil))
	out := waitFor(t, tm, "my-fact")
	if !bytes.Contains(out, []byte("a real description")) {
		t.Fatalf("View() missing the description:\n%s", out)
	}
}

// TestEnterOpensAFactAgainstARealProgram is the pty-level half of
// TestEnterOpensAFactAndCachesIt.
func TestEnterOpensAFactAgainstARealProgram(t *testing.T) {
	fetch := func() ([]IndexEntry, error) {
		return []IndexEntry{{Slug: "my-fact", Title: "My fact"}}, nil
	}
	loadFact := func(slug string) (Fact, error) {
		return Fact{Slug: slug, Type: "project", Body: "the real body of the fact"}, nil
	}
	tm := run(t, New(fetch, loadFact))
	waitFor(t, tm, "my-fact")

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "the real body of the fact")
}

// TestQQuitsARealProgram matches every other pane's own convention.
func TestQQuitsARealProgram(t *testing.T) {
	tm := run(t, New(nil, nil))
	waitFor(t, tm, "knowledge")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
}
