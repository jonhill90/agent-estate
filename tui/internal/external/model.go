// Package external renders a nav destination that is not a pane at all:
// nav.KindExternal, an href the web nav opens in a browser
// (hill90-app/services/ui/src/components/nav-items.ts marks it
// `external: true`). Today that is exactly one entry, Platform Docs ->
// https://docs.hill90.com.
//
// Why this exists rather than a stub: the nav tree declared Platform Docs
// KindExternal from the day it was written, but internal/shell routed it
// to internal/stub like any unbuilt route, so the pane said "not built
// yet" -- which is false twice over. It is not unbuilt, and it is never
// going to be built: a Mintlify documentation site is not something a
// terminal pane renders. The declaration was right and the routing was
// wrong (agent-tui#94's walk row 19 recorded the contradiction without
// resolving it). This package makes the route behave the way it is
// declared: it names the destination and opens it, and it never claims a
// pane is coming.
package external

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

// Opener hands a URL to the host's browser. It is the adapter seam
// (AGENTS.md): nothing under internal/ may call os/exec, so cmd/estate
// supplies the real one and every test here supplies a fake. A nil Opener
// is a real state -- the view says opening is unavailable and still shows
// the URL, which is the part a human can act on by hand.
type Opener func(url string) error

type openResultMsg struct {
	url string
	err error
}

// Model is the pane for one external destination. It holds no fetched
// data because there is nothing to fetch: an external href is nav data,
// not a projection of estate state.
type Model struct {
	title string
	url   string
	open  Opener

	// opened/openErr record the LAST open attempt, so the pane can tell a
	// reader what happened rather than appearing to do nothing -- a
	// browser opens behind the terminal and is easy to miss.
	opened   bool
	openErr  error
	attempts int

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model for one destination.
func New(open Opener) Model {
	return Model{open: open, width: 100, height: 30, theme: theme.Default}
}

// WithDestination returns a copy of m pointed at title/url. The shell
// calls this when an external route is selected, so one Model serves every
// KindExternal entry rather than the shell holding one per destination --
// there is one today and the nav tree is 1:1 with a web nav that may add
// another.
func (m Model) WithDestination(title, url string) Model {
	if title != m.title || url != m.url {
		m.opened, m.openErr, m.attempts = false, nil, 0
	}
	m.title, m.url = title, url
	return m
}

// WithTheme returns a copy of m painted with th.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

// URL is the destination this pane would open, exported so a caller or a
// test can assert on it without parsing View's output.
func (m Model) URL() string { return m.url }

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case openResultMsg:
		m.openErr = msg.err
		m.opened = msg.err == nil
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "o":
			if m.open == nil || m.url == "" {
				return m, nil
			}
			m.attempts++
			url, open := m.url, m.open
			return m, func() tea.Msg { return openResultMsg{url: url, err: open(url)} }
		case "t":
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
	}
	return m, nil
}
