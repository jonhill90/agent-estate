// agent-tui#20's explicit ask: "build behind an adapter seam so a layout
// can be enabled, disabled or removed" -- the same numbered, live-against-
// real-data cycler internal/lane.Variants and internal/rail's readings
// already prove works, applied a third time. Layouts is DATA, nothing
// switches on a layout ID anywhere outside this package, and adding or
// dropping a candidate is a line here (lane.Variants's own framing).
//
// The issue names four options: [1] thread list + transcript, [2]
// multi-pane live tail, [3] unified activity feed, [4] focus + peripherals.
// Only two are structurally different renderers; the other two are
// readings of those two, not new layouts:
//
//   - [3] unified activity feed is [1] with one more thread in the list: a
//     synthetic "All" thread that interleaves every real thread's messages
//     chronologically, tagged with its source thread. It needs no new
//     selection model, no new key, no new render path -- listLayout
//     produces it for free by treating AggregateAll's result as Threads[0].
//     Building it as a *third* layout would mean two layouts sharing one
//     entire rendering function with a one-line difference in what feeds
//     it, which is exactly the "near-duplicate" the issue says to collapse.
//
//   - [4] focus + peripherals is [2] with one pane enlarged and the rest
//     shrunk to a compact strip -- a density state of the SAME multi-pane
//     renderer, not a different one. gridLayout's own focus toggle (model.go's
//     "f" key) is that state; splitting it into a fifth Layout entry would
//     make "focus on/off" and "which layout" two overlapping controls for
//     the same visual outcome, worse than one.
//
// That leaves two genuinely different renderers, same shape as
// internal/rail's own two-readings precedent (readings.go): listLayout
// (single focus, familiar, the unified feed folded in) and gridLayout (see
// every thread at once, focus folded in). Layouts[0] is the default -- the
// same "silence still yields something sane" rule readings.go and
// lane.Variants both already carry.
package chat

var Layouts = []layoutDef{listLayout, gridLayout}

type layoutDef struct {
	ID          string
	Name        string
	Description string
}

// listLayout: thread list on the left, the selected thread's transcript on
// the right -- Slack/Discord shape, one conversation read at a time. The
// list's first row is always the synthetic "All" thread (see AggregateAll
// in render.go), so the unified-feed reading is one keypress away, not a
// separate screen. Both columns are viewport-backed (model.go) so a list
// or a transcript taller than the pane scrolls rather than truncating --
// the agent-tui#29 defect this package is built not to repeat.
var listLayout = layoutDef{
	ID:          "list",
	Name:        "Thread list",
	Description: "sessions on the left, selected transcript on the right -- includes a synthetic \"All\" thread for the unified feed",
}

// gridLayout: every thread tiled at once, each pane a compact live tail --
// the direct answer to "see the agents chatting with each other" plural.
// A tile that cannot show every message says so (a "N more above" marker,
// render.go) rather than silently dropping the oldest lines; pressing "f"
// focuses the selected pane into the same scrollable single-thread view
// listLayout's transcript uses, which is how a tile's hidden history is
// actually reached -- layout [4]'s "focus + peripherals" reading of this
// same renderer.
var gridLayout = layoutDef{
	ID:          "grid",
	Name:        "Multi-pane",
	Description: "every thread tiled and live; \"f\" focuses one pane (scrollable) with the rest as a peripheral strip",
}
