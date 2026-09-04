package library

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
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 30))
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

// TestInitialFrameShowsItemsFromTheFakeFetch drives a real Program against
// a fake Fetcher (no real ledger read -- adapter discipline, AGENTS.md).
func TestInitialFrameShowsItemsFromTheFakeFetch(t *testing.T) {
	fetch := fakeFetch([]ItemRow{{ID: "it-realid00000001", Kind: "parameter", Weight: "hard", Status: "acknowledged", ResolvedTo: "ui_fidelity=1:1", BodySnippet: "a real snippet"}}, nil)
	tm := run(t, New(fetch, nil, nil))
	out := waitFor(t, tm, "it-realid00000001")
	if !bytes.Contains(out, []byte("a real snippet")) {
		t.Fatalf("View() missing the body snippet:\n%s", out)
	}
}

// TestEnterOpensAnItemAgainstARealProgram is the pty-level half of
// TestEnterOpensAnItemAndCachesIt.
func TestEnterOpensAnItemAgainstARealProgram(t *testing.T) {
	fetch := fakeFetch([]ItemRow{{ID: "it-realid00000001", Kind: "parameter"}}, nil)
	loadDetail := func(id string) (ItemDetail, error) {
		return ItemDetail{ID: id, Body: "the real full body of the item"}, nil
	}
	tm := run(t, New(fetch, loadDetail, nil))
	waitFor(t, tm, "it-realid00000001")

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "the real full body of the item")
}

// TestVKeyCyclesViewAgainstARealProgram proves the [v] key actually
// re-fetches through a real Program, not just Update() called by hand.
func TestVKeyCyclesViewAgainstARealProgram(t *testing.T) {
	calls := 0
	fetch := func(v View, weight, status string) ([]ItemRow, error) {
		calls++
		return []ItemRow{{ID: "it-realid00000001", Kind: "parameter", BodySnippet: string(v)}}, nil
	}
	tm := run(t, New(fetch, nil, nil))
	waitFor(t, tm, "needs_review")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	waitFor(t, tm, "live_parameters")
}

// TestDKeyShowsTheQueueAgainstARealProgram proves [d] actually switches to
// QueueFiledAsLaw's own rows through a real Program, not just Update()
// called by hand -- the pty-level half of TestDKeyTogglesQueueAndReFetches
// (agent-estate#1094).
func TestDKeyShowsTheQueueAgainstARealProgram(t *testing.T) {
	src := Source{
		Name: "shared",
		Fetch: func(View, string, string) ([]ItemRow, error) {
			return []ItemRow{{ID: "it-view0000000001", Kind: "parameter"}}, nil
		},
		FetchQueue: func(Queue) ([]ItemRow, error) {
			return []ItemRow{{ID: "it-lawq0000000001", Kind: "question", Status: "acted"}}, nil
		},
	}
	tm := run(t, NewSources([]Source{src}))
	waitFor(t, tm, "it-view0000000001")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	waitFor(t, tm, "it-lawq0000000001")
}

// TestQQuitsARealProgram matches every other pane's own convention.
func TestQQuitsARealProgram(t *testing.T) {
	tm := run(t, New(nil, nil, nil))
	waitFor(t, tm, "library")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
}
