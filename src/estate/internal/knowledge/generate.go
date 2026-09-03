package knowledge

import "time"

// Generate runs all four sources and assembles one Result. Each source
// is independent: one failing never stops the others, and never removes
// its own line from Sources -- see this package's own doc comment on
// honest absence. now is injected so tests get a fixed GeneratedAt/id
// base rather than a moving one.
func Generate(cfg Config, now time.Time) Result {
	clock := newIDClock(now)

	res := Result{
		GeneratedAt:   now.UTC(),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
	}

	starsRes, starsItems := starsSource(cfg.RunGH, clock)
	res.Sources = append(res.Sources, starsRes)
	res.Items = append(res.Items, starsItems...)

	vaultRes, vaultItems := vaultSource(cfg.VaultDir, clock)
	res.Sources = append(res.Sources, vaultRes)
	res.Items = append(res.Items, vaultItems...)

	corpusRes, corpusItems := corpusSource(cfg.CorpusDBPath, clock)
	res.Sources = append(res.Sources, corpusRes)
	res.Items = append(res.Items, corpusItems...)

	loopsRes, loopsItems := loopsSource(cfg.LoopsResearch, clock)
	res.Sources = append(res.Sources, loopsRes)
	res.Items = append(res.Items, loopsItems...)

	return res
}
