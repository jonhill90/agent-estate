package library

// Fetcher retrieves the current list for one view+filter combination -- the
// adapter seam (AGENTS.md) Model's list is built around. cmd/keelson/
// library.go is the one real implementation this repo ships; every test in
// this package builds a fake instead.
type Fetcher func(view View, weight, status string) ([]ItemRow, error)

// DetailLoader retrieves one item's full record -- called only when a
// caller actually opens that item (this package's own progressive-
// disclosure constraint; see the package doc comment).
type DetailLoader func(id string) (ItemDetail, error)

// CountFetcher retrieves possibility_count -- refetched alongside the list
// (Model.Init/refreshMsg), shown regardless of which View is active.
type CountFetcher func() (int, error)
