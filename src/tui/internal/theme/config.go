package theme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// config is the on-disk shape of a user's theme preference. agent-tui#27 shipped
// exactly one field -- "theme", a name that selects a whole shipped
// Theme. Agent-tui#34 adds "colors": a role-name -> hex-string map letting
// a user define their own per-role colours instead of only picking
// between shipped ones, which is what agent-tui#33's review found agent-tui#27's format
// could not express ("per-role colours are compile-time data, not
// user-editable JSON").
// Colors is decoded as json.RawMessage per entry, not string: a
// map[string]string forces json.Unmarshal to fail the WHOLE file the
// moment one entry is a JSON number or object, discarding every other
// valid "colors" entry and "theme" along with it (agent-tui#36's finding
// against agent-tui#34 -- a typo'd colour lost a user's entire theme, not just
// that one role). Holding each entry as raw JSON until
// applyColorOverrides inspects it means a wrong-typed entry fails on its
// own, exactly like a wrong-shaped string already did.
type config struct {
	Theme  string                     `json:"theme"`
	Colors map[string]json.RawMessage `json:"colors"`
}

// hexColor matches the exact "#RRGGBB" shape every colour in registry.go
// already uses (see Default/Mono's Colors maps) -- a user-authored value is
// held to the same shape the shipped themes use, not a looser format this
// package would then have to special-case at render time.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// isKnownRole reports whether name matches a Role this build's Theme
// struct actually has a slot for -- the check config.go's "unknown role"
// notice (agent-tui#34) is built on.
func isKnownRole(name string) bool {
	for _, r := range AllRoles {
		if string(r) == name {
			return true
		}
	}
	return false
}

