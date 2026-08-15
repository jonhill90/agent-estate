package board

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/theme"
)

const refreshInterval = 5 * time.Second

// Snapshot is one fetch's worth of already-derived board state.
// cmd/agent-tui's Fetcher composes github.go/ledger.go/card.go into this;
// Model does no I/O of its own, same separation rail.Model keeps from
// cmd/agent-tui (internal/rail/model.go's own doc comment).
type Snapshot struct {
	Cards []Card
	WIP   []WIP
	// Repos is every repo this fetch queried -- not derived from Cards,
	// because a repo with zero open/closed issues right now would
	// otherwise never appear for the project-selection toggle
	// (agent-tui#10 item 2) to offer. Sorted by GitHubID so its picker
	// letters ([a],[b],...) are stable across renders.
	Repos []Repo
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
	layoutIdx     int

	// deselected holds repos toggled OFF by GitHubID, lowercased. Empty
	// means "show every repo" -- the default with zero interaction, and
	// the same shape the current estate already renders, so a lane that
	// never touches a letter key sees exactly what it saw before #10.
	// Toggling is a pure filter over the already-fetched Snapshot (#10
	// item 4: "data fetching must stay off the render path") -- no repo
	// selection ever triggers a new fetch.
	deselected map[string]bool

	snap        Snapshot
	fetchErr    error
	lastFetched time.Time
	quitting    bool

	// theme is agent-tui#27's seam: every colour/border/padding value
	// View() and layout.go's Render use comes from here, never a literal
	// at the call site. Defaults to theme.Default so every pre-#27 caller
	// (every test that builds a Model with New) renders exactly as before
	// this field existed.
	theme theme.Theme
	// themeNotice is #27 acceptance item 3's "says so visibly" half: set
	// only when cmd/agent-tui's theme.Load resolved a malformed config or
	// an unknown theme name, never for a plain missing config (which is
	// not an error). Rendered once, right under the title.
	themeNotice string
}

func New(fetch Fetcher) Model {
	return Model{fetch: fetch, width: 100, height: 30, theme: theme.Default}
}

// WithTheme returns a copy of m with th (and, when non-empty, a visible
// notice about how th was resolved) wired in -- the one call cmd/agent-tui
// makes once theme.Load has run, mirroring rail.Model.WithOps's shape.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
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
		case "0":
			// Show every repo again -- the single key that undoes any
			// combination of letter toggles, so "select 1 or many" (#10
			// item 2) never leaves a lane stuck unable to see something it
			// deselected and forgot about.
			m.deselected = nil
			return m, nil
		}
		if n, ok := digitKey(msg.String()); ok && n >= 1 && n <= len(Layouts) {
			m.layoutIdx = n - 1
			return m, nil
		}
		if i, ok := letterKey(msg.String()); ok && i < len(m.snap.Repos) {
			id := strings.ToLower(m.snap.Repos[i].GitHubID())
			if m.deselected == nil {
				m.deselected = map[string]bool{}
			}
			if m.deselected[id] {
				delete(m.deselected, id)
			} else {
				m.deselected[id] = true
			}
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

// letterKey maps a lowercase-letter key to a zero-based repo index -- 'a'
// is Repos[0], 'b' is Repos[1], and so on, the same discoverable-on-screen
// convention digitKey already sets for layouts (View()'s legend prints
// each repo beside its letter).
func letterKey(s string) (int, bool) {
	if len(s) != 1 || s[0] < 'a' || s[0] > 'z' {
		return 0, false
	}
	return int(s[0] - 'a'), true
}

var titleStyle = lipgloss.NewStyle().Bold(true)

// errStyle/dimStyle are per-render, built from m.theme -- see styles()
// below. There is no package-level var for either any more: a literal
// theme.Role color baked into a package var at init time couldn't change
// when the active theme does, which is exactly the bug agent-tui#27 exists
// to prevent.
type boardStyles struct {
	err, dim lipgloss.Style
}

func (m Model) styles() boardStyles {
	th := m.theme
	return boardStyles{
		err: lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleError)),
		dim: lipgloss.NewStyle().Faint(true),
	}
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 100
	}
	st := m.styles()

	var out string
	out += titleStyle.Render("task board") + "\n"

	if m.themeNotice != "" {
		out += st.err.Render("! "+m.themeNotice) + "\n"
	}

	if m.fetchErr != nil {
		out += st.err.Render("! unavailable") + "\n"
		out += st.dim.Render(m.fetchErr.Error()) + "\n"
	} else if len(m.snap.Cards) == 0 && m.lastFetched.IsZero() {
		out += st.dim.Render("(loading)") + "\n"
	}

	layout := Layouts[m.layoutIdx]
	out += layout.Render(m.visibleCards(), m.snap.WIP, width, m.theme)

	if !m.lastFetched.IsZero() {
		age := time.Since(m.lastFetched).Round(time.Second)
		out += "\n" + st.dim.Render(fmt.Sprintf("fetched %s ago", age)) + "\n"
	}
	out += st.dim.Render(fmt.Sprintf("layout %d/%d: %s -- %s", m.layoutIdx+1, len(Layouts), layout.Name, layout.Description)) + "\n"
	out += st.dim.Render(fmt.Sprintf("[1-%d] switch layout  %s", len(Layouts), m.repoLegend())) + "\n"
	out += st.dim.Render("[r] refresh  [q] quit")
	return out
}

