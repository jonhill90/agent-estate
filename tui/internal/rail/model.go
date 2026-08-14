// Package rail is the left-anchored navigation rail: a Bubble Tea model that
// renders lane state as moving glyphs in a narrow column. It is a LAYOUT
// REGION, not a screen -- agent-supervisor#107 is explicit that the rail is
// meant to sit in a narrow tmux pane (~24-32 columns) beside a terminal
// pane, not fill the window as a full-screen list. This package does not
// create that split; it only renders correctly inside whatever width it is
// given, narrow or wide. Splitting panes is a tmux operation a human or the
// Director performs, per the "own window, never inject panes into live
// ones" constraint -- this program never calls tmux itself.
package rail

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/lane"
)

// RailWidth is the target column count for the rail region. Jon asked for
// "roughly 24-32 columns" (issue #107); 28 sits in the middle of that band.
const RailWidth = 28

const tickInterval = 120 * time.Millisecond
const refreshInterval = 2 * time.Second

// Fetcher retrieves the current lane list. cmd/agent-tui supplies the real
// implementation (an MCP tools/call("lanes")); this package knows nothing
// about MCP, tmux, or lanes.sh -- only that it can ask for []lane.Lane and
// might get an error back, which it must show, never swallow into a blank
// rail (issue #107 hard-acceptance item 3, applied to the fetch path too:
// an instrument that cannot see must not look like a healthy empty estate).
type Fetcher func() ([]lane.Lane, error)

type tickMsg time.Time
type refreshMsg time.Time

type fetchResultMsg struct {
	lanes []lane.Lane
	err   error
}

// Model is the rail's Bubble Tea model.
type Model struct {
	fetch Fetcher

	width, height int
	tick          int

	lanes    []lane.Lane
	selected int

	// glyphSet is the live picker agent-supervisor#107's addendum asks for:
	// "a key steps through numbered options; he presses a number; done in
	// two seconds." It indexes lane.Variants directly -- number key N
	// selects Variants[N-1] against the SAME real lane data already on
	// screen, never a mock (addendum rule 1). Starts at 0, lane.Default's
	// index, so silence still yields something sane (addendum rule 3).
	glyphSet int

	fetchErr    error
	lastFetched time.Time
	quitting    bool
}

// New builds a Model bound to the given fetch function.
func New(fetch Fetcher) Model {
	return Model{fetch: fetch, width: RailWidth, height: 24}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), refreshCmd(), doFetch(m.fetch))
}

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func doFetch(fetch Fetcher) tea.Cmd {
	return func() tea.Msg {
		lanes, err := fetch()
		return fetchResultMsg{lanes: lanes, err: err}
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
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
			return m, nil
		case "down", "j":
			if m.selected < len(m.lanes)-1 {
				m.selected++
			}
			return m, nil
		case "r":
			return m, doFetch(m.fetch)
		}
		// Number keys select a glyph set directly, live, against whatever
		// is already on screen -- the whole picker is this one branch plus
		// the legend line in View(). "1" through "9" cover up to nine
		// variants without needing a mode to enter or leave first.
		if n, ok := digitKey(msg.String()); ok && n >= 1 && n <= len(lane.Variants) {
			m.glyphSet = n - 1
		}
		return m, nil

	case tickMsg:
		m.tick++
		return m, tickCmd()

	case refreshMsg:
		return m, tea.Batch(refreshCmd(), doFetch(m.fetch))

	case fetchResultMsg:
		m.fetchErr = msg.err
		if msg.err == nil {
			m.lanes = msg.lanes
			m.lastFetched = time.Now()
			if m.selected >= len(m.lanes) {
				m.selected = max(0, len(m.lanes)-1)
			}
		}
		return m, nil
	}
	return m, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// digitKey reports whether s is a single ASCII digit key and, if so, its
// value. tea.KeyMsg.String() renders a bare digit key as itself ("1", "2",
// ...), so no key-code table is needed.
func digitKey(s string) (int, bool) {
	if len(s) != 1 || s[0] < '1' || s[0] > '9' {
		return 0, false
	}
	return int(s[0] - '0'), true
}

