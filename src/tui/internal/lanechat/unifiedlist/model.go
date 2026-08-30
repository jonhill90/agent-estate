// Package unifiedlist is agent-tui#115's third combined-surface variant:
// "lanes and threads as one list of 'agents you can talk to', state and
// conversation on the same row" -- the issue's own third named direction,
// and the one structurally furthest from the other two. laneprimary and
// roomprimary both keep a two-region layout (a persistent list beside a
// persistent transcript); this one has no second region at all. Every row
// IS the agent -- glyph, name, idle time and an unread/last-message
// preview on one line -- and selecting a row EXPANDS it in place
// (accordion-style) to show its transcript and composer, pushing the rows
// below it down, rather than opening a separate pane. Closing it (moving
// selection away, or [esc]) collapses it back to one line. That is the
// genuine difference in kind from the other two variants: there is
// nothing here shaped like a rail or a room list beside a transcript --
// there is exactly one list, and "open" is a per-row state, not a second
// column.
//
// Implication for the nav tree (agent-tui#115's own hard constraint): of
// the three variants, this one merges the MOST -- there is no rail concept
// left at all, and no separate "conversation view" either; a nav tree
// built around this shape would replace both "Lanes" and "Chat" with one
// destination ("Agents", or similar) whose content IS this list. As with
// laneprimary/roomprimary, this package makes no such change:
// internal/nav.Build() and internal/shell's existing "lanes"/"chat" routes
// are untouched.
package unifiedlist

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/src/tui/internal/chat"
	"github.com/jonhill90/agent-estate/src/tui/internal/lane"
	"github.com/jonhill90/agent-estate/src/tui/internal/lanechat"
	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

type row struct {
	lane   lane.Lane
	thread chat.Thread
}

// Model is unifiedlist's Bubble Tea model -- no live Fetcher, same
// compiled-in-fixture posture as the other two variants.
type Model struct {
	rows     []row
	selected int
	expanded bool // whether rows[selected] is the one open accordion panel

	composer  textinput.Model
	composing bool
	status    string

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model over internal/lanechat's shared fixture -- the same
// dataset laneprimary and roomprimary render, laid out a third way.
func New() Model {
	lanes := lanechat.FixtureLanes()
	byName := make(map[string]chat.Thread, len(lanes))
	for _, t := range lanechat.FixtureThreads() {
		byName[t.Lane] = t
	}
	rows := make([]row, 0, len(lanes))
	for _, l := range lanes {
		rows = append(rows, row{lane: l, thread: byName[l.Name]})
	}
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 4000
	return Model{rows: rows, composer: ti, width: 100, height: 30, theme: theme.Default}
}

func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.composing {
			return m.handleComposerKey(msg)
		}
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
			if m.selected < len(m.rows)-1 {
				m.selected++
			}
			return m, nil
		case "enter":
			// The accordion toggle -- the one interaction that has no
			// equivalent in laneprimary/roomprimary, both of which always
			// show a transcript for whatever is selected. Here, selecting
			// a row is not the same as opening it.
			m.expanded = !m.expanded
			return m, nil
		case "esc":
			m.expanded = false
			return m, nil
		case "i":
			if m.expanded {
				m.composing = true
				m.status = ""
				m.composer.SetValue("")
				m.composer.Focus()
				return m, textinput.Blink
			}
			return m, nil
		case "t":
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
	}
	return m, nil
}

func (m Model) handleComposerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.composing = false
		m.composer.Blur()
		m.composer.SetValue("")
		m.status = ""
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.composer.Value())
		if text == "" {
			return m, nil
		}
		if err := chat.ValidateMentions(text, lanechat.FixtureParticipants()); err != nil {
			m.status = "! " + err.Error()
			return m, nil
		}
		m.status = "queued (fixture -- no live lane behind this room)"
		m.composer.SetValue("")
		m.composing = false
		m.composer.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	return m, cmd
}

type styles struct {
	dim, sel, title, warn, mention, err lipgloss.Style
}

func (m Model) styles() styles {
	th := m.theme
	return styles{
		dim:     lipgloss.NewStyle().Faint(true),
		sel:     lipgloss.NewStyle().Bold(true).Background(th.Color(theme.RoleSelectedBG)),
		title:   lipgloss.NewStyle().Bold(true),
		warn:    lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleWarn)),
		mention: lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleMention)),
		err:     lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleError)),
	}
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	st := m.styles()

	var out string
	out += st.title.Render("lane+chat variant: unified-list") + "\n"
	if m.themeNotice != "" {
		out += st.err.Render("! "+m.themeNotice) + "\n"
	}
	out += st.dim.Render("FIXTURE DATA -- not a live estate. one list, every agent; [enter] expands a row in place.") + "\n"
	out += st.dim.Render("[j/k] move  [enter] expand/collapse  [i] compose (when expanded)  [t] theme  [q] quit") + "\n\n"

	var b []string
	for i, r := range m.rows {
		b = append(b, m.renderRow(i, r, st))
		if i == m.selected && m.expanded {
			b = append(b, m.renderPanel(r, st))
		}
	}
	out += strings.Join(b, "\n")
	return out
}

func (m Model) renderRow(i int, r row, st styles) string {
	set := lane.Variants[0]
	style := lane.StyleFor(set, r.lane.State)
	glyph := "?"
	if len(style.Frames) > 0 {
		glyph = style.Frames[0]
	}
	mark := " "
	if r.thread.Unread {
		mark = "*"
	}
	last := "(no messages)"
	if n := len(r.thread.Messages); n > 0 {
		last = summarize(r.thread.Messages[n-1])
	}
	line := fmt.Sprintf("%s%s %-16s %-12s idle:%-5ds %s", mark, glyph, truncate(r.lane.Name, 16), r.lane.State, r.lane.IdleSeconds, last)
	if i == m.selected {
		return st.sel.Render(line)
	}
	return line
}

// renderPanel is the accordion body -- the transcript and composer for
// whichever row is currently expanded, indented so it visually nests
// under the row it belongs to rather than reading as a sibling row.
func (m Model) renderPanel(r row, st styles) string {
	const indent = "    "
	var b []string
	if len(r.thread.Messages) == 0 {
		b = append(b, indent+st.dim.Render("(no conversation recorded)"))
	}
	for _, msg := range r.thread.Messages {
		for _, line := range chat.RenderMessage(msg) {
			b = append(b, indent+highlightMentions(line, st))
		}
	}
	// The input line reflects composing state; the status line reflects
	// m.status independently of it -- see laneprimary/model.go's identical
	// comment on this same fix for why a refused send must still show
	// while m.composing stays true.
	if m.composing {
		b = append(b, indent+m.composer.View())
	} else {
		b = append(b, indent+st.dim.Render("> (press [i] to compose)"))
	}
	switch {
	case strings.HasPrefix(m.status, "!"):
		b = append(b, indent+st.err.Render(m.status))
	case m.status != "":
		b = append(b, indent+st.warn.Render(m.status))
	case m.composing:
		b = append(b, indent+st.dim.Render("[enter] send  [esc] cancel"))
	}
	b = append(b, "")
	return strings.Join(b, "\n")
}

func summarize(msg chat.Message) string {
	lines := chat.RenderMessage(msg)
	if len(lines) == 0 {
		return ""
	}
	return truncate(lines[0], 60)
}

func highlightMentions(line string, st styles) string {
	names := chat.ParseMentions(line)
	if len(names) == 0 {
		return line
	}
	out := line
	for _, n := range names {
		out = strings.ReplaceAll(out, "@"+n, st.mention.Render("@"+n))
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
