package library

import (
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

// refreshInterval matches internal/knowledge's own reasoning: the corpus
// changes by the itemize_prompts.py pipeline running (a batch, not a
// per-second write) -- a slow poll plus [r] for "I just loaded a new
// batch" is the same shape.
const refreshInterval = 30 * time.Second

type refreshMsg time.Time
type fetchResultMsg struct {
	rows []ItemRow
	err  error
}
type countResultMsg struct {
	count int
	err   error
}
type detailResultMsg struct {
	id     string
	detail ItemDetail
	err    error
}

// mode is which of the two panes this Model currently shows -- the list
// (one View's own filtered rows) or, once a row is opened, that item's
// full detail (ItemDetail, including its originating prompt).
type mode int

const (
	modeList mode = iota
	modeReading
)

// weightCycle/statusCycle are the fixed orders [f]/[x] step through --
// "" means no filter, always first, so the default view is unfiltered.
var weightCycle = []string{"", "hard", "preference", "retracted"}
var statusCycle = []string{"", "open", "acknowledged", "acted", "resolved", "dropped"}

func filterLabel(s string) string {
	if s == "" {
		return "any"
	}
	return s
}

// Model is the library view: the corpus's own live_parameters/
// open_questions/unacknowledged views, cycled with [v], each optionally
// narrowed by weight ([f]) and status ([x]), plus possibility_count shown
// as a standing legend regardless of which view is active. Read-only --
// this package has no write path, no key or method that touches the
// ledger for anything but a SELECT. Wired into internal/shell via
// WithLibrary (cmd/estate/main.go).
//
// The corpus itself is a SUPPLIED CHOICE (agent-estate#1088), cycled with
// [c] across sources -- today the shared prompt/decision ledger
// (internal/board's own database, this package's original target) and the
// operator's own ~/corpus, both read-only. The shared corpus stays index 0
// (the default) unless a caller's own Source order says otherwise; see
// cmd/estate/library.go for which two sources this repo actually wires in
// and why the shared one is still first.
type Model struct {
	sources   []Source
	sourceIdx int

	// fetch/loadDetail/fetchCount always mirror sources[sourceIdx] -- kept
	// as their own fields, rather than read through sources on every call,
	// because every existing call site in this file already reads them
	// this way (predates multi-source support) and cycling sources is rare
	// (one key, [c]) next to every other read.
	fetch      Fetcher
	loadDetail DetailLoader
	fetchCount CountFetcher

	view   View
	weight string
	status string

	rows     []ItemRow
	fetchErr error
	// unconfigured is true when New was given a nil Fetcher -- no -ledger
	// (or its auto-discovered default) was available at all, so Init never
	// issues a fetch and fetchErr would otherwise stay nil forever. view.go
	// renders this as its own visible error, distinct from "fetched and
	// found zero rows" (w5c.md's own hard requirement: missing/unreadable
	// is a VISIBLE error, never an empty list).
	unconfigured bool

	count    int
	countErr error

	// cache holds every item this Model has actually opened THIS session --
	// the one place an item's full body/prompt context live once read,
	// never populated by anything but a real open (internal/knowledge's
	// own cache, same shape).
	cache map[string]ItemDetail

	selected int
	mode     mode
	// opening is the id a detailResultMsg is still in flight for -- so a
	// second [enter] on the same row while it is loading doesn't fire a
	// second read, and so the reading pane can say "loading" rather than
	// show stale content from whatever was open before.
	opening string
	readErr string

	listVP viewport.Model
	bodyVP viewport.Model

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a single-source Model with fetch/loadDetail/fetchCount wired
// in directly -- sugar for NewSources with one unnamed Source, kept for
// every existing caller (including this package's own tests) that has no
// second corpus to offer. fetch == nil (cmd/estate had no ledger to build
// one from) is a distinct, VISIBLE state from "fetch ran and found zero
// rows" -- w5c.md's own hard requirement -- tracked here as unconfigured
// rather than left to read as an empty (Init never issues doFetch, so
// m.fetchErr would otherwise stay nil forever and the list would silently
// render "(no items)" for a ledger that was never even asked).
func New(fetch Fetcher, loadDetail DetailLoader, fetchCount CountFetcher) Model {
	return NewSources([]Source{{Fetch: fetch, LoadDetail: loadDetail, FetchCount: fetchCount}})
}

// NewSources builds a Model over one or more corpora, starting on sources[0]
// (agent-estate#1088: the shared corpus stays the default; cmd/estate/
// library.go is what decides the order). A caller passing an empty slice
// gets a single unconfigured Source, matching New(nil, nil, nil)'s own
// behaviour rather than panicking on sources[0].
func NewSources(sources []Source) Model {
	if len(sources) == 0 {
		sources = []Source{{}}
	}
	m := Model{
		sources:      sources,
		sourceIdx:    0,
		unconfigured: sources[0].Fetch == nil,
		view:         ViewLiveParameters,
		cache:        map[string]ItemDetail{},
		listVP:       viewport.New(0, 0),
		bodyVP:       viewport.New(0, 0),
		width:        100,
		height:       30,
		theme:        theme.Default,
	}
	m.applySource()
	return m
}

// applySource copies the current sources[sourceIdx]'s three seams into
// fetch/loadDetail/fetchCount and recomputes unconfigured -- the one place
// a source switch ([c]) or initial build (NewSources) touches those three
// fields, so every other method in this file keeps reading them exactly as
// it did before multi-source support existed.
func (m *Model) applySource() {
	src := m.sources[m.sourceIdx]
	m.fetch = src.Fetch
	m.loadDetail = src.LoadDetail
	m.fetchCount = src.FetchCount
	m.unconfigured = src.Fetch == nil
}

// currentSourceName is what view.go shows for the active corpus -- falls
// back to "corpus" for a Source built with no Name (New's single-source
// sugar, and this package's own tests), so the title line is never blank.
func (m Model) currentSourceName() string {
	name := m.sources[m.sourceIdx].Name
	if name == "" {
		return "corpus"
	}
	return name
}

// WithTheme returns a copy of m painted with th -- the same per-pane seam
// every other package in this repo exposes.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m.sync()
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(refreshCmd(), doFetch(m.fetch, m.view, m.weight, m.status), doFetchCount(m.fetchCount))
}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func doFetch(fetch Fetcher, view View, weight, status string) tea.Cmd {
	if fetch == nil {
		return nil
	}
	return func() tea.Msg {
		rows, err := fetch(view, weight, status)
		return fetchResultMsg{rows: rows, err: err}
	}
}

func doFetchCount(fetchCount CountFetcher) tea.Cmd {
	if fetchCount == nil {
		return nil
	}
	return func() tea.Msg {
		count, err := fetchCount()
		return countResultMsg{count: count, err: err}
	}
}

func doLoadDetail(loadDetail DetailLoader, id string) tea.Cmd {
	if loadDetail == nil {
		return nil
	}
	return func() tea.Msg {
		detail, err := loadDetail(id)
		return detailResultMsg{id: id, detail: detail, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.sync(), nil

	case refreshMsg:
		return m, tea.Batch(refreshCmd(), doFetch(m.fetch, m.view, m.weight, m.status), doFetchCount(m.fetchCount))

	case fetchResultMsg:
		m.fetchErr = msg.err
		if msg.err == nil {
			m.rows = msg.rows
			if m.selected >= len(m.rows) {
				m.selected = len(m.rows) - 1
			}
			if m.selected < 0 {
				m.selected = 0
			}
		}
		return m.sync(), nil

	case countResultMsg:
		m.countErr = msg.err
		if msg.err == nil {
			m.count = msg.count
		}
		return m, nil

	case detailResultMsg:
		if msg.id != m.opening {
			// A refresh, a filter change, or a second open superseded this
			// one; drop it -- never let a stale load overwrite what is
			// actually on screen (internal/knowledge's own rule).
			return m, nil
		}
		m.opening = ""
		if msg.err != nil {
			m.readErr = msg.err.Error()
			return m.sync(), nil
		}
		m.readErr = ""
		m.cache[msg.id] = msg.detail
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
	case "c":
		// Handled here, above the modeReading branch below, deliberately --
		// [c] must work from EITHER pane (list or an open item), the same
		// as [q]/[t] just above it. A switch always returns to modeList:
		// staying in modeReading would keep showing the OLD corpus's
		// bodyVP content (or "(loading)") under a title that now names the
		// new corpus, which reads as data leaking across the switch.
		if len(m.sources) < 2 {
			// Nothing to cycle to -- e.g. New's single-source sugar, or
			// cmd/estate found no second corpus configured. A no-op, not
			// an error: [c] simply has nothing to do yet.
			return m, nil
		}
		m.sourceIdx = (m.sourceIdx + 1) % len(m.sources)
		m.applySource()
		m.selected = 0
		// A different corpus is a different database -- items opened
		// under the old source stay cached under ids that mean nothing
		// here, and could even collide with a same-shaped id from this
		// source. Clear rather than risk showing the wrong corpus's text
		// under a coincidentally-matching id.
		m.rows = nil
		m.cache = map[string]ItemDetail{}
		m.mode = modeList
		m.opening = ""
		m.readErr = ""
		return m.sync(), tea.Batch(doFetch(m.fetch, m.view, m.weight, m.status), doFetchCount(m.fetchCount))
	}

	if m.mode == modeReading {
		switch msg.String() {
		case "esc", "left":
			m.mode = modeList
			return m.sync(), nil
		case "r":
			if m.selected < 0 || m.selected >= len(m.rows) {
				return m, nil
			}
			id := m.rows[m.selected].ID
			m.opening = id
			m.readErr = ""
			return m, doLoadDetail(m.loadDetail, id)
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
		return m, tea.Batch(doFetch(m.fetch, m.view, m.weight, m.status), doFetchCount(m.fetchCount))
	case "v":
		m.view = nextView(m.view)
		m.selected = 0
		return m.sync(), doFetch(m.fetch, m.view, m.weight, m.status)
	case "f":
		m.weight = nextInCycle(weightCycle, m.weight)
		m.selected = 0
		return m.sync(), doFetch(m.fetch, m.view, m.weight, m.status)
	case "x":
		m.status = nextInCycle(statusCycle, m.status)
		m.selected = 0
		return m.sync(), doFetch(m.fetch, m.view, m.weight, m.status)
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
		return m.sync(), nil
	case "down", "j":
		if m.selected < len(m.rows)-1 {
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
		if m.selected < 0 || m.selected >= len(m.rows) {
			return m, nil
		}
		id := m.rows[m.selected].ID
		m.mode = modeReading
		m.readErr = ""
		if _, ok := m.cache[id]; ok {
			// Already opened this session -- no re-read, per this
			// package's own progressive-disclosure constraint.
			return m.sync(), nil
		}
		m.opening = id
		return m.sync(), doLoadDetail(m.loadDetail, id)
	}
	return m, nil
}

func nextView(cur View) View {
	for i, v := range Views {
		if v == cur {
			return Views[(i+1)%len(Views)]
		}
	}
	return Views[0]
}

func nextInCycle(cycle []string, cur string) string {
	for i, v := range cycle {
		if v == cur {
			return cycle[(i+1)%len(cycle)]
		}
	}
	return cycle[0]
}

// metrics is the width/height split both sync and View need to agree on --
// internal/knowledge's own metrics type, same reason.
type metrics struct {
	bodyHeight int
}

func (m Model) metrics() metrics {
	height := m.height
	if height <= 0 {
		height = 30
	}
	// Five fixed lines viewList always renders outside the scrollable
	// area: title, column-header/error line, the scroll indicator,
	// possibility_count's own legend line, and the bottom legend line --
	// ONE MORE than internal/knowledge's own four (this package adds
	// possibility_count as a standing line knowledge has no equivalent
	// of). Found by driving a real teatest.Program, not by inspecting the
	// string: with the old bodyHeight=height-4 budget, viewList()'s ACTUAL
	// output was consistently one line taller than the terminal's fixed
	// row count, and a real terminal (vt10x, teatest's own virtual one)
	// SCROLLS rather than truncates when content overflows a fixed-height
	// screen -- so the title line silently scrolled off the top on every
	// single frame. View()'s own returned string was correct the whole
	// time (confirmed independently); only the BUDGET this function hands
	// out was wrong.
	bodyHeight := height - 5
	if bodyHeight < 3 {
		bodyHeight = 3
	}
	return metrics{bodyHeight: bodyHeight}
}

// sync recomputes listVP/bodyVP size and content from current state --
// the ONE place either viewport's SetContent or Width/Height is touched.
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
	if id, ok := m.currentID(); ok {
		if d, ok := m.cache[id]; ok {
			m.bodyVP.SetContent(m.renderDetailBody(d))
		} else {
			m.bodyVP.SetContent("")
		}
	} else {
		m.bodyVP.SetContent("")
	}

	return m
}

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

func (m Model) currentID() (string, bool) {
	if m.selected < 0 || m.selected >= len(m.rows) {
		return "", false
	}
	return m.rows[m.selected].ID, true
}
