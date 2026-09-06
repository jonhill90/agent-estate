package knowledge

import (
	"fmt"
	"sort"

	"github.com/jonhill90/agent-estate/estate/internal/catalogue"
)

// catalogueSource reads internal/catalogue's private source register
// (agent-estate#1139 lane B) and returns one Item per registered entry.
// Like every other source in this package, it only ever reads -- it
// never registers, refreshes, or writes anything into the register
// itself; that is cmd/sourcecatalogue's own job.
//
// registerDir empty, missing, or holding zero entries are all reported
// as an honestly non-OK or empty-but-OK SourceResult -- never a fatal
// Generate error -- the same treatment loopsSource gives an absent
// Loops-Research directory: most machines that run `estate knowledge`
// have never registered anything into this register at all.
//
// Item content never carries a registered source's own extracted text --
// that stays in the register's private per-entry cache
// (RegisterEntry.ExtractionCachePath). Tier2/Tier3 here are built only
// from the register's own structural metadata (kind, locator, authority,
// scope, status, ...), the same declared-fields-only boundary
// catalogue.Refresh itself enforces on drift.
func catalogueSource(registerDir string) (SourceResult, []Item) {
	res := SourceResult{Name: "catalogue-source"}
	if registerDir == "" {
		res.Reason = "no catalogue register path configured"
		return res, nil
	}

	reg, err := catalogue.LoadRegister(registerDir)
	if err != nil {
		res.Reason = fmt.Sprintf("cannot load register at %s: %v", registerDir, err)
		return res, nil
	}

	entries := reg.List()
	if len(entries) == 0 {
		res.OK = true
		res.Reason = "register at " + registerDir + " exists but holds zero entries"
		return res, nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })

	publishable, basis := classify("catalogue-source")
	var items []Item
	for _, e := range entries {
		permalink := "catalogue:" + e.ID
		items = append(items, Item{
			ID:        itemID(permalink),
			Source:    "catalogue-source",
			Permalink: permalink,
			StructuralTags: []string{
				"catalogue-source",
				"kind:" + e.Kind,
				"status:" + string(e.Status),
			},
			Tier1: truncate(nonEmptyOr(e.WhyIndexed, e.ID), 200),
			Tier2: fmt.Sprintf(
				"kind=%s locator=%s authority=%s scope=%s access=%s review_state=%s drift_status=%s",
				e.Kind, e.Locator, e.Authority, e.Scope, e.Access, e.ReviewState, e.Status),
			Tier3: fmt.Sprintf(
				"provenance: %s\nattribution: %s\nowner: %s\nfreshness: %s\nextraction_status: %s\nobserved_revision: %s\nlast_refreshed_at: %s\nregistered_at: %s",
				e.Provenance, e.Attribution, e.Owner, e.Freshness, e.ExtractionStatus, e.ObservedRevision, e.LastRefreshedAt, e.RegisteredAt),
			Publishable:  publishable,
			PublishBasis: basis,
		})
	}

	res.OK = true
	res.Count = len(items)
	return res, items
}

func nonEmptyOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
