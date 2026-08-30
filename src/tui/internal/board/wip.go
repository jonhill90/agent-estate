package board

import "sort"

// SessionCapacity is the WIP limit per tmux SESSION, not per estate.
// agent-tui#6: "two workers per session is a WIP limit of 2 ... the estate
// now runs two sessions (agent-supervisor, agent-tui), so capacity is
// per-session, not global." Measured against the live lanes table while
// building this board, the estate actually had more than two distinct
// session prefixes running at once (ad-hoc review/worktree sessions
// alongside the two long-lived ones) -- WIP is grouped by whatever session
// prefix a lane's own name carries, not a hardcoded pair, so that finding
// doesn't quietly go stale the way the "two sessions" framing already has.
const SessionCapacity = 2

// WIP is one session's in-progress count against its capacity.
type WIP struct {
	Session      string
	InProgress   int
	Capacity     int
	OverCapacity bool
}

// ComputeWIP groups every InProgress card by Session and compares each
// group's count to SessionCapacity. A card with no Session (Backlog, or a
// ledger row missing its lane) is not counted anywhere -- WIP is about
// lanes actually holding work, not cards in general.
func ComputeWIP(cards []Card) []WIP {
	counts := map[string]int{}
	for _, c := range cards {
		if c.Column != InProgress || c.Session == "" {
			continue
		}
		counts[c.Session]++
	}
	out := make([]WIP, 0, len(counts))
	for session, n := range counts {
		out = append(out, WIP{
			Session:      session,
			InProgress:   n,
			Capacity:     SessionCapacity,
			OverCapacity: n > SessionCapacity,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Session < out[j].Session })
	return out
}
