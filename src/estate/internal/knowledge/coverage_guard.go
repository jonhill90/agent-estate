package knowledge

import "fmt"

// CoverageRegressions compares a freshly generated Result against the
// Result already written on disk and reports every source that went from
// readable to unreadable -- agent-estate#1123. `estate knowledge`
// regenerates unconditionally today: nothing stops an invocation from an
// environment that happens to lack a source (a cron, a launchd job, a
// shell that never sourced the profile, a container) from overwriting a
// healthy shared index with a degraded one, silently and at exit 0. The
// live incident this guards against: the vault-facts source read
// ok=true, count=109 in the index on disk, a regeneration ran with
// $AGENT_MEMORY_VAULT unset, and the shared index sat at ok=false,
// count=0 for roughly fifteen minutes before anyone noticed by hand.
//
// DEFINITION CHOSEN: a source's own OK flipping true -> false, never a
// bare item-count drop. The two are not the same test. Count drops
// happen on ordinary corpus churn -- a stale github-stars entry
// unstarred, a vault fact deleted, a doc section reworded away -- and a
// guard that fired on every such drop would be noise within a day,
// which is exactly the "warn so often it gets ignored" failure this
// exists to avoid repeating. A source going OK=true to OK=false is
// categorically different: it is not the source's own content changing,
// it is this package's ability to read the source at all failing, which
// is precisely the shape of the vault incident above.
//
// A source name present in prev but entirely absent from next (or vice
// versa) is not reported here -- Generate always runs the same five
// sources in the same order, so an absent name means prev or next is not
// actually one of this package's own Results (a hand-edited or foreign
// file), which is a different problem this function does not attempt to
// diagnose.
func CoverageRegressions(prev, next Result) []string {
	prevOK := make(map[string]SourceResult, len(prev.Sources))
	for _, s := range prev.Sources {
		prevOK[s.Name] = s
	}

	var regressions []string
	for _, n := range next.Sources {
		p, ok := prevOK[n.Name]
		if !ok {
			continue
		}
		if p.OK && !n.OK {
			regressions = append(regressions, fmt.Sprintf(
				"%s: ok=true (count=%d) -> ok=false (%s)", n.Name, p.Count, n.Reason))
		}
	}
	return regressions
}
