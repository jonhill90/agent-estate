package board

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/theme"
)

// DefaultRefreshInterval is how often the board re-fetches on its own tick,
// with no flag or override supplied. agent-tui#28 measured the previous
// value (5s) at ~138 GraphQL points/min against gh issue list/gh pr list --
// ~8,160/hr against a 5,000/hr shared budget, enough to starve
// agent-supervisor's own dispatch (agent-supervisor#144). 60s is not a
// magic number chosen to hit some target ratio: it is what #28's own text
// calls "honest for issue state" -- this is a screen a human reads, not a
// control loop, and nothing on it needs sub-minute freshness. See
// cmd/agent-tui's -board-refresh flag, which is how the value can be
// argued without a rebuild (#28 acceptance item 2).
const DefaultRefreshInterval = 60 * time.Second

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

	// refreshInterval is how often Init/the refreshMsg loop below re-ticks.
	// Set once, at construction (New/NewWithRefreshInterval), never mutated
	// after -- there is no key that changes it, on purpose: #28's fix is a
	// deploy-time flag (cmd/agent-tui's -board-refresh), not a runtime
	// control, so the same "argued without a rebuild" flag also can't be
	// fat-fingered from the keyboard mid-session back down toward 5s.
	refreshInterval time.Duration

	width, height int
	layoutIdx     int

	// scrollOffset is how many lines of the rendered board (post-header,
	// pre-footer -- see View) are scrolled past. agent-tui#29: the board
	// had no viewport at all, so content taller than the pane was only
	// ever reachable by whatever the terminal itself did with overflow --
	// which, per #29's own report, was "show the bottom, headers scrolled
	// off, no indication anything is hidden." View clamps this to
	// [0, maxScrollOffset] on every render, so it is safe for Update to
	// move it by any amount, including past either end.
	scrollOffset int

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
	return NewWithRefreshInterval(fetch, DefaultRefreshInterval)
}

// WithTheme returns a copy of m with th (and, when non-empty, a visible
// notice about how th was resolved) wired in -- the one call cmd/agent-tui
// makes once theme.Load has run, mirroring rail.Model.WithOps's shape.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

// NewWithRefreshInterval is New, with the tick interval as an explicit
// argument -- cmd/agent-tui's -board-refresh flag (and $AGENT_TUI_BOARD_REFRESH)
// call this directly so the value is settable without a rebuild (#28
// acceptance item 2); New itself keeps DefaultRefreshInterval so every
// existing caller and test that never touches the flag keeps working
// unchanged.
func NewWithRefreshInterval(fetch Fetcher, interval time.Duration) Model {
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	return Model{fetch: fetch, width: 100, height: 30, refreshInterval: interval, theme: theme.Default}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(refreshCmd(m.refreshInterval), doFetch(m.fetch))
}

func refreshCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg { return refreshMsg(t) })
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
		// agent-tui#29: up/down/j/k/pgup/pgdown move the viewport. The
		// actual bound is enforced in View (it needs the rendered line
		// count, which Update doesn't have), so these are free to push
		// scrollOffset negative or past the end -- View's clamp is the only
		// place that has to be right, and every caller of it (including
		// these) can stay simple.
		case "up", "k":
			m.scrollOffset--
			return m, nil
		case "down", "j":
			m.scrollOffset++
			return m, nil
		case "pgup":
			m.scrollOffset -= pageSize(m.height)
			return m, nil
		case "pgdown", "pgdn":
			m.scrollOffset += pageSize(m.height)
			return m, nil
		case "t":
			// agent-tui#25 scope item 3: runtime theme comparison, same
			// shape as rail.Model's "t" case -- see its comment for why
			// this never touches theme.Save.
			m.theme = theme.Cycle(m.theme)
			return m, nil
		}
		if n, ok := digitKey(msg.String()); ok && n >= 1 && n <= len(Layouts) {
			m.layoutIdx = n - 1
			m.scrollOffset = 0 // a new layout is different content; don't carry a stale scroll position into it
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
			m.scrollOffset = 0 // same reasoning as the layout switch above: the repo filter just changed what's on screen
		}
		return m, nil

	case refreshMsg:
		return m, tea.Batch(refreshCmd(m.refreshInterval), doFetch(m.fetch))

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

// errStyle/dimStyle/scrollIndicator are per-render, built from m.theme --
// see styles() below. There is no package-level var for any of them any
// more: a literal theme.Role color baked into a package var at init time
// couldn't change when the active theme does, which is exactly the bug
// agent-tui#27 exists to prevent.
type boardStyles struct {
	err, dim, scrollIndicator lipgloss.Style
}

