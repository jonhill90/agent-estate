package apidocs

// Fetcher retrieves the current reference -- the one adapter seam this
// package's Model depends on (AGENTS.md's adapter discipline). cmd/keelson
// builds the real implementation over a path resolved from a flag or the
// environment; every test in this package builds a fake instead.
type Fetcher func() (Reference, error)

// NewFetcher builds a Fetcher over Load(specPath). specPath is an explicit
// string, not an os.Getenv read here -- the same shape
// knowledge.NewFetcher(vaultDir) and mcpservers.NewFetcher(configPath)
// already use, with cmd/keelson resolving the environment once.
func NewFetcher(specPath string) Fetcher {
	return func() (Reference, error) { return Load(specPath) }
}
