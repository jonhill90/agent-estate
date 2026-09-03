package knowledge

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/src/tui/internal/knowledgeindex"
)

// TestCPaneReachesCompiledIndexEndToEnd is agent-estate#946's own "must
// be reachable in the app" requirement, driven the same way this
// package's other Update-sequence tests already drive a key press:
// build a Model wired to a fake compiled-index Fetcher, press [c] from
// modeList, run the fetch it kicks off, and confirm the rendered view
// actually shows the compiled index's own content -- not just that the
// mode field flipped.
func TestCPaneReachesCompiledIndexEndToEnd(t *testing.T) {
	fake := func() (knowledgeindex.Result, error) {
		return knowledgeindex.Result{
			Sources: []knowledgeindex.SourceResult{{Name: "github-stars", OK: true, Count: 1}},
			Items:   []knowledgeindex.Item{{ID: "20260903000000", Source: "github-stars", Tier1: "a/one -- test repo"}},
		}, nil
	}
	m := New(nil, nil).WithCompiled(fake)

	// Init fires m.compiled's own fetch (this package's own Init doc
	// comment) -- run every command it returns until the compiled
	// pane's fetchResultMsg lands, the same shape a real tea.Program
	// would drive it.
	for _, cmd := range flattenCmds(m.Init()) {
		if cmd == nil {
			continue
		}
		next, _ := m.Update(cmd())
		m = next.(Model)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = next.(Model)
	if m.mode != modeCompiled {
		t.Fatal("[c] did not switch to modeCompiled")
	}

	got := m.View()
	if !contains(got, "a/one -- test repo") {
		t.Fatalf("View() after [c] does not show the compiled index's own item:\n%s", got)
	}
	if !contains(got, "github-stars") {
		t.Fatalf("View() after [c] does not show the compiled index's own source status:\n%s", got)
	}

	// [esc] must return to the vault's own list, not leave the pane
	// stuck showing the compiled index.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.mode != modeList {
		t.Fatal("[esc] from modeCompiled did not return to modeList")
	}
}

// flattenCmds unwraps a possibly-batched tea.Cmd into its leaf commands --
// tea.Batch's own return value is an unexported *batchMsg-producing
// closure this test cannot type-switch on, so it is invoked once and its
// result (a BatchMsg, or a single Msg) is normalized here instead.
func flattenCmds(cmd tea.Cmd) []tea.Cmd {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Cmd{func() tea.Msg { return msg }}
	}
	var out []tea.Cmd
	for _, c := range batch {
		out = append(out, flattenCmds(c)...)
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
