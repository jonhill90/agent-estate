// Package flow is agent-tui#64's live flow view -- work MOVING between
// states, not a static list. It reads the exact same already-derived
// board.Snapshot the board pane (internal/board) fetches: cmd/keelson wires
// literally the same board.Fetcher value into both panes, so this package
// is never a second reader of gh, the ledger, or live lanes -- it only
// re-projects board.Card (whose own Column already prefers gh/live-lane
// truth over the ledger's task_status wherever they disagree, per
// board.Derive's switch) into a pipeline reading, and adds a local,
// I/O-free animation tick so the pipeline visibly moves between fetches
// (#64: "it will seem like watching a company work," not a dashboard that
// only changes once a minute).
package flow

import (
	"sort"

	"github.com/jonhill90/keelson/internal/board"
)

// Stage is one node of the flow pipeline. Unlike board.Column (five
// columns tuned for a kanban board), Stage is ordered left-to-right the way
// work actually moves through the estate, and treats Blocked as a loop back
// into Working/Review rather than a fifth parallel column -- issue #64's
// own framing ("review -> fix -> re-review is a CYCLE").
type Stage int

const (
	StageQueued Stage = iota
	StageWorking
	StageReview
	StageBlocked
	StageDone
	stageCount
)

var stageLabel = [stageCount]string{
	StageQueued:  "queued",
	StageWorking: "working",
	StageReview:  "review",
	StageBlocked: "blocked",
	StageDone:    "done",
}

func (s Stage) String() string {
	if s < 0 || int(s) >= len(stageLabel) {
		return "unknown"
	}
	return stageLabel[s]
}

// Item is one board.Card re-read as a pipeline position. Terminal is only
// set once Stage == StageDone, and distinguishes "a PR closed this issue by
// merging" from "the issue closed with no merging PR" -- the honest split
// board.Card's own data supports. It is deliberately never "refused": this
// package has no signal (#401's reaper stamps are exactly the kind of
// ledger claim AGENTS.md says gh must override, and even gh's CLOSED state
// doesn't say why) that would let it assert a refusal rather than any other
// reason an issue closed without a merge.
type Item struct {
	Card     board.Card
	Stage    Stage
	Terminal string // "merged", "closed", or "" (Stage != StageDone)
}

// stageOf maps board.Card.Column (board.Derive's own gh-preferred
// projection, card.go) onto Stage -- never a second derivation from raw gh
// output. This is the one place the two vocabularies meet.
func stageOf(c board.Card) Stage {
	switch c.Column {
	case board.InProgress:
		return StageWorking
	case board.InReview:
		return StageReview
	case board.Blocked:
		return StageBlocked
	case board.Done:
		return StageDone
	default:
		return StageQueued
	}
}

// DeriveItems re-projects every card in snap onto its pipeline Stage.
// Pure, like board.Derive -- no I/O, safe to call on every render.
func DeriveItems(cards []board.Card) []Item {
	items := make([]Item, 0, len(cards))
	for _, c := range cards {
		it := Item{Card: c, Stage: stageOf(c)}
		if it.Stage == StageDone {
			if c.PRNumber != 0 {
				it.Terminal = "merged"
			} else {
				it.Terminal = "closed"
			}
		}
		items = append(items, it)
	}
	return items
}

// StageCounts tallies items by Stage, for the pipeline header's own
// per-node numbers.
func StageCounts(items []Item) [stageCount]int {
	var counts [stageCount]int
	for _, it := range items {
		counts[it.Stage]++
	}
	return counts
}

// InFlight returns Working/Review/Blocked items only -- the "actually
// moving right now" subset the body list shows by default (#64: a flow
// view is about motion, and Queued/Done are the two stages that, by
// definition, are not moving). Sorted oldest-in-current-stage first
// (largest Card.Age), so the item that has sat the longest -- #64's own
// example, #251 at 22 turns and 918 minutes -- always sorts to the top,
// exactly where a human scanning for a stuck item would look first.
func InFlight(items []Item) []Item {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if it.Stage == StageWorking || it.Stage == StageReview || it.Stage == StageBlocked {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Card.Age > out[j].Card.Age })
	return out
}
