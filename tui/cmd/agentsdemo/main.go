// agentsdemo renders internal/agents' standalone Agents view against fixed
// fixture lanes, so the MODE column can be judged (and screenshotted) on
// its own without a live agent-supervisor daemon behind it -- the same
// role cmd/demo plays for the full shell (SPEC-shell.md's own "a visible
// layout beats an assertion" precedent).
//
// Restores testdata/vhs/agents-mode.tape's own build step
// (`go build -o /tmp/agentsdemo ./cmd/agentsdemo`), which has referenced
// this path since the tape was added in #91 (49887de) -- that PR's diff
// added the tape but never committed the binary it names, so the tape has
// been unrunnable since it landed. Measured, not assumed: `git log
// --diff-filter=D -- 'cmd/agentsdemo/*'` finds no deletion commit either;
// the file was simply never created.
//
// Fixture lanes are chosen to exercise every value internal/agents.modeFor
// can currently produce (row.go's own doc comment): a live lane with a
// Command reports ExecutionLocal, and a dead/stale or command-less lane
// reports nil ("unknown" in the MODE column) -- container mode is
// deliberately absent from this fixture because modeFor cannot produce it
// yet (session.ErrContainerNotImplemented's own doc comment; no signal in
// lanes.sh --json can positively identify one), and a fixture claiming
// otherwise would be exactly the fabricated-value AGENTS.md forbids.
//
// agent-tui#130 review (estate:2): the rendered frame's own row values
// (session names, states, models) are realistic-looking fixture data with
// nothing on screen marking them as fake, unlike cmd/demo -- see
// disclaimerModel below.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/agents"
	"github.com/jonhill90/agent-tui/internal/lane"
	"github.com/jonhill90/agent-tui/internal/theme"
)

// bannerStyle reuses RoleWarn, the same role board/cost already use for
// "this number is not what it looks like" (registry.go's own comment on
// RoleWarn), rather than a color invented just for this file.
var bannerStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Default.Color(theme.RoleWarn))

// disclaimerModel wraps agents.Model with a visible fake-data banner,
// mirroring cmd/demo's own `ALL DATA ON THIS SCREEN IS FAKE` footer
// (cmd/demo/main.go's View) rather than inventing a second convention.
// internal/agents.Model has no disclaimer/demo-mode parameter to opt into
// (its own New/WithTheme signatures carry no such field, checked directly)
// and deliberately shouldn't grow one just for this: this package's whole
// point is rendering the REAL production view unmodified so the MODE
// column can be judged as it actually looks, not a stand-in. Wrapping here,
// the way cmd/demo wraps a purpose-built model of its own, keeps the
// disclaimer a property of the demo binary rather than of the shared,
// real internal/agents package every other caller (cmd/estate included)
// also renders.
type disclaimerModel struct {
	inner agents.Model
}

func (m disclaimerModel) Init() tea.Cmd { return m.inner.Init() }

func (m disclaimerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.inner.Update(msg)
	m.inner = next.(agents.Model)
	return m, cmd
}

func (m disclaimerModel) View() string {
	banner := bannerStyle.Render("  ALL DATA ON THIS SCREEN IS FAKE — MODE-column demo only, no live agent-supervisor daemon behind it")
	return m.inner.View() + "\n" + banner
}

func fixtureFetch() ([]lane.Session, error) {
	return []lane.Session{
		{
			Name: "director",
			Lanes: []lane.Lane{
				// Live, running -- modeFor returns ExecutionLocal (the
				// only positive value it can produce today).
				{Window: 1, Name: "w1", State: "busy", Command: "claude", Model: "sonnet"},
				{Window: 2, Name: "w2", State: "free", Command: "codex", Model: "unknown"},
				// Dead/stale -- modeFor returns nil regardless of
				// Command, matching row.go's own early-return.
				{Window: 3, Name: "w3", State: "dead", Command: "claude", Model: "opus"},
				{Window: 4, Name: "w4", State: "stale", Command: "codex", Model: "unknown"},
				// No Command at all (never attached a harness) --
				// modeFor returns nil for the same reason a dead lane
				// does: no positive evidence to report.
				{Window: 5, Name: "w5", State: "free", Command: "", Model: "unknown"},
			},
		},
	}, nil
}

func main() {
	m := disclaimerModel{inner: agents.New(fixtureFetch)}
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "agentsdemo:", err)
		os.Exit(1)
	}
}
