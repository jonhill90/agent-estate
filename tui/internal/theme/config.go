package theme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// config is the on-disk shape of a user's theme preference -- the smallest
// file that can carry "which theme": #27's per-user half (agent-tui#25)
// asks for a persisted preference, not a designer's config format, so this
// has exactly one field.
type config struct {
	Theme string `json:"theme"`
}

// ConfigPath returns where a user's theme preference lives: the
// $AGENT_TUI_THEME_CONFIG override if set, else
// $XDG_CONFIG_HOME-or-os.UserConfigDir()/agent-tui/theme.json -- the same
// per-user, per-OS convention config packages typically use, so this is not
// a second, agent-tui-specific config directory scheme.
func ConfigPath() string {
	if p := os.Getenv("AGENT_TUI_THEME_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "agent-tui", "theme.json")
}

// Load reads path and resolves a Theme plus a notice to show the user, if
// any. Agent-tui#27 acceptance item 3 fixes exactly three outcomes -- "an
// undeterminable preference is never silently treated as a valid one":
//
//   - no file at path: Default, empty notice. Renders exactly as it did
//     before this mechanism existed -- a missing config is not an error.
//   - file exists but isn't readable, isn't valid JSON, or names a theme ID
//     this build doesn't ship: Default, and a non-empty notice the caller
//     MUST render somewhere visible. A malformed preference is never
//     silently treated as "use the default and say nothing" -- that would
//     be indistinguishable from the file having named the default on
//     purpose.
//   - file exists, is valid JSON, and names a theme this build ships: that
//     theme, empty notice.
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
	if cfg.Theme == "" {
		// Valid JSON that simply doesn't set "theme" -- same as no
		// preference expressed at all, not a malformed file.
		return Default, ""
	}

	t, ok := ByID(cfg.Theme)
	if !ok {
		return Default, fmt.Sprintf("unknown theme %q in %s; using default theme %q", cfg.Theme, path, Default.ID)
	}
	return t, ""
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
