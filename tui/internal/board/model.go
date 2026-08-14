package board

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const refreshInterval = 5 * time.Second

// Snapshot is one fetch's worth of already-derived board state.
// cmd/agent-tui's Fetcher composes github.go/ledger.go/card.go into this;
// Model does no I/O of its own, same separation rail.Model keeps from
// cmd/agent-tui (internal/rail/model.go's own doc comment).
type Snapshot struct {
	Cards []Card
	WIP   []WIP
}

// Fetcher retrieves the current board snapshot. Errors are surfaced, never
// swallowed into an empty-looking board -- the same discipline
// internal/rail.Fetcher documents for the identical reason: an instrument
// that cannot see must not look like a healthy empty estate.
type Fetcher func() (Snapshot, error)

type refreshMsg time.Time
type fetchResultMsg struct {
	snap Snapshot
	err  error
}

// Model is the board's Bubble Tea program -- a separate screen from
// internal/rail.Model, not a mode grafted onto it (agent-tui#6: "do not
// restyle the rail"). cmd/agent-tui runs one or the other.
type Model struct {
	fetch Fetcher

	width, height int
	viewIdx       int

	snap        Snapshot
	fetchErr    error
	lastFetched time.Time
	quitting    bool
}

func New(fetch Fetcher) Model {
	return Model{fetch: fetch, width: 100, height: 30}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(refreshCmd(), doFetch(m.fetch))
}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func doFetch(fetch Fetcher) tea.Cmd {
	return func() tea.Msg {
		snap, err := fetch()
		return fetchResultMsg{snap: snap, err: err}
	}
}

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
		case "r":
			return m, doFetch(m.fetch)
		}
		if n, ok := digitKey(msg.String()); ok && n >= 1 && n <= len(Views) {
			m.viewIdx = n - 1
		}
		return m, nil

	case refreshMsg:
		return m, tea.Batch(refreshCmd(), doFetch(m.fetch))

	case fetchResultMsg:
		m.fetchErr = msg.err
		if msg.err == nil {
			m.snap = msg.snap
			m.lastFetched = time.Now()
		}
		return m, nil
	}
	return m, nil
}

func digitKey(s string) (int, bool) {
	if len(s) != 1 || s[0] < '1' || s[0] > '9' {
		return 0, false
	}
	return int(s[0] - '0'), true
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	errStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff5555"))
	dimStyle   = lipgloss.NewStyle().Faint(true)
	warnStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f1c40f"))
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 100
	}

	var out string
	out += titleStyle.Render("task board") + "\n"

	if m.fetchErr != nil {
		out += errStyle.Render("! unavailable") + "\n"
		out += dimStyle.Render(m.fetchErr.Error()) + "\n"
	} else if len(m.snap.Cards) == 0 && m.lastFetched.IsZero() {
		out += dimStyle.Render("(loading)") + "\n"
	}

	view := Views[m.viewIdx]
	body := view.Render(m.snap.Cards, m.snap.WIP, width)
	// Highlight the "!" aged-card marker this package's own cardLine writes
	// (view.go) -- done here, not in view.go, so the plain-text Render
	// functions stay usable outside a styled terminal (a --json dump, a
	// test) while the interactive Model still gets color.
	out += colorizeAged(body)

	if !m.lastFetched.IsZero() {
		age := time.Since(m.lastFetched).Round(time.Second)
		out += "\n" + dimStyle.Render(fmt.Sprintf("fetched %s ago", age)) + "\n"
	}
	out += dimStyle.Render(fmt.Sprintf("view %d/%d: %s -- %s", m.viewIdx+1, len(Views), view.Name, view.Description)) + "\n"
	out += dimStyle.Render(fmt.Sprintf("[1-%d] switch view  [r] refresh  [q] quit", len(Views)))
	return out
}

func colorizeAged(body string) string {
	var out string
	for _, line := range splitLinesKeepEmpty(body) {
		if isAgedCardLine(line) {
			out += warnStyle.Render(line) + "\n"
		} else {
			out += line + "\n"
		}
	}
	if len(out) > 0 {
		out = out[:len(out)-1] // drop the trailing newline this loop always adds
	}
	return out
}

// isAgedCardLine reports whether line is one of cardLine's own aged-marker
// lines. cardLine (view.go) puts the "!" marker at column 0 of the string
// it returns, but RenderByColumn/RenderByRepo indent every card line before
// writing it ("  " + cardLine(...), "    " + cardLine(...)) -- so in real
// board output the marker never actually sits at column 0, and a bare
// line[0] == '!' check (as this used to be) can never fire. Skipping past
// cardLine's own leading indentation, whatever depth it is, is what makes
// this match the same lines cardLine marks, regardless of which Render
// function -- or a future one at a different indent -- wrote them.
func isAgedCardLine(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	return len(trimmed) > 0 && trimmed[0] == '!'
}

func splitLinesKeepEmpty(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