// applyColorOverrides layers colors on top of base, replacing only the
// roles colors names -- agent-tui#34's precedence decision, stated here
// because Load is where it takes effect:
//
// A user who overrides some roles of a shipped theme keeps that theme's
// colours for every role they did NOT mention. Overriding is additive per
// role, never a switch to "the user's palette replaces the theme's
// palette wholesale" -- the alternative (any override discards the rest of
// the base theme) would make a one-role edit silently blank out every
// other role to the zero-colour default, which is exactly the "partial
// override silently behaves like a full one" defect shape the brief for
// this issue calls out. base's own Colors map is never mutated -- All's
// shipped Theme values must stay identical across every Load call.
//
// Each entry in colors is validated against the three failure modes agent-tui#33's
// review named as having "nowhere to fail" before user-authored colours
// existed: a blank role name, a role name this build's Theme has no slot
// for, and a value that is not a colour. Each produces its own notice
// rather than a silent default, and the offending entry is dropped rather
// than applied -- every other, valid entry in the same file still takes
// effect. Notices are returned in role-name sorted order so repeated Load
// calls against the same file produce byte-identical output; Go's map
// iteration order is randomized and would otherwise make notice order
// flaky across runs.
//
// "not a colour" covers every wrong-typed JSON value, not just a
// wrong-shaped string: colors holds each entry as json.RawMessage (see
// config's doc comment), so a JSON number or nested object reaches here
// as raw bytes rather than failing json.Unmarshal for the whole file.
// rawValueText decodes each entry as a JSON string first (the valid
// shape); anything that isn't one -- 42, {"nested":"obj"}, true, null --
// falls into the same "not a colour" notice a bad string already got,
// naming the role and the offending value exactly the same way.
func applyColorOverrides(base Theme, colors map[string]json.RawMessage, path string) (Theme, []string) {
	if len(colors) == 0 {
		return base, nil
	}

	merged := make(map[Role]lipgloss.Color, len(base.Colors))
	for r, c := range base.Colors {
		merged[r] = c
	}

	keys := make([]string, 0, len(colors))
	for k := range colors {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var notices []string
	for _, key := range keys {
		text, isString := rawValueText(colors[key])
		display := text
		if isString {
			display = fmt.Sprintf("%q", text)
		}
		switch {
		case key == "":
			notices = append(notices, fmt.Sprintf("theme config at %s has a colour entry with no role name (value %s); ignoring it", path, display))
		case !isKnownRole(key):
			notices = append(notices, fmt.Sprintf("theme config at %s names unknown role %q; ignoring it", path, key))
		case !isString || !hexColor.MatchString(text):
			notices = append(notices, fmt.Sprintf("theme config at %s role %q has a value that is not a colour (%s, want #RRGGBB); ignoring it", path, key, display))
		default:
			merged[Role(key)] = lipgloss.Color(text)
		}
	}

	base.Colors = merged
	return base, notices
}

// rawValueText decodes raw as a JSON string, returning (value, true) when
// it is one. Any other JSON shape -- number, object, array, bool, null --
// is not a colour no matter what it contains, so it is reported back as
// its literal JSON text (e.g. "42", `{"nested":"obj"}`) and isString is
// false, letting applyColorOverrides fold it into the same "not a
// colour" notice a wrong-shaped string already gets rather than a
// separate type-error path.
func rawValueText(raw json.RawMessage) (value string, isString bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	return strings.TrimSpace(string(raw)), false
}

// ConfigPath returns where a user's theme preference lives.
//
// The product renamed agent-tui -> the Estate (decisions/0006), but this
// path never moved: it was still writing "agent-tui/theme.json" under
// os.UserConfigDir(), and on the host this was checked against (agent-tui#878),
// that directory holds a real, current preference --
// "$HOME/Library/Application Support/agent-tui/theme.json" containing
// {"theme": "signal-dark", "colors": null}, last modified before this fix.
// Silently switching the path segment to "estate" with no fallback would have
// made that preference vanish with no error: the next launch would find
// nothing at the new path and fall back to Default, which looks identical to
// "never configured" -- the exact failure this repo's own CLAUDE.md names as
// its most common defect shape ("an instrument that cannot see a thing looks
// exactly like the thing being absent").
//
// Resolution order:
//
//  1. $ESTATE_THEME_CONFIG, if set -- the new-named override. Wins over the
//     legacy override below if both happen to be set, since it is the more
//     specific, more recently stated instruction.
//  2. $AGENT_TUI_THEME_CONFIG, if set -- the pre-rename override name, kept
//     working indefinitely; something may already set it (agent-tui#876).
//  3. Otherwise, resolve two candidate paths under os.UserConfigDir() (per-OS
//     convention -- $HOME/Library/Application Support on macOS,
//     $XDG_CONFIG_HOME-or-$HOME/.config on other Unix): "estate/theme.json"
//     (new) and "agent-tui/theme.json" (legacy). If the new one exists, return it --
//     once a file lives at the new name, by any means (including a prior
//     Save from this same build), it is authoritative and the legacy file is
//     left untouched. Otherwise, if the legacy file exists, return IT --
//     Load then reads the operator's real preference from where it actually
//     is, and a subsequent Save (e.g. picking a theme in the UI) writes back
//     to that same legacy path, updating it in place rather than creating a
//     second, diverging copy. This is a deliberate choice over migrating on
//     first read: it never deletes or duplicates the operator's file, and an
//     older build (still hardcoding "agent-tui/theme.json") keeps reading
//     and writing the same file with no coordination needed. The cost is
//     that the file keeps its legacy directory name until something writes
//     to the new one directly (e.g. a fresh install with no legacy file to
//     find). If neither exists, return the new path, so a first-ever Save
//     starts the operator on the new name.
//
// Existence is checked with plain existence (os.Stat succeeding), not mtime:
// a file that exists at the new path is authoritative regardless of which
// file was touched more recently, avoiding any dependency on clock skew
// between the two Stat calls. A legacy file that exists but is unreadable
// (e.g. permission-denied) still counts as "exists" here -- ConfigPath only
// decides WHICH path to hand back; Load is what turns an unreadable file
// into its own honest notice rather than a silent Default.
func ConfigPath() string {
	if p := os.Getenv("ESTATE_THEME_CONFIG"); p != "" {
		return p
	}
	if p := os.Getenv("AGENT_TUI_THEME_CONFIG"); p != "" {
		return p
	}

	dir, err := userConfigDir()
	if err != nil {
		dir = "."
	}
	return resolveConfigPath(dir)
}

// userConfigDir is os.UserConfigDir, indirected so config_test.go can point
// ConfigPath's resolution at a temp directory instead of the operator's real
// "$HOME/Library/Application Support" -- that directory holds this operator's
// actual theme.json (agent-tui#878's own finding), so a test asserting
// fallback behaviour must never read or write it.
var userConfigDir = os.UserConfigDir

// resolveConfigPath implements ConfigPath's step 3 (new-vs-legacy fallback)
// against an already-resolved base dir, split out so it's testable without
// also stubbing the env-var checks above it.
func resolveConfigPath(dir string) string {
	newPath := filepath.Join(dir, "estate", "theme.json")
	legacyPath := filepath.Join(dir, "agent-tui", "theme.json")

	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath
	}
	return newPath
}

