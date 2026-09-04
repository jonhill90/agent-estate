package knowledge

import "time"

// Generate runs all four sources and assembles one Result. Each source
// is independent: one failing never stops the others, and never removes
// its own line from Sources -- see this package's own doc comment on
// honest absence. now is injected so tests get a fixed GeneratedAt
// rather than a moving one; it no longer seeds an id clock -- Item.ID is
// derived from each item's own Permalink (see id.go's itemID), which is
// what makes it stable across two Generate calls over the same sources.
func Generate(cfg Config, now time.Time) Result {
	res := Result{
		GeneratedAt:   now.UTC(),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
	}

	starsRes, starsItems := starsSource(cfg.RunGH)
	res.Sources = append(res.Sources, starsRes)
	res.Items = append(res.Items, starsItems...)

	vaultRes, vaultItems := vaultSource(cfg.VaultDir)
	res.Sources = append(res.Sources, vaultRes)
	res.Items = append(res.Items, vaultItems...)

	corpusRes, corpusItems := corpusSource(cfg.CorpusDBPath)
	res.Sources = append(res.Sources, corpusRes)
	res.Items = append(res.Items, corpusItems...)

	loopsRes, loopsItems := loopsSource(cfg.LoopsResearch)
	res.Sources = append(res.Sources, loopsRes)
	res.Items = append(res.Items, loopsItems...)

	return res
}
