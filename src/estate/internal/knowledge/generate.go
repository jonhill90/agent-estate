package knowledge

import "time"

// Generate runs all five sources and assembles one Result. Each source
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

	docsRes, docsItems := repoDocsSource(cfg.RepoRoot)
	res.Sources = append(res.Sources, docsRes)
	res.Items = append(res.Items, docsItems...)

	addSourceTag(res.Items)

	return res
}

// addSourceTag appends "source:<Item.Source>" to every item's own
// StructuralTags, in place -- agent-estate#1069. Every one of the five
// readers above already sets Item.Source to its own family name
// ("github-stars", "repo-docs", "corpus-directive", ...); this is the one
// place all five converge before Generate returns, so it is the one place
// that needs to know about the filter rather than each source file
// duplicating the same one-line append. The tag composes with the
// existing exact-tag filter (extractTagFilters/itemHasAllTags in
// query.go) for free -- it is a key:value structural tag exactly like
// kind:directive already is, so `source:repo-docs <question>` filters the
// same way `kind:directive <question>` already does. This never touches
// or removes the colon-less tags a source already carries (`repo-docs`,
// `github-stars`, `AGENTS.md`) -- it only adds one more.
func addSourceTag(items []Item) {
	for i := range items {
		items[i].StructuralTags = append(items[i].StructuralTags, "source:"+items[i].Source)
	}
}
