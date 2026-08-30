package admin

import "github.com/jonhill90/agent-estate/src/tui/internal/theme"

// fetchSettings reads this application's own persisted configuration --
// theme.Load's own file, the one setting agent-tui has today. theme.Load
// already implements the "no file yet" / "unreadable" / "malformed" /
// "unknown theme id" cases as Default-plus-notice (its own doc comment),
// so this never returns an error itself -- a missing or bad theme config
// is not a fetch failure, it's theme.Load's own honestly-reported
// default, which SettingsErr would otherwise make look like a worse
// problem than it is.
func fetchSettings(configPath string) ([]Setting, error) {
	th, notice := theme.Load(configPath)
	settings := []Setting{{Name: "theme", Value: th.Name}}
	if notice != "" {
		settings = append(settings, Setting{Name: "theme notice", Value: notice})
	}
	return settings, nil
}
