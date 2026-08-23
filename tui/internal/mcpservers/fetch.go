package mcpservers

// Fetcher retrieves the current server list -- the adapter seam
// (AGENTS.md) Model is built around. NewFetcher below is the one real
// implementation this repo ships; every test in this package builds a
// fake instead.
type Fetcher func() ([]Server, error)

// NewFetcher composes Load and WithReachability into the one Fetcher
// cmd/estate would wire in for a real ~/.claude.json -- e.g.
// mcpservers.NewFetcher(theme.ConfigDir()+"/.claude.json", cwd,
// exec.LookPath). lookPath == nil is a valid, silent "no reachability
// check" default (WithReachability's own doc comment).
func NewFetcher(configPath, projectDir string, lookPath LookPath) Fetcher {
	return func() ([]Server, error) {
		servers, err := Load(configPath, projectDir)
		if err != nil {
			return nil, err
		}
		return WithReachability(servers, lookPath), nil
	}
}
