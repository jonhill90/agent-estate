package admin

// This file drives the real tea.Program via charmbracelet/x/exp/teatest --
// the same discipline internal/mcpservers/internal/skills' own
// model_teatest_test.go files use: send tea.Msg through the actual event
// loop, then read the actual rendered output.

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

// TestInitialFrameShowsAllFiveSectionsFromTheFakeFetch drives a real
// Program against a fake Fetcher (no real docker/LookPath/theme.json read
// -- adapter discipline, AGENTS.md) and asserts the actual rendered frame
// shows every one of S11's five named sections.
func TestInitialFrameShowsAllFiveSectionsFromTheFakeFetch(t *testing.T) {
	reachable := true
	fetch := func() (Snapshot, error) {
		return Snapshot{
			Services:     []Service{{Name: "mcp-vibes-server", Image: "vibes:latest", Status: "Up 3 days"}},
			Dependencies: []Dependency{{Name: "docker", Reachable: &reachable}},
			Settings:     []Setting{{Name: "theme", Value: "Signal (default)"}},
			ProfilesNote: noProfilesNote,
			UsersNote:    noUsersNote,
		}, nil
	}
	tm := run(t, New(fetch))
	out := waitFor(t, tm, "mcp-vibes-server")
	for _, want := range []string{"Dependencies", "Settings", "Profiles", "Users", noProfilesNote} {
		if !bytes.Contains(out, []byte(want)) {
			t.Fatalf("frame missing %q:\n%s", want, out)
		}
	}
}

// TestQQuitsARealProgram matches every other pane's own convention.
func TestQQuitsARealProgram(t *testing.T) {
	tm := run(t, New(nil))
	waitFor(t, tm, "admin")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
}
