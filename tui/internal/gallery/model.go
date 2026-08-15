package gallery

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model is the gallery's Bubble Tea program -- a separate screen from
// internal/rail.Model, internal/board.Model and internal/cost.Model
// (agent-tui#11's scope: stay in the rail/glyph area, do not restyle the
// rail itself). cmd/agent-tui runs exactly one of these at a time. Unlike
// rail and board, the gallery reads no lanes and needs no supervisor
// connection at all -- its entire content is BuildRows() over data already
// compiled into the binary (internal/lane's Variants and Candidates), so
// New takes no Fetcher.
type Model struct {
	rows   []Row
	offset int // index into rows; the first state currently shown

	width, height int
	quitting      bool
}

// New builds a Model with a fresh BuildRows() snapshot. Called once at
// startup -- the gallery's content is static per process (lane.Variants and
// lane.Candidates are package-level data, not something that changes while
// the program runs), so there is no refresh/fetch cycle to wire up here,
// unlike rail or cost.
func New() Model {
	return Model{rows: BuildRows(), width: 100, height: 30}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.offset > 0 {
				m.offset--
			}
			return m, nil
		case "down", "j":
			if m.offset < len(m.rows)-1 {
				m.offset++
			}
			return m, nil
		case "g", "home":
			m.offset = 0
			return m, nil
		case "G", "end":
			if len(m.rows) > 0 {
				m.offset = len(m.rows) - 1
			}
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	stateStyle  = lipgloss.NewStyle().Bold(true)
	flagStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#e0c34c"))
	legendStyle = lipgloss.NewStyle().Faint(true)
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 30
	}

	var out string
	out += titleStyle.Render("glyph gallery") + "\n"
	out += dimStyle.Render(fmt.Sprintf("state %d/%d -- [j/k] scroll one state, [g/G] top/bottom, [q] quit", m.offset+1, len(m.rows))) + "\n\n"

	// Budget: title(1) + subtitle(1) + blank(1) + legend(len) + blank(1) +
	// footer(1) reserved; whatever remains goes to Render's row content.
	legend := Legend()
	reserved := 5 + len(legend)
	bodyLines := height - reserved
	if bodyLines < 3 {
		bodyLines = 3 // always show at least the current state's own cells
	}

	for _, line := range Render(m.rows, m.offset, bodyLines, width) {
		out += colorizeFlags(line) + "\n"
	}

	out += "\n"
	for _, l := range legend {
		out += legendStyle.Render(l) + "\n"
	}

	return out
}

// colorizeFlags highlights a [NF]/[emoji] tag and bolds a "state:" line,
// done here rather than in view.go for the same reason internal/cost/model.go's
// colorizeWarn is split from its Render functions: the plain-text Render
// output stays usable in a fixture or a test with no styled terminal, while
// the interactive Model still gets colour and never renders a state name
// as anything less than bold.
func colorizeFlags(line string) string {
	if len(line) >= 6 && line[:6] == "state:" {
		return stateStyle.Render(line)
	}
	if containsAny(line, "[NF]", "[emoji]") {
		return flagStyle.Render(line)
	}
	return line
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