func (m Model) styles() boardStyles {
	th := m.theme
	return boardStyles{
		err: lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleError)),
		dim: lipgloss.NewStyle().Faint(true),
		// scrollIndicator is agent-tui#29's "whatever is off-screen must be
		// visibly indicated" element, given its own named field (rather
		// than reusing dim inline, or a literal colour) specifically so
		// agent-tui#27's theme table can restyle just this element without
		// a second pass through this file. Currently identical to dim;
		// #27 owns making it look like anything more than that.
		scrollIndicator: lipgloss.NewStyle().Faint(true),
	}
}

// pageSize is how many lines up/pgup/down/pgdown move for one page --
// height minus a little headroom so a page-down always leaves the last
// line of the previous page visible as an anchor, the same reason
// terminal pagers (less, more) don't jump a full screen at a time. height
// <= 0 (should not happen once WindowSizeMsg has landed once, but Update
// doesn't get to assume that) falls back to a fixed page so pgup/pgdown are
// never a no-op before the first resize arrives.
func pageSize(height int) int {
	if height <= 1 {
		return 10
	}
	if height <= 3 {
		return height
	}
	return height - 3
}

// scrollBody splits body into lines and returns the window [offset,
// offset+viewportHeight) of it, clamped so offset can never show blank
// space past either end -- the one place agent-tui#29's viewport math
// lives, so Update's key handlers (which don't know the rendered line
// count) can move scrollOffset by any amount without risking a panic or an
// empty render here. Returns the visible lines, the clamped offset actually
// used, and the total line count -- the caller needs all three to build the
// "N more below/above" indicator.
func scrollBody(body string, offset, viewportHeight int) (visible []string, usedOffset, total int) {
	lines := strings.Split(body, "\n")
	total = len(lines)
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	maxOffset := total - viewportHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	end := offset + viewportHeight
	if end > total {
		end = total
	}
	return lines[offset:end], offset, total
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

	var header strings.Builder
	header.WriteString(titleStyle.Render("task board") + "\n")
	if m.themeNotice != "" {
		header.WriteString(st.err.Render("! "+m.themeNotice) + "\n")
	}
	if m.fetchErr != nil {
		header.WriteString(st.err.Render("! unavailable") + "\n")
		header.WriteString(st.dim.Render(m.fetchErr.Error()) + "\n")
	} else if len(m.snap.Cards) == 0 && m.lastFetched.IsZero() {
		header.WriteString(st.dim.Render("(loading)") + "\n")
	}

	layout := Layouts[m.layoutIdx]
	body := strings.TrimRight(layout.Render(m.visibleCards(), m.snap.WIP, width, m.theme), "\n")

	var footer strings.Builder
	if !m.lastFetched.IsZero() {
		age := time.Since(m.lastFetched).Round(time.Second)
		footer.WriteString(st.dim.Render(fmt.Sprintf("fetched %s ago", age)) + "\n")
	}
	footer.WriteString(st.dim.Render(fmt.Sprintf("layout %d/%d: %s -- %s", m.layoutIdx+1, len(Layouts), layout.Name, layout.Description)) + "\n")
	footer.WriteString(st.dim.Render(fmt.Sprintf("[1-%d] switch layout  %s", len(Layouts), m.repoLegend())) + "\n")
	footer.WriteString(st.dim.Render(fmt.Sprintf("theme: %s  [t] switch (this session only)", m.theme.Name)) + "\n")
	footer.WriteString(st.dim.Render("[j/k up/down  pgup/pgdn] scroll  [r] refresh  [q] quit"))

	// agent-tui#29: the viewport is only the body -- header and footer
	// always render in full, every frame, so the column-header row inside
	// body is the ONLY thing that can ever scroll out of view (never the
	// title, error state, or the key legend a lane needs to discover
	// scrolling exists in the first place).
	headerLines := strings.Count(header.String(), "\n")
	footerLines := strings.Count(footer.String(), "\n") + 1
	viewportHeight := m.height - headerLines - footerLines
	if viewportHeight < 1 {
		viewportHeight = 1
	}

	visible, offset, total := scrollBody(body, m.scrollOffset, viewportHeight)

	var out strings.Builder
	out.WriteString(header.String())
	out.WriteString(strings.Join(visible, "\n"))
	out.WriteString("\n")
	if hiddenBelow := total - offset - len(visible); total > viewportHeight {
		hiddenAbove := offset
		out.WriteString(st.scrollIndicator.Render(fmt.Sprintf(
			"-- showing lines %d-%d of %d (%d more above, %d more below) --",
			offset+1, offset+len(visible), total, hiddenAbove, hiddenBelow,
		)) + "\n")
	}
	out.WriteString(footer.String())
	return out.String()
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
