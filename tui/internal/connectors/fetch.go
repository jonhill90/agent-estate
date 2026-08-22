package connectors

// Fetcher retrieves the current connection/model list -- the adapter seam
// (AGENTS.md) Model is built around, the same (T, error)-returning shape
// internal/mcpservers.Fetcher and internal/skills.Fetcher already use, even
// though Load itself cannot fail (every read degrades to "not configured"
// rather than an error -- see Load's own doc comment): keeping the shape
// consistent means every pane's fetchResultMsg/View error-rendering path
// works identically, and a fake Fetcher in a test can still simulate a
// failure if one is ever needed.
type Fetcher func() ([]Connection, []AvailableModel, error)

// NewFetcher composes Load into the one Fetcher cmd/keelson would wire in
// for a real environment -- e.g.
// connectors.NewFetcher(connectors.Paths{ClaudeConfig: home+"/.claude.json",
// CodexConfig: home+"/.codex/config.toml", CodexModelsCache:
// home+"/.codex/models_cache.json", PiSettings: home+"/.pi/agent/settings.json"}).
func NewFetcher(paths Paths) Fetcher {
	return func() ([]Connection, []AvailableModel, error) {
		conns, models := Load(paths)
		return conns, models, nil
	}
}
