package external

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func pressed(m Model, s string) (Model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return next.(Model), cmd
}

// TestViewNamesTheDestinationAndNeverClaimsAPaneIsComing is the whole
// point of this package: Platform Docs rendered "not built yet" for as
// long as it was routed to internal/stub, and no pane is ever coming for
// a Mintlify site.
func TestViewNamesTheDestinationAndNeverClaimsAPaneIsComing(t *testing.T) {
	m := New(nil).WithDestination("Platform Docs", "https://docs.hill90.com")
	out := m.View()
	if !strings.Contains(out, "https://docs.hill90.com") {
		t.Errorf("View() does not name the URL:\n%s", out)
	}
	if strings.Contains(out, "not built yet") {
		t.Errorf("View() still reads as an unbuilt stub:\n%s", out)
	}
	if !strings.Contains(out, "browser") {
		t.Errorf("View() does not say where it opens:\n%s", out)
	}
}

func TestOpenCallsTheOpenerWithExactlyTheNavURL(t *testing.T) {
	var got []string
	m := New(func(url string) error {
		got = append(got, url)
		return nil
	}).WithDestination("Platform Docs", "https://docs.hill90.com")

	_, cmd := pressed(m, "o")
	if cmd == nil {
		t.Fatal("[o] returned no command")
	}
	msg := cmd()
	if len(got) != 1 || got[0] != "https://docs.hill90.com" {
		t.Fatalf("opener saw %v, want exactly the nav URL", got)
	}
	res, ok := msg.(openResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("open result = %#v", msg)
	}

	next, _ := m.Update(res)
	if out := next.(Model).View(); !strings.Contains(out, "handed to your browser") {
		t.Errorf("a successful open is not reported:\n%s", out)
	}
}

// TestOpenFailureIsReported: a browser that would not start must be said
// out loud -- the failure is otherwise completely invisible, since the
// browser opens behind the terminal even when it works.
func TestOpenFailureIsReported(t *testing.T) {
	m := New(func(string) error { return errors.New("exec: \"xdg-open\": not found") }).
		WithDestination("Platform Docs", "https://docs.hill90.com")
	_, cmd := pressed(m, "o")
	next, _ := m.Update(cmd())
	out := next.(Model).View()
	if !strings.Contains(out, "could not open it") || !strings.Contains(out, "xdg-open") {
		t.Errorf("View() hides the open failure:\n%s", out)
	}
}

// TestNilOpenerStillShowsTheURL: with no opener wired, the URL is the part
// a human can still act on, so it must stay on screen.
func TestNilOpenerStillShowsTheURL(t *testing.T) {
	m := New(nil).WithDestination("Platform Docs", "https://docs.hill90.com")
	if _, cmd := pressed(m, "o"); cmd != nil {
		t.Error("[o] with a nil opener returned a command")
	}
	out := m.View()
	if !strings.Contains(out, "https://docs.hill90.com") || !strings.Contains(out, "no browser opener") {
		t.Errorf("View() with no opener:\n%s", out)
	}
}

// TestNoURLIsAVisibleState rather than a blank pane -- if a KindExternal
// item ever reaches this Model without an href, the pane says so instead
// of rendering an empty box that looks like a broken screen.
func TestNoURLIsAVisibleState(t *testing.T) {
	out := New(nil).WithDestination("Some Route", "").View()
	if !strings.Contains(out, "no URL recorded") {
		t.Errorf("View() with no URL:\n%s", out)
	}
}

// TestSwitchingDestinationClearsTheLastOpenResult: otherwise a second
// external route would inherit "handed to your browser" from the first
// and claim something that never happened for it.
func TestSwitchingDestinationClearsTheLastOpenResult(t *testing.T) {
	m := New(func(string) error { return nil }).WithDestination("Platform Docs", "https://docs.hill90.com")
	_, cmd := pressed(m, "o")
	next, _ := m.Update(cmd())
	m = next.(Model).WithDestination("Other Docs", "https://example.invalid")
	if out := m.View(); strings.Contains(out, "handed to your browser") {
		t.Errorf("stale open result survived a destination change:\n%s", out)
	}
}

func TestQuitKeysStillQuit(t *testing.T) {
	m := New(nil).WithDestination("Platform Docs", "https://docs.hill90.com")
	if _, cmd := pressed(m, "q"); cmd == nil {
		t.Error("[q] did not quit")
	}
}