// visibleCards filters m.snap.Cards to repos not in m.deselected -- the
// project-selection half of agent-tui#10 item 2. This is the only place
// selection touches Cards; Layout.Render (layout.go) never sees a
// deselected repo's cards at all, so a Layout can't accidentally show one
// through some path this filter forgot.
func (m Model) visibleCards() []Card {
	if len(m.deselected) == 0 {
		return m.snap.Cards
	}
	out := make([]Card, 0, len(m.snap.Cards))
	for _, c := range m.snap.Cards {
		if !m.deselected[strings.ToLower(c.Repo.GitHubID())] {
			out = append(out, c)
		}
	}
	return out
}

// repoLegend prints every fetched repo beside the letter key that toggles
// it, selected ones marked "*" -- the same "discoverable on screen"
// requirement digitKey's own legend already meets (#10: "cycle keys must
// be discoverable on screen, as [1-4] already is"). [0] always shows too,
// so the reset key is never something a lane has to remember.
func (m Model) repoLegend() string {
	if len(m.snap.Repos) == 0 {
		return "[0] all repos"
	}
	parts := make([]string, 0, len(m.snap.Repos)+1)
	for i, r := range m.snap.Repos {
		if i >= 26 {
			break // out of letters; unreachable at this estate's repo count, not worth a second key scheme
		}
		mark := "*"
		if m.deselected[strings.ToLower(r.GitHubID())] {
			mark = " "
		}
		label := r.Label
		if label == "" {
			label = r.Name
		}
		parts = append(parts, fmt.Sprintf("[%c]%s%s", 'a'+i, mark, label))
	}
	parts = append(parts, "[0]all")
	return strings.Join(parts, " ")
}

// Aged/blocked colouring is no longer a post-processing pass over plain
// text. #6 through #9 needed that shape (colorizeAged/isAgedCardLine) only
// because RenderByColumn/RenderByRepo returned plain strings with a bare
// "!" marker that model.go alone knew how to recolour, and the indent
// depth varied by which Render function wrote a line. #10 retired both of
// those functions (view.go's doc comment) in favour of layout.go's
// Layouts, whose renderCard bakes cardWarnColor directly into a card's own
// lipgloss.Style at construction time -- there is no marker text left to
// scan for, in any Layout, at any indent -- see layout_test.go's
// TestLayoutRenderColorsAgedCard, which now covers what
// TestModelViewColorsAgedCard used to.
