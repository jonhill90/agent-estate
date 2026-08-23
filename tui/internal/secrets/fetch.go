package secrets

// Fetcher retrieves the current inventory -- the one adapter seam this
// package's Model depends on (AGENTS.md's adapter discipline). cmd/estate
// builds the real implementation over a path resolved from a flag or the
// environment; every test in this package builds a fake instead.
type Fetcher func() (Inventory, error)

// NewFetcher builds a Fetcher over Load(schemaPath) -- the same shape
// apidocs.NewFetcher(specPath) already uses, with cmd/estate resolving
// the environment once.
func NewFetcher(schemaPath string) Fetcher {
	return func() (Inventory, error) { return Load(schemaPath) }
}
