package theme

import "github.com/charmbracelet/lipgloss"

// Default is today's appearance, unchanged: every colour, border and
// padding value here was measured out of board/layout.go, board/model.go,
// cost/model.go, gallery/model.go, rail/model.go and rail/sessions.go
// before this package existed (see this PR's inventory) and moved here
// byte-for-byte. Agent-tui#27 is explicit -- "do not invent a palette...
// ship the mechanism with today's appearance as the default theme" -- so
// this is a relocation, not a redesign.
var Default = Theme{
	ID:          "signal-dark",
	Name:        "Signal (default)",
	Description: "today's appearance, unchanged -- the pre-#27 hardcoded look, relocated here byte-for-byte",
	Colors: map[Role]lipgloss.Color{
		RoleError:        lipgloss.Color("#ff5555"), // board/cost/rail errStyle
		RoleWarn:         lipgloss.Color("#f1c40f"), // board cardWarnColor, cost warnStyle
		RoleFlag:         lipgloss.Color("#e0c34c"), // gallery flagStyle
		RoleDirector:     lipgloss.Color("#ffd166"), // rail directorAccent
		RoleUnsupervised: lipgloss.Color("#e0a94c"), // rail unsupervisedAccent
		RoleSelectedBG:   lipgloss.Color("#33475b"), // rail selectionBackground
		RoleBorder:       lipgloss.Color("#444444"), // rail borderStyle
		RoleBacklog:      lipgloss.Color("#7f8c9a"), // board vividColumnColor[Backlog]
		RoleInProgress:   lipgloss.Color("#3b82f6"), // board vividColumnColor[InProgress]
		RoleInReview:     lipgloss.Color("#eab308"), // board vividColumnColor[InReview]
		RoleBlocked:      lipgloss.Color("#ef4444"), // board vividColumnColor[Blocked]
		RoleDone:         lipgloss.Color("#22c55e"), // board vividColumnColor[Done]
		RoleNeutral:      lipgloss.Color("#6b7280"), // board restrainedColumnColor
	},
	Border:       lipgloss.RoundedBorder(),
	Padding:      1,
	DirectorMark: "★",
}

// Mono is the seam's proof, not a shipped aesthetic. Agent-tui#27's own
// acceptance test is "one theme proves nothing" -- Mono exists so a
// literal left un-routed has somewhere to visibly fail to change. Every
// role gets a colour that is not Default's, the border switches from
// rounded to thick, padding widens, and the director mark changes glyph
// entirely, so the mutation check (un-route one literal, watch the
// two-theme test fail) has something to catch on every axis the inventory
// found. The hill90 palette Jon actually wants is a follow-up (#27: "do
// not invent a palette to ship with"); this is deliberately not it.
var Mono = Theme{
	ID:          "mono-contrast",
	Name:        "Mono (verification)",
	Description: "high-contrast, every role visibly different from Default -- proves the routing, not a design proposal",
	Colors: map[Role]lipgloss.Color{
		RoleError:        lipgloss.Color("#ff0044"),
		RoleWarn:         lipgloss.Color("#ffee00"),
		RoleFlag:         lipgloss.Color("#00ffee"),
		RoleDirector:     lipgloss.Color("#ffffff"),
		RoleUnsupervised: lipgloss.Color("#ff8800"),
		RoleSelectedBG:   lipgloss.Color("#000080"),
		RoleBorder:       lipgloss.Color("#ffffff"),
		RoleBacklog:      lipgloss.Color("#888888"),
		RoleInProgress:   lipgloss.Color("#0044ff"),
		RoleInReview:     lipgloss.Color("#ffaa00"),
		RoleBlocked:      lipgloss.Color("#ff0000"),
		RoleDone:         lipgloss.Color("#00ff44"),
		RoleNeutral:      lipgloss.Color("#aaaaaa"),
	},
	Border:       lipgloss.ThickBorder(),
	Padding:      2,
	DirectorMark: "◆",
}

// All is every theme this build ships, in picker order -- the same
// data-behind-a-slice shape internal/lane.Variants and internal/board.Layouts
// already use, so adding a third theme (the hill90 follow-up) is a line
// here, not a new code path.
var All = []Theme{Default, Mono}

// ByID returns the theme whose ID matches id and true, or Default and
// false when id names nothing this build ships -- the caller (Load, or
// anything else resolving a user preference) is the one that decides
// whether false means "say so visibly" or "silently use the default".
func ByID(id string) (Theme, bool) {
	for _, t := range All {
		if t.ID == id {
			return t, true
		}
	}
	return Default, false
}

// Cycle returns the theme in All that follows current, wrapping around --
// agent-tui#25's other half of "per-user, persisted": the config file
// (config.go's Load/Save) is the committed preference, but issue #25's
// scope item 3 also asks for "switchable at runtime as well as at
// startup, if it is cheap... he has consistently wanted to compare rather
// than commit." This is that comparison -- a live, in-memory swap the
// four screens' Update loops call on a keypress (see each package's own
// "t" case), same shape as lane.Variants' glyph-set cycle and this rail's
// own group-style cycle. It never writes theme.Save; the config file is
// untouched until a user edits it themselves, exactly as the issue
// requires ("the user cannot set it by editing their own config" is what
// would make a theme control wrong).
//
// A current theme not present in All (defensive -- every caller today
// only ever holds a Theme that came from Default, ByID, or this function)
// cycles to All[0] rather than panicking or standing still.
func Cycle(current Theme) Theme {
	for i, t := range All {
		if t.ID == current.ID {
			return All[(i+1)%len(All)]
		}
	}
	return All[0]
}
