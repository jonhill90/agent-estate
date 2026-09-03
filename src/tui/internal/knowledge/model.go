package knowledge

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/src/tui/internal/memgraph"
	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

// refreshInterval matches internal/skills/internal/mcpservers' identical
// reasoning: agent/index.md changes by hand (a fact gets added/updated),
// not by the second -- a slow poll plus [r] for "I just wrote one" is the
// same shape those two packages' own refreshInterval doc comments already
// justify.
const refreshInterval = 30 * time.Second

type refreshMsg time.Time
type fetchResultMsg struct {
	entries []IndexEntry
	err     error
}
type factResultMsg struct {
	slug string
	fact Fact
	err  error
}

// mode is which of the two panes this Model currently shows -- the list
// (every IndexEntry) or one fact's own opened body.
type mode int

const (
	modeList mode = iota
	modeReading
	// modeGraph is the whole vault as a draggable force-directed graph
	// (internal/memgraph) -- reached from modeList with [g], back to
	// modeList with [esc]/[left], the same shape modeReading already
	// uses for its own two-way transition.
	modeGraph
)

// sortMode is the list's own sort key -- both options are computable from
// IndexEntry alone (index order, the order agent/index.md itself lists
// facts in, and alphabetical by title), never Type or Created: those two
// are unknown for a row that has never been opened (this package's own
// progressive-disclosure constraint), so sorting by either would silently
// reorder around missing data for most rows most of the time.
type sortMode int

const (
	sortIndexOrder sortMode = iota
	sortAlpha
)

// Row is one list line -- IndexEntry's own three fields plus Type/Created,
// filled in ONLY once this row's fact has actually been opened (Model's
// own cache) -- nil until then, absence as a typed value (this package's
// own doc comment on why).
type Row struct {
	Slug        string
	Title       string
	Description string
	Type        *string
	Created     *string
}

// Model is the knowledge view: a list of every fact in $AGENT_MEMORY_VAULT
// (via Fetcher, agent/index.md only) and, once one is opened, that one
// fact's full body (via FactLoader, agent/facts/<slug>.md -- read ONLY at
// that point, never before). Read-only: this package has no write path at
// all, matching the requirement directly -- there is no Update case, no
// key, no method anywhere in this package that opens a vault file for
// anything but os.ReadFile. Both the list and an opened fact's body are
// backed by bubbles/viewport (internal/chat's own listVP/transcriptVP
// pattern) rather than a plain string clipped to a height -- agent-tui#29
// was a board pane that silently dropped rows past its own height, and
// this view's real vault already has 64 facts, comfortably more than any
// realistic terminal shows at once. Not wired into internal/shell yet --
// matches every other view this repo has built standalone-first
// (internal/stub, internal/agents, internal/skills, internal/mcpservers,
// internal/connectors, internal/admin).
type Model struct {
	fetch    Fetcher
	loadFact FactLoader

	entries  []IndexEntry
	fetchErr error

	// cache holds every fact this Model has actually opened THIS session
	// -- the one place a fact's body (and its Type/Created) live once
	// read; never populated by anything but a real open.
	cache map[string]Fact

	sort     sortMode
	selected int

	mode mode
	// opening is the slug a factResultMsg is still in flight for -- so a
	// second [enter] on the same row while it is loading doesn't fire a
	// second read, and so the reading pane can say "loading" rather than
	// show stale content from whatever was open before.
	opening string
	// readErr is a failed LoadFact for the fact currently (attempted to
	// be) open -- a slug in agent/index.md with no corresponding file is
	// a real, visible error, never a silent fall-back to the list.
	readErr string

	// listVP is the scrollable list (SPEC: "sortable, scrollable"); bodyVP
	// is the scrollable reading pane once a fact is open. Content and size
	// are kept in sync (sync below) on every event that could invalidate
	// either, never recomputed inline inside View -- View has a value
	// receiver and cannot persist a scroll position a user has moved away
	// from.
	listVP viewport.Model
	bodyVP viewport.Model

	// graph is the memory-graph pane (modeGraph) -- agent-tui's own
	// "make the knowledge graph a real, draggable pane inside the app"
	// issue. Its zero value (New's own default, no fetch wired) renders
	// an honest "not configured" rather than a demo graph; WithGraph
	// below is what wires a real Fetcher in, same shape WithTheme uses
	// for theme.Theme.
	graph memgraph.Model

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model with fetch/loadFact wired in.
func New(fetch Fetcher, loadFact FactLoader) Model {
	return Model{
		fetch:    fetch,
		loadFact: loadFact,
		cache:    map[string]Fact{},
		listVP:   viewport.New(0, 0),
		bodyVP:   viewport.New(0, 0),
		graph:    memgraph.New(nil),
		width:    100,
		height:   30,
		theme:    theme.Default,
	}
}

// WithGraph returns a copy of m with its memory-graph pane (modeGraph)
// wired to fetch -- cmd/estate's own buildMemgraphFetch, in the real
// binary. Left unset (New's own zero-value memgraph.Model), [g] still
// works but the graph pane renders its own honest "not configured" state
// rather than a demo graph.
func (m Model) WithGraph(fetch memgraph.Fetcher) Model {
	m.graph = memgraph.New(fetch)
	return m
}

// WithTheme returns a copy of m painted with th -- the same per-pane seam
// every other package in this repo exposes.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	m.graph = m.graph.WithTheme(th)
	return m.sync()
}

