package knowledge

// Fetcher retrieves the current index -- the adapter seam (AGENTS.md)
// Model's list is built around. NewFetcher below is the one real
// implementation this repo ships; every test in this package builds a
// fake instead.
type Fetcher func() ([]IndexEntry, error)

// NewFetcher builds a Fetcher over LoadIndex(vaultDir) -- the one-file
// read the list view uses. vaultDir is a plain string, not
// os.Getenv("AGENT_MEMORY_VAULT") read here: this package takes an
// explicit path the same way every other adapter in this module does
// (internal/mcpservers.NewFetcher takes configPath, not a getenv call);
// cmd/keelson resolves the environment variable once and passes the
// result in, empty string and all -- LoadIndex's own doc comment is what
// turns an empty vaultDir into the visible "not set" error this view
// requires.
func NewFetcher(vaultDir string) Fetcher {
	return func() ([]IndexEntry, error) { return LoadIndex(vaultDir) }
}

// FactLoader retrieves one fact's full body -- called only when a caller
// actually opens that fact (this package's own progressive-disclosure
// constraint; see the package doc comment).
type FactLoader func(slug string) (Fact, error)

// NewFactLoader builds a FactLoader over LoadFact(vaultDir, slug).
func NewFactLoader(vaultDir string) FactLoader {
	return func(slug string) (Fact, error) { return LoadFact(vaultDir, slug) }
}