// Load reads path and resolves a Theme plus a notice to show the user, if
// any. Agent-tui#27 acceptance item 3 fixes three outcomes for the
// "theme" field -- "an undeterminable preference is never silently
// treated as a valid one":
//
//   - no file at path: Default, empty notice. Renders exactly as it did
//     before this mechanism existed -- a missing config is not an error.
//   - file exists but isn't readable or isn't valid JSON: Default, and a
//     non-empty notice the caller MUST render somewhere visible. A
//     malformed preference is never silently treated as "use the default
//     and say nothing" -- that would be indistinguishable from the file
//     having named the default on purpose.
//   - file exists, is valid JSON, but names a theme ID this build doesn't
//     ship: Default, non-empty notice, same reasoning.
//   - file exists, is valid JSON, and names a theme this build ships: that
//     theme, empty notice (before any "colors" override is applied).
//
// Agent-tui#34 adds a fourth input, the optional "colors" map, resolved
// AFTER the theme above: applyColorOverrides layers it onto whichever
// theme the four cases above already produced (Default, or the named
// theme) -- see its own doc comment for the precedence this implements
// and the three colour-entry failure modes it gives their own notices.
// Every notice this call produces -- a bad "theme" value, and each bad
// "colors" entry -- is joined into the single notice string this
// function has always returned; the caller (cmd/agent-tui) renders
// whatever it gets as one line per agent-tui#27's existing wiring, unchanged.
func Load(path string) (Theme, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default, ""
		}
		return Default, fmt.Sprintf("theme config at %s unreadable (%s); using default theme %q", path, err, Default.ID)
	}

	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default, fmt.Sprintf("theme config at %s is malformed (%s); using default theme %q", path, err, Default.ID)
	}

	var notices []string
	base := Default
	if cfg.Theme != "" {
		t, ok := ByID(cfg.Theme)
		if !ok {
			notices = append(notices, fmt.Sprintf("unknown theme %q in %s; using default theme %q", cfg.Theme, path, Default.ID))
		} else {
			base = t
		}
	}

	th, colorNotices := applyColorOverrides(base, cfg.Colors, path)
	notices = append(notices, colorNotices...)

	return th, strings.Join(notices, "; ")
}

// Save persists id as the user's theme preference at path, creating parent
// directories as needed. Not called by any render path today (agent-tui#27
// ships the mechanism, not a picker UI that writes it) -- it exists so the
// round-trip (write, then Load reads it back) is provable in a test rather
// than asserted only in prose, and so a future in-app picker has something
// to call rather than reinventing the file format.
func Save(path, id string) error {
	if _, ok := ByID(id); !ok {
		return fmt.Errorf("theme: unknown theme %q", id)
	}
	data, err := json.MarshalIndent(config{Theme: id}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
