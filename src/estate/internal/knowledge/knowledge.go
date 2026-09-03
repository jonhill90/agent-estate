// Package knowledge compiles an index over knowledge that already exists
// somewhere else.
//
// IT IS DERIVED, NEVER AUTHORITATIVE. Every file it writes is regenerable
// from its sources, and deleting the whole index loses nothing. Nothing here
// mutates a source: stars, the corpus, the vaults and the research tree are
// read-only inputs.
//
// WHY A COMPILED ARTIFACT AND NOT A HAND-KEPT DOCUMENT. The operator's own
// Second Brain agent guide is hand-maintained and claims 245 files against
// 641 actual -- 2.6x stale. A document that must be edited by hand to stay
// true will drift, and a stale index is worse than none because it is
// believed. So every file carries generated_at and a staleness rule telling
// the reader when to stop trusting it, and the answer to drift is to
// regenerate rather than to edit.
//
// CONVENTIONS ARE HIS, NOT NEW ONES:
//   - 14-character YYYYMMDDHHmmss ids, the pattern his Notebook-MCP models.py
//     pins with the comment "prevents agent collision".
//   - two tag classes, as his Second Brain guide defines them: synaptic tags
//     carry a hash and express association; structural tags are bare and
//     express organisation.
//   - a permalink field, so an item has a stable address independent of the
//     path it currently lives at.
package knowledge

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// StaleAfter is how long a generated file may be trusted before the reader
// should regenerate. Stated in the file itself, not only here.
const StaleAfter = 7 * 24 * time.Hour

// Item is one indexed thing, whatever source it came from.
type Item struct {
	// ID is 14 characters, YYYYMMDDHHmmss.
	ID    string
	Title string
	// Detail is one line. The index is a pointer, not a copy: anything
	// longer belongs in the source, which is why nothing here stores bodies.
	Detail string
	// Permalink is the stable address of the thing itself.
	Permalink string
	// Synaptic tags express association and are rendered with a hash.
	Synaptic []string
	// Structural tags express organisation and are rendered bare.
	Structural []string
	// Signal is the source's own dated judgement, if it has one -- a star's
	// date, a parameter's weight. Empty when the source offers none.
	Signal string
}

// Source is one body of knowledge being indexed.
type Source struct {
	// Slug is the file name under sources/.
	Slug string
	// Name is what a human calls it.
	Name string
	// Origin says exactly where the data came from, so a reader can go and
	// check rather than trusting this file.
	Origin string
	Items  []Item
	// Note records anything the reader must know to judge the contents --
	// including what is missing.
	Note string
}

// IDAt formats an id in his convention. Uniqueness is per-second, so a
// compiler emitting many items in one second must offset them; NewIDs does
// that rather than leaving collisions to chance.
func IDAt(t time.Time) string { return t.UTC().Format("20060102150405") }

// NewIDs returns n ids that are unique and ordered, starting at t.
//
// The comment his models.py carries on this pattern is "prevents agent
// collision"; a compiler that emitted the same second twice would reintroduce
// exactly what the convention exists to stop.
func NewIDs(t time.Time, n int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = IDAt(t.Add(time.Duration(i) * time.Second))
	}
	return out
}

// header is the front matter every generated file carries.
func header(title, permalink string, generatedAt time.Time, origin string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", title)
	fmt.Fprintf(&b, "permalink: %s\n", permalink)
	fmt.Fprintf(&b, "generated_at: %s\n", generatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "stale_after: %s\n", generatedAt.UTC().Add(StaleAfter).Format(time.RFC3339))
	fmt.Fprintf(&b, "generated_by: estate knowledge\n")
	fmt.Fprintf(&b, "source: %s\n", origin)
	b.WriteString("---\n\n")
	b.WriteString("> **This file is compiled. Do not edit it — regenerate it.**\n")
	fmt.Fprintf(&b, "> Generated %s from %s.\n", generatedAt.UTC().Format(time.RFC3339), origin)
	fmt.Fprintf(&b, "> **Distrust it after %s** and run `estate knowledge` again.\n",
		generatedAt.UTC().Add(StaleAfter).Format(time.RFC3339))
	b.WriteString("> A hand-edited index drifts silently; this one is meant to be thrown away.\n\n")
	return b.String()
}

// tags renders both classes his guide defines: synaptic with a hash for
// association, structural bare for organisation.
func tags(it Item) string {
	var parts []string
	for _, s := range it.Synaptic {
		parts = append(parts, "#"+s)
	}
	parts = append(parts, it.Structural...)
	return strings.Join(parts, " ")
}

// SourceIndex renders the middle tier: every item in one source, one line
// each. Bodies are never copied -- the permalink is the pointer.
func SourceIndex(s Source, generatedAt time.Time) string {
	var b strings.Builder
	b.WriteString(header(s.Name, "knowledge/"+s.Slug, generatedAt, s.Origin))
	if s.Note != "" {
		fmt.Fprintf(&b, "%s\n\n", s.Note)
	}
	fmt.Fprintf(&b, "%d items.\n\n", len(s.Items))
	b.WriteString("| id | item | signal | tags |\n|---|---|---|---|\n")
	for _, it := range s.Items {
		title := it.Title
		if it.Permalink != "" {
			title = fmt.Sprintf("[%s](%s)", it.Title, it.Permalink)
		}
		detail := strings.ReplaceAll(it.Detail, "|", "\\|")
		if detail != "" {
			title += "<br>" + detail
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", it.ID, title, it.Signal, tags(it))
	}
	return b.String()
}

// TopIndex renders the small top tier: one line per source, and nothing else.
//
// An agent must never read the whole index to find one item
// (docs_structure=progressive_disclosure, hard), so this tier stays a
// directory and refuses to grow into a catalogue.
func TopIndex(sources []Source, generatedAt time.Time) string {
	sorted := append([]Source(nil), sources...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })

	var b strings.Builder
	b.WriteString(header("Knowledge index", "knowledge/index", generatedAt, "several sources; see each row"))
	b.WriteString("Every row is a source that already exists elsewhere. This index is\n")
	b.WriteString("**derived**: delete the whole thing and nothing is lost.\n\n")
	b.WriteString("| source | items | where it really lives |\n|---|---|---|\n")
	total := 0
	for _, s := range sorted {
		total += len(s.Items)
		fmt.Fprintf(&b, "| [%s](sources/%s.md) | %d | %s |\n", s.Name, s.Slug, len(s.Items), s.Origin)
	}
	fmt.Fprintf(&b, "\n%d items across %d source(s).\n", total, len(sorted))
	return b.String()
}