// selectionBackground is the ONE colour a selected row ever gets. It is
// fixed regardless of the selected lane's state -- as#109 diagnosed the old
// `Reverse(true)` selection as wonky precisely because reverse video inverts
// whatever foreground colour the row already carries, so a selected `busy`
// row highlighted blue and a selected `free` row highlighted green. An
// explicit, constant background is the fix; never go back to Reverse.
const selectionBackground = lipgloss.Color("#33475b")

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	rowStyle    = lipgloss.NewStyle().Padding(0, 1)
	selRowStyle = rowStyle.Background(selectionBackground)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	errStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff5555"))
	legendStyle = lipgloss.NewStyle().Faint(true)
	borderStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(lipgloss.Color("#444444"))
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	width := m.width
	if width <= 0 || width > RailWidth+8 {
		width = RailWidth
	}
	innerWidth := width - 2 // padding

	set := lane.Variants[m.glyphSet]

	var b []string
	b = append(b, titleStyle.Width(innerWidth).Render("lanes"))

	// A fetch failure is the load-bearing case (mirrors
	// supervisor_view.SupervisorUnavailable): show it, never render a blank
	// or stale-looking rail as if the estate were quietly idle.
	if m.fetchErr != nil {
		b = append(b, errStyle.Width(innerWidth).Render("! unavailable"))
		b = append(b, dimStyle.Width(innerWidth).Render(truncate(m.fetchErr.Error(), innerWidth)))
	} else if len(m.lanes) == 0 {
		b = append(b, dimStyle.Width(innerWidth).Render("(no lanes)"))
	}

	// name budget: innerWidth minus the row's own Padding(0,1) (2 cols)
	// minus the glyph column and the single space after it (2 cols). Both
	// must be subtracted -- reserving only for the glyph+space and not for
	// the row's own padding let a name run one cell past the available
	// width and wrap onto a second line with no glyph or indent (as#109's
	// second defect).
	nameWidth := innerWidth - 4

	for i, l := range m.lanes {
		style := lane.StyleFor(set, l.State)
		glyph := lane.Frame(set, l.State, m.tick)
		name := truncate(l.Name, nameWidth)

		var line string
		if i == m.selected {
			// Glyph and the rest of the row are rendered as two separately
			// closed ANSI spans. A single Render() over a string that
			// already contains the glyph's own embedded reset code would
			// have that inner reset wipe the outer selection background
			// for everything after it -- measured: the trailing " name"
			// text came out with no background at all. Giving " "+name
			// its own Background()-carrying span keeps the highlight
			// unbroken across the whole row.
			g := lipgloss.NewStyle().Foreground(lipgloss.Color(style.Color)).Background(selectionBackground).Render(glyph)
			rest := lipgloss.NewStyle().Background(selectionBackground).Render(" " + name)
			line = g + rest
			b = append(b, selRowStyle.Width(innerWidth).Render(line))
		} else {
			g := lipgloss.NewStyle().Foreground(lipgloss.Color(style.Color)).Render(glyph)
			line = fmt.Sprintf("%s %s", g, name)
			b = append(b, rowStyle.Width(innerWidth).Render(line))
		}
	}

	// The legend: every state must be nameable (issue #107 hard-acceptance
	// item 3). This line always shows the SELECTED lane's real state word,
	// verbatim, so a glyph is never the only source of truth on screen --
	// the same discipline laneview/text.sh applies by printing the state
	// name beside its glyph on every row.
	b = append(b, dimStyle.Width(innerWidth).Render(""))
	if len(m.lanes) > 0 && m.selected < len(m.lanes) {
		sel := m.lanes[m.selected]
		style := lane.StyleFor(set, sel.State)
		label := style.Label
		if label == "" {
			label = sel.State // Unmapped: still print the raw word, never blank
		}
		b = append(b, legendStyle.Width(innerWidth).Render(fmt.Sprintf("state: %s", label)))
		b = append(b, legendStyle.Width(innerWidth).Render(fmt.Sprintf("idle:  %ds", sel.IdleSeconds)))
	}
	if !m.lastFetched.IsZero() {
		age := time.Since(m.lastFetched).Round(time.Second)
		b = append(b, legendStyle.Width(innerWidth).Render(fmt.Sprintf("age:   %s", age)))
	}

	// The picker itself: which glyph set is live, and how to change it.
	// Every option is real and numbered here, not described in prose
	// elsewhere -- pressing the number is the whole interaction.
	b = append(b, dimStyle.Width(innerWidth).Render(""))
	b = append(b, legendStyle.Width(innerWidth).Render(fmt.Sprintf("glyphs %d/%d: %s", m.glyphSet+1, len(lane.Variants), set.Name)))
	b = append(b, legendStyle.Width(innerWidth).Render(truncate(set.Description, innerWidth)))
	b = append(b, legendStyle.Width(innerWidth).Render(fmt.Sprintf("[1-%d] to switch", len(lane.Variants))))

	content := lipgloss.JoinVertical(lipgloss.Left, b...)
	return borderStyle.Height(m.height - 1).Render(content)
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