// Rows returns the current list, in the current sort order, with Type/
// Created filled in from m.cache wherever a row has actually been opened
// -- exported so a caller (a future shell wiring, or this package's own
// teatest) can assert on it without depending on View's rendered string.
func (m Model) Rows() []Row {
	rows := make([]Row, len(m.entries))
	for i, e := range m.entries {
		r := Row{Slug: e.Slug, Title: e.Title, Description: e.Description}
		if f, ok := m.cache[e.Slug]; ok {
			t, c := f.Type, f.Created
			r.Type = &t
			r.Created = &c
		}
		rows[i] = r
	}
	switch m.sort {
	case sortAlpha:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Title < rows[j].Title })
	}
	return rows
}

func (m Model) Init() tea.Cmd {
	// m.graph.Init fires its own fetch here too -- the same "keep every
	// pane's own state current in the background, whether it is the one
	// on screen or not" property every OTHER pane in this module already
	// has (internal/shell.routeAll's own doc comment), so [g] shows a
	// graph that has usually already loaded rather than a fresh
	// "loading" frame every time.
	return tea.Batch(refreshCmd(), doFetch(m.fetch), m.graph.Init())
}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func doFetch(fetch Fetcher) tea.Cmd {
	if fetch == nil {
		return nil
	}
	return func() tea.Msg {
		entries, err := fetch()
		return fetchResultMsg{entries: entries, err: err}
	}
}

func doLoadFact(loadFact FactLoader, slug string) tea.Cmd {
	if loadFact == nil {
		return nil
	}
	return func() tea.Msg {
		fact, err := loadFact(slug)
		return factResultMsg{slug: slug, fact: fact, err: err}
	}
}

// Update forwards every message to the memory-graph sub-model FIRST,
// unconditionally -- internal/memgraph.Model's own fetch-result and mouse
// messages are types this package's switch below never names, so without
// this they would silently vanish. This is the exact "background refresh
// even while not the active mode" property internal/shell.routeAll
// already gives every top-level pane; modeGraph is a pane-within-a-pane
// and needs the same property for the same reason -- the graph should
// already be loaded, and a drag started before quite reaching modeGraph
// display should never be dropped mid-gesture.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, graphCmd := m.graph.Update(msg)
	m.graph = next.(memgraph.Model)

	nm, cmd := m.updateSelf(msg)
	m = nm.(Model)
	return m, tea.Batch(cmd, graphCmd)
}

func (m Model) updateSelf(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.sync(), nil

	case refreshMsg:
		return m, tea.Batch(refreshCmd(), doFetch(m.fetch))

	case fetchResultMsg:
		m.fetchErr = msg.err
		if msg.err == nil {
			m.entries = msg.entries
			if m.selected >= len(m.entries) {
				m.selected = len(m.entries) - 1
			}
			if m.selected < 0 {
				m.selected = 0
			}
		}
		return m.sync(), nil

	case factResultMsg:
		if msg.slug != m.opening {
			// A refresh or a second open superseded this one; drop it --
			// never let a stale load overwrite what is actually on screen.
			return m, nil
		}
		m.opening = ""
		if msg.err != nil {
			m.readErr = msg.err.Error()
			return m.sync(), nil
		}
		m.readErr = ""
		m.cache[msg.slug] = msg.fact
		return m.sync(), nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "t":
		return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
	}

	if m.mode == modeGraph {
		switch msg.String() {
		case "esc", "left":
			m.mode = modeList
			return m.sync(), nil
		}
		// Every other key (mouse-drag reload "r", or nothing) is already
		// handled by Update's own unconditional forward into m.graph,
		// above -- handleKey only owns the mode transition back out.
		return m, nil
	}

	if m.mode == modeReading {
		switch msg.String() {
		case "esc", "left":
			m.mode = modeList
			return m.sync(), nil
		case "r":
			// Reload the ONE fact currently open -- not the index, and
			// not every other fact this session has already read. Still
			// a single-file read, consistent with this package's own
			// progressive-disclosure constraint.
			rows := m.Rows()
			if m.selected < 0 || m.selected >= len(rows) {
				return m, nil
			}
			slug := rows[m.selected].Slug
			m.opening = slug
			m.readErr = ""
			return m, doLoadFact(m.loadFact, slug)
		case "pgdown", "ctrl+d":
			m.bodyVP.HalfPageDown()
			return m, nil
		case "pgup", "ctrl+u":
			m.bodyVP.HalfPageUp()
			return m, nil
		case "down", "j":
			m.bodyVP.LineDown(1)
			return m, nil
		case "up", "k":
			m.bodyVP.LineUp(1)
			return m, nil
		case "home", "g":
			m.bodyVP.GotoTop()
			return m, nil
		case "end", "G":
			m.bodyVP.GotoBottom()
			return m, nil
		}
		return m, nil
	}

	switch msg.String() {
	case "r":
		return m, doFetch(m.fetch)
	case "g":
		m.mode = modeGraph
		return m.sync(), nil
	case "s":
		m.sort = (m.sort + 1) % 2
		return m.sync(), nil
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
		return m.sync(), nil
	case "down", "j":
		rows := m.Rows()
		if m.selected < len(rows)-1 {
			m.selected++
		}
		return m.sync(), nil
	case "pgdown", "ctrl+d":
		m.listVP.HalfPageDown()
		return m, nil
	case "pgup", "ctrl+u":
		m.listVP.HalfPageUp()
		return m, nil
	case "enter", "right":
		rows := m.Rows()
		if m.selected < 0 || m.selected >= len(rows) {
			return m, nil
		}
		slug := rows[m.selected].Slug
		m.mode = modeReading
		m.readErr = ""
		if _, ok := m.cache[slug]; ok {
			// Already opened this session -- no re-read, per this
			// package's own progressive-disclosure constraint (open once,
			// not once per view).
			return m.sync(), nil
		}
		m.opening = slug
		return m.sync(), doLoadFact(m.loadFact, slug)
	}
	return m, nil
}

