// Package laneprimary is agent-tui#115's first combined-surface variant:
// "the rail stays the spine, a conversation opens against the selected
// lane" -- the issue's own first named direction, built for real rather
// than described. The left column is exactly what internal/rail already
// renders (a narrow list of lane.Lane rows, one glyph per state, from
// lane.Variants[0]); the right column is ONE conversation at a time -- the
// thread belonging to whichever lane is currently selected on the left,
// never a second, independent thread list. Moving the rail selection
// changes which conversation is on screen; there is no second navigation
// axis to keep in sync with it.
//
// Implication for the nav tree (agent-tui#115's own hard constraint: a
// variant that merges a destination must say so, not merge it quietly):
// this shape folds Chat INTO Lanes -- a conversation is reached by
// selecting a lane, not by a separate "Chat" destination in
// internal/nav.Build()'s tree. It does not touch internal/nav.Build() or
// internal/shell's existing "chat"/"lanes" routes -- both stay exactly as
// they are; this is a prototype of what selecting a lane COULD open, not a
// change to what selecting Lanes or Chat does today.
package laneprimary

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/chat"
	"github.com/jonhill90/keelson/internal/lane"
	"github.com/jonhill90/keelson/internal/lanechat"
	"github.com/jonhill90/keelson/internal/theme"
)

// railWidth mirrors internal/rail.RailWidth -- the same "roughly 24-32
// columns" band agent-supervisor#107 asked for, kept as a literal here
// rather than importing internal/rail (this pane renders lane.Lane rows
// itself; it has no other reason to depend on that package).
const railWidth = 28

// Model is laneprimary's Bubble Tea model. Like internal/gallery.Model, it
// takes no live Fetcher -- New()'s data is internal/lanechat's shared
// fixture, compiled into the binary, never fetched.
type Model struct {
	lanes    []lane.Lane
	threads  map[string]chat.Thread // keyed by lane.Lane.Name
	selected int

	composer  textinput.Model
	composing bool
	status    string // last compose-time outcome, empty when nothing to report

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model over internal/lanechat's shared fixture -- the same
// five lanes and five threads roomprimary and unifiedlist render, so a
// human comparing the three is comparing one dataset laid out three ways.
func New() Model {
	lanes := lanechat.FixtureLanes()
	byName := make(map[string]chat.Thread, len(lanes))
	for _, t := range lanechat.FixtureThreads() {
		byName[t.Lane] = t
	}
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 4000
	return Model{
		lanes:    lanes,
		threads:  byName,
		composer: ti,
		width:    100,
		height:   30,
		theme:    theme.Default,
	}
}

// WithTheme mirrors every other pane's seam (see gallery.Model.WithTheme's
// doc comment for the shared rationale).
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) selectedLane() (lane.Lane, bool) {
	if m.selected < 0 || m.selected >= len(m.lanes) {
		return lane.Lane{}, false
	}
	return m.lanes[m.selected], true
}

func (m Model) selectedThread() (chat.Thread, bool) {
	l, ok := m.selectedLane()
	if !ok {
		return chat.Thread{}, false
	}
	t, ok := m.threads[l.Name]
	return t, ok
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
			if m.selected < len(m.lanes)-1 {
				m.selected++
			}
			return m, nil
		case "i":
			if _, ok := m.selectedThread(); ok {
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
		// The same compose-time gate agent-tui#114 built for internal/chat,
		// reused rather than reimplemented: a bad @-mention is refused
		// here, before anything that looks like a send. There is no
		// chat.Sender wired into this fixture pane at all (per this
		// package's own doc comment -- no live lane behind any of this
		// data), so a VALID mention still never reaches a daemon; it is
		// reported as queued-against-a-fixture, honestly, never as
		// delivered.
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
	out += st.title.Render("lane+chat variant: lane-primary") + "\n"
	if m.themeNotice != "" {
		out += st.err.Render("! "+m.themeNotice) + "\n"
	}
	out += st.dim.Render("FIXTURE DATA -- not a live estate. the rail stays the spine; selecting a lane opens its conversation.") + "\n"
	out += st.dim.Render("[j/k] move  [i] compose  [enter] send  [esc] cancel  [t] theme  [q] quit") + "\n\n"

	left := m.renderRail(st)
	right := m.renderConversation(st)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	out += body
	return out
}

func (m Model) renderRail(st styles) string {
	var b []string
	b = append(b, st.dim.Render("lanes"))
	for i, l := range m.lanes {
		set := lane.Variants[0]
		style := lane.StyleFor(set, l.State)
		glyph := "?"
		if len(style.Frames) > 0 {
			glyph = style.Frames[0]
		}
		row := fmt.Sprintf("%s %-16s %s", glyph, truncate(l.Name, 16), l.State)
		if i == m.selected {
			row = st.sel.Render(row)
		}
		b = append(b, row)
	}
	return lipgloss.NewStyle().Width(railWidth).Render(strings.Join(b, "\n"))
}

func (m Model) renderConversation(st styles) string {
	l, haveLane := m.selectedLane()
	t, haveThread := m.selectedThread()
	if !haveLane {
		return st.dim.Render("(no lane selected)")
	}

	var b []string
	b = append(b, st.title.Render("-- "+l.Name+" -- "+l.State+" --"))
	if !haveThread {
		b = append(b, st.dim.Render("(no conversation recorded for this lane)"))
	} else {
		for _, msg := range t.Messages {
			for _, line := range chat.RenderMessage(msg) {
				b = append(b, highlightMentions(line, st))
			}
		}
	}
	b = append(b, "")
	// The input line reflects composing state; the status line reflects
	// m.status independently of it -- a refused send (ValidateMentions,
	// handleComposerKey's own "enter" case) leaves m.composing true so the
	// draft is never lost, so the status line must render on BOTH sides of
	// that branch, not just the idle one. Missing this was found live: a
	// refusal rendered "[enter] send  [esc] cancel" forever with no
	// visible error at all, the same "! cannot send" text
	// internal/chat/model.go's own renderComposer always shows regardless
	// of composing state.
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

// highlightMentions colours every @token in line -- the same rendering
// requirement agent-tui#114 built for internal/chat (see that package's
// own highlightMentions doc comment), applied here with the same parser
// (chat.ParseMentions) so the two never disagree on what counts as a
// mention.
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
