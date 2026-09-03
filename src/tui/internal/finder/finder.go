// Package finder is a Telescope-style fuzzy jump list: type part of a name,
// see what matches, press enter to go there.
//
// WHY IT EXISTS. A leader menu answers "what can I press right now". It does
// not answer "where is the thing called roughly X", and it cannot: a menu has
// to fit on screen, so it can only show a curated handful. The estate has
// twenty-odd routes and will have far more agents, tasks and notes than any
// menu can list. Telescope's answer is to stop listing and start filtering.
//
// The matching is SUBSEQUENCE matching, which is what makes it feel fuzzy:
// "knw" finds "knowledge" because k, n and w appear in that order. Scoring
// then prefers matches that are early, contiguous, and on word boundaries, so
// "chat" ranks the Chat route above a note that merely contains those letters
// scattered through it.
//
// Sources are supplied by the caller as Items. The finder never reads a
// ledger, a repo or a filesystem: adding agents, tasks or notes to the jump
// list is a new supplier at the call site, never a change here.
package finder

import (
	"sort"
	"strings"
	"unicode"
)

// Kind labels where an item came from, so the list can show "route" beside a
// pane and "task" beside an issue without the finder knowing what either is.
type Kind string

// Item is one jumpable thing.
type Item struct {
	Kind  Kind
	ID    string
	Label string
	// Detail is optional context shown dimmed after the label.
	Detail string
}

// Match is an Item with its score and the label positions that matched, so a
// renderer can highlight exactly the characters the query hit.
type Match struct {
	Item      Item
	Score     int
	Positions []int
}

// Score returns whether query is a subsequence of s and how good the match
// is. A higher score is better. An empty query matches everything at zero.
//
// Case-insensitive, because nobody types capitals into a jump list.
func Score(query, s string) (int, []int, bool) {
	if query == "" {
		return 0, nil, true
	}
	q := []rune(strings.ToLower(query))
	target := []rune(s)
	lower := []rune(strings.ToLower(s))

	var pos []int
	score, qi, lastHit := 0, 0, -2
	for i := 0; i < len(lower) && qi < len(q); i++ {
		if lower[i] != q[qi] {
			continue
		}
		// Contiguous runs read as one word to a human, so weight them.
		if i == lastHit+1 {
			score += 5
		}
		// A match at a word boundary is what the user almost always means.
		if i == 0 || target[i-1] == ' ' || target[i-1] == '-' || target[i-1] == '/' ||
			(unicode.IsUpper(target[i]) && unicode.IsLower(target[i-1])) {
			score += 8
		}
		// Earlier is better, mildly.
		score += max(0, 10-i)
		pos = append(pos, i)
		lastHit = i
		qi++
	}
	if qi < len(q) {
		return 0, nil, false
	}
	// A shorter label matching the same query is the more specific answer.
	score += max(0, 20-len(target))
	return score, pos, true
}

// Filter returns the items matching query, best first. Ties break on label so
// the order never jitters between identical queries -- a list that reshuffles
// under a stable query is unusable, however good the ranking.
func Filter(items []Item, query string) []Match {
	var out []Match
	for _, it := range items {
		hay := it.Label
		if it.Detail != "" {
			hay = it.Label + " " + it.Detail
		}
		if sc, pos, ok := Score(query, hay); ok {
			out = append(out, Match{Item: it, Score: sc, Positions: pos})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Item.Label < out[j].Item.Label
	})
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Model is the finder's own state: the query, the matches, and which one is
// selected.
type Model struct {
	Query    string
	Items    []Item
	Matches  []Match
	Selected int
	// Height is how many rows of results to show.
	Height int
}

// New builds a finder over items.
func New(items []Item) Model {
	m := Model{Items: items, Height: 8}
	return m.refilter()
}

func (m Model) refilter() Model {
	m.Matches = Filter(m.Items, m.Query)
	if m.Selected >= len(m.Matches) {
		m.Selected = maxInt0(len(m.Matches) - 1)
	}
	return m
}

func maxInt0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// Type appends a rune to the query.
func (m Model) Type(r rune) Model { m.Query += string(r); m.Selected = 0; return m.refilter() }

// Backspace removes the last rune. On an empty query it is a no-op rather
// than an error: backspacing past the start is something everyone does.
func (m Model) Backspace() Model {
	if m.Query == "" {
		return m
	}
	r := []rune(m.Query)
	m.Query = string(r[:len(r)-1])
	m.Selected = 0
	return m.refilter()
}

// Move shifts the selection, clamped. It does not wrap: wrapping in a
// filtered list moves you somewhere you were not looking.
func (m Model) Move(delta int) Model {
	if len(m.Matches) == 0 {
		m.Selected = 0
		return m
	}
	m.Selected += delta
	if m.Selected < 0 {
		m.Selected = 0
	}
	if m.Selected >= len(m.Matches) {
		m.Selected = len(m.Matches) - 1
	}
	return m
}

// Choice returns the selected item, and whether there is one. No matches
// means no choice -- pressing enter on an empty list must do nothing rather
// than jump somewhere arbitrary.
func (m Model) Choice() (Item, bool) {
	if len(m.Matches) == 0 || m.Selected < 0 || m.Selected >= len(m.Matches) {
		return Item{}, false
	}
	return m.Matches[m.Selected].Item, true
}