// metrics is the width/height split both sync and View need to agree on --
// computed once so a resize, a mode switch and a render can never derive
// two different budgets for the same frame (the same reason
// internal/chat's own metrics type exists).
type metrics struct {
	bodyHeight int
}

func (m Model) metrics() metrics {
	height := m.height
	if height <= 0 {
		height = 30
	}
	// title line + column-header line + blank + legend line, the fixed
	// chrome View always renders outside the scrollable area.
	bodyHeight := height - 4
	if bodyHeight < 3 {
		bodyHeight = 3
	}
	return metrics{bodyHeight: bodyHeight}
}

// sync recomputes listVP/bodyVP size and content from current state --
// the ONE place either viewport's SetContent or Width/Height is touched,
// called after every event that could invalidate them (resize, fetch,
// selection/sort change, mode switch, a fact finishing its load).
func (m Model) sync() Model {
	mx := m.metrics()
	width := m.width
	if width <= 0 {
		width = 100
	}

	m.listVP.Width = width
	m.listVP.Height = mx.bodyHeight
	m.listVP.SetContent(m.renderListLines())
	m.ensureListVisible(mx)

	m.bodyVP.Width = width
	m.bodyVP.Height = mx.bodyHeight
	if f, ok := m.currentFact(); ok {
		m.bodyVP.SetContent(f.Body)
	} else {
		m.bodyVP.SetContent("")
	}

	return m
}

// ensureListVisible scrolls listVP so the selected row stays on screen --
// internal/chat.Model's own ensureListVisible, the same "content taller
// than the pane must be reachable" answer (agent-tui#29).
func (m *Model) ensureListVisible(mx metrics) {
	if m.listVP.Height <= 0 {
		return
	}
	if m.selected < m.listVP.YOffset {
		m.listVP.SetYOffset(m.selected)
		return
	}
	if m.selected >= m.listVP.YOffset+m.listVP.Height {
		m.listVP.SetYOffset(m.selected - m.listVP.Height + 1)
	}
}

func (m Model) currentFact() (Fact, bool) {
	rows := m.Rows()
	if m.selected < 0 || m.selected >= len(rows) {
		return Fact{}, false
	}
	f, ok := m.cache[rows[m.selected].Slug]
	return f, ok
}

// renderListLines builds listVP's own content -- one line per Row, the
// selected row highlighted -- factored out of View so sync (which must
// call SetContent) and View (which only reads listVP.View()) never
// duplicate this logic and drift apart.
func (m Model) renderListLines() string {
	rows := m.Rows()
	if len(rows) == 0 {
		return legendStyle.Render("(no facts)")
	}
	sel := selectedStyle(m.theme)
	var lines []string
	for i, r := range rows {
		typ := unknown
		if r.Type != nil {
			typ = *r.Type
		}
		created := unknown
		if r.Created != nil {
			created = *r.Created
		}
		titleDesc := r.Title
		if r.Description != "" {
			titleDesc = r.Title + " -- " + r.Description
		}
		line := fmt.Sprintf("%-40s %-10s %-56s %s", truncate(r.Slug, 40), truncate(typ, 10), truncate(titleDesc, 56), created)
		if i == m.selected {
			line = sel.Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// scrollIndicatorText is a one-line "hidden content" marker for vp, PLAIN
// (unstyled, untruncated) -- internal/chat's own answer to agent-tui#29's
// acceptance ("a view with content past its edges must say so, never
// truncate silently"), returned as plain text so view.go can truncate it
// to width BEFORE styling (see View's own doc comment on why that order
// matters).
func scrollIndicatorText(vp viewport.Model) string {
	if vp.TotalLineCount() <= vp.Height {
		return ""
	}
	above, below := "", ""
	if !vp.AtTop() {
		above = "▲ more above "
	}
	if !vp.AtBottom() {
		below = "▼ more below "
	}
	pct := int(vp.ScrollPercent() * 100)
	return fmt.Sprintf("%s%s(%d%%)", above, below, pct)
}
