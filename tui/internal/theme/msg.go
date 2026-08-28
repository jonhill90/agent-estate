package theme

// CycleRequestedMsg is a Bubble Tea message a pane sends UP (as the tea.Cmd
// returned from its own Update) when its "t" key is pressed, rather than
// cycling its own theme field in place. Agent-tui#48 shipped four packages
// (board/cost/gallery/rail) each holding an independent in-memory theme
// value and each duplicating "m.theme = theme.Cycle(m.theme)" in their own
// Update -- one keypress only ever changed whichever ONE pane had focus,
// so the rail and the active content pane could show two different themes
// on screen at once, and none of the four ever called Save, so the choice
// never survived a restart. Agent-tui#51 fixes both by moving the theme
// value itself to internal/shell.Model, the one package that already
// composes every pane: a pane's "t" case now only asks for a cycle via
// this message, exactly the same way a fetch result already flows back up
// through a tea.Cmd's returned Msg. shell.Model is the only place that
// type-switches on CycleRequestedMsg, cycles its own theme.Theme, calls
// theme.Save once, and pushes the new value into all four panes via their
// existing WithTheme -- one owner, one write, every surface repainting
// together. A pane's own text-input gating (rail's opsMode, see
// handleOpsKey) still runs BEFORE this case is ever reached, so a session
// name that happens to contain the letter 't' is untouched -- nothing
// about how 't' reaches a pane's Update changed, only what happens once it
// does.
type CycleRequestedMsg struct{}
