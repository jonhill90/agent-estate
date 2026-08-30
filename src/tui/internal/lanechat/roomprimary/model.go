// Package roomprimary is agent-tui#115's second combined-surface variant:
// "one room per lane, with the lane's live state as room metadata" -- the
// issue's own second named direction. Structurally this is
// internal/chat's own list+transcript shape (layouts.go's listLayout),
// but the room LIST ROW itself is the merge point: every row carries the
// lane's glyph and state inline, not just a title and an unread mark, so
// the room list reads as "agents, each with a conversation" rather than
// "conversations that happen to have a Lane field" (chat.Thread.Lane
// today, RenderThreadRow, does not surface state at all -- that is
// exactly the gap this variant fills in a different way than laneprimary
// does).
//
// Implication for the nav tree (agent-tui#115's own hard constraint): this
// shape makes the 1:1 between a lane and its room load-bearing -- every
// lane IS a room and every room IS a lane, so a nav tree built around this
// shape would have no reason to keep "Lanes" and "Chat" as two separate
// destinations; they would collapse into one ("Rooms", or similar). This
// package does not make that change -- internal/nav.Build() and
// internal/shell's existing "lanes"/"chat" routes are untouched; this is a
// prototype of the merged destination's CONTENT, not a change to the nav
// tree itself.
package roomprimary

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

// room is one row: a lane and the (exactly one, per this variant's own
// 1:1 rule) thread it owns.
type room struct {
	lane   lane.Lane
	thread chat.Thread
}

// Model is roomprimary's Bubble Tea model -- no live Fetcher, same
// "compiled-in fixture" posture as internal/gallery.Model and
// laneprimary.Model.
type Model struct {
	rooms    []room
	selected int

	composer  textinput.Model
	composing bool
	status    string

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model over internal/lanechat's shared fixture, joined 1:1 by
// lane name -- the same dataset laneprimary and unifiedlist render, laid
// out a third way.
func New() Model {
	lanes := lanechat.FixtureLanes()
	byName := make(map[string]chat.Thread, len(lanes))
	for _, t := range lanechat.FixtureThreads() {
		byName[t.Lane] = t
	}
	rooms := make([]room, 0, len(lanes))
	for _, l := range lanes {
		rooms = append(rooms, room{lane: l, thread: byName[l.Name]})
	}
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 4000
	return Model{rooms: rooms, composer: ti, width: 100, height: 30, theme: theme.Default}
}

func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) selectedRoom() (room, bool) {
	if m.selected < 0 || m.selected >= len(m.rooms) {
		return room{}, false
	}
	return m.rooms[m.selected], true
}

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
			if m.selected < len(m.rooms)-1 {
				m.selected++
			}
			return m, nil
		case "i":
			m.composing = true
			m.status = ""
			m.composer.SetValue("")
			m.composer.Focus()
			return m, textinput.Blink
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
	out += st.title.Render("lane+chat variant: room-primary") + "\n"
	if m.themeNotice != "" {
		out += st.err.Render("! "+m.themeNotice) + "\n"
	}
	out += st.dim.Render("FIXTURE DATA -- not a live estate. one room per lane; the row IS the lane, state and all.") + "\n"
	out += st.dim.Render("[j/k] move  [i] compose  [enter] send  [esc] cancel  [t] theme  [q] quit") + "\n\n"

	out += m.renderRoomList(st) + "\n\n"
	out += m.renderTranscript(st)
	return out
}

func (m Model) renderRoomList(st styles) string {
	var b []string
	b = append(b, st.dim.Render("rooms"))
	for i, r := range m.rooms {
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
		row := fmt.Sprintf("%s%s %-16s %-12s idle:%-5ds %s", mark, glyph, truncate(r.lane.Name, 16), r.lane.State, r.lane.IdleSeconds, last)
		if i == m.selected {
			row = st.sel.Render(row)
		}
		b = append(b, row)
	}
	return strings.Join(b, "\n")
}

func (m Model) renderTranscript(st styles) string {
	r, ok := m.selectedRoom()
	if !ok {
		return st.dim.Render("(no room selected)")
	}
	var b []string
	b = append(b, st.title.Render("-- "+r.lane.Name+" -- "+r.lane.State+" --"))
	if len(r.thread.Messages) == 0 {
		b = append(b, st.dim.Render("(no conversation recorded for this room)"))
	}
	for _, msg := range r.thread.Messages {
		for _, line := range chat.RenderMessage(msg) {
			b = append(b, highlightMentions(line, st))
		}
	}
	b = append(b, "")
	// The input line reflects composing state; the status line reflects
	// m.status independently of it -- see laneprimary/model.go's identical
	// comment on this same fix for why a refused send must still show
	// while m.composing stays true.
	if m.composing {
		b = append(b, m.composer.View())
	} else {
		b = append(b, st.dim.Render("> (press [i] to compose)"))
	}
	switch {
	case strings.HasPrefix(m.status, "!"):
		b = append(b, st.err.Render(m.status))
	case m.status != "":
		b = append(b, st.warn.Render(m.status))
	case m.composing:
		b = append(b, st.dim.Render("[enter] send  [esc] cancel"))
	default:
		b = append(b, st.dim.Render("[i] compose a message"))
	}
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
