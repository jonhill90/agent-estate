package knowledgeindex

import (
	"encoding/json"
	"fmt"
	"os"
)

// Fetcher retrieves the current compiled index -- the adapter seam
// (AGENTS.md) Model is built around. NewFetcher below is the one real
// implementation this repo ships; every test in this package builds a
// fake instead.
type Fetcher func() (Result, error)

// NewFetcher builds a Fetcher over a plain read of path -- the JSON file
// `estate knowledge` writes (src/estate's own DefaultOutputPath, by
// default ~/.local/state/agent-estate/knowledge/index.json). path=="" is
// a distinct, visible error ("not configured"), never an empty Result
// indistinguishable from "the index has zero items".
func NewFetcher(path string) Fetcher {
	return func() (Result, error) { return Load(path) }
}

// Load reads and parses path. A file that has never been generated (the
// common first-run case: nobody has run `estate knowledge` yet) is a
// real, visible error here -- "not generated yet" is a different fact
// from "generated with zero items" and this package never blurs the two.
func Load(path string) (Result, error) {
	if path == "" {
		return Result{}, fmt.Errorf("no compiled-index path configured")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, fmt.Errorf("%s does not exist yet -- run `estate knowledge` to generate it", path)
		}
		return Result{}, fmt.Errorf("read %s: %w", path, err)
	}
	var res Result
	if err := json.Unmarshal(data, &res); err != nil {
		return Result{}, fmt.Errorf("%s is not a valid compiled index: %w", path, err)
	}
	return res, nil
}
