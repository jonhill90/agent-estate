// Package distill turns many corpus items that say the same thing into one
// durable fact, with the rest recorded as its evidence.
//
// Why this exists. The corpus captures every prompt, so a rule Jon states
// twenty times becomes twenty items and twenty notes. That is a transcript
// with good metadata, not memory -- his own recorded words: "the corpus is
// scraped transcripts plus context -- memory is more than that". Measured
// 2026-09-07: 3,070 parameter notes against 119 facts, and zero rows in the
// links table, so nothing had ever been merged or superseded.
//
// The one rule that shapes the design: distillation NEVER invents wording. A
// group's canonical statement is CHOSEN from the item bodies already in the
// group -- the clearest one Jon actually caused to be written -- and the rest
// are linked as evidence. Nothing here writes a sentence no item contains, so
// the output cannot put words in his mouth. Grouping is deterministic set
// similarity, not a model call, so a rerun over unchanged input produces the
// same groups.
package distill

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Item is one corpus item considered for distillation.
type Item struct {
	ID     string
	Kind   string
	Status string
	Body   string
	At     int64
	Prompt string
}

// Terms exposes the term extraction so callers that group by subject rather
// than by pairwise similarity score against the same vocabulary.
func Terms(body string) map[string]bool { return terms(body) }

// Group is a set of items judged to state the same rule.
type Group struct {
	// Key is a stable identity for the group, derived from its member ids, so
	// the same group re-proposes under the same identity on a later run.
	Key string
	// Canonical is the member chosen to state the rule. Always one of Members.
	Canonical Item
	// Members are every item in the group, Canonical included, oldest first.
	Members []Item
	// Score is the mean pairwise similarity, for reporting how tight the
	// group is. A caller may use it to order proposals by confidence.
	Score float64
}

// stop words carry no discriminating signal in this corpus. "corpus" and
// "vault" are here for the same reason "the" is: measured 2026-09-07, tagging
// on those two words swallowed 1,752 and 1,748 notes respectively, because
// nearly every item is ABOUT the corpus or the vault. A term that matches
// almost everything cannot separate anything.
var stop = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "but": true, "by": true, "can": true, "do": true, "does": true,
	"for": true, "from": true, "has": true, "have": true, "in": true, "is": true,
	"it": true, "its": true, "must": true, "no": true, "not": true, "of": true,
	"on": true, "or": true, "should": true, "so": true, "than": true, "that": true,
	"the": true, "their": true, "them": true, "then": true, "there": true,
	"they": true, "this": true, "to": true, "was": true, "were": true, "when": true,
	"which": true, "why": true, "will": true, "with": true, "you": true, "your": true,
	"corpus": true, "vault": true,
}

// terms reduces a body to its discriminating token set. Lowercased, split on
// non-letters, stop words dropped, a crude suffix strip so "recorded" and
// "record" agree, and anything shorter than three characters discarded.
func terms(body string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(body), func(r rune) bool {
		return r < 'a' || r > 'z'
	}) {
		if len(f) < 3 || stop[f] {
			continue
		}
		for _, suf := range []string{"ing", "ed", "es", "s"} {
			if len(f) > len(suf)+3 && strings.HasSuffix(f, suf) {
				f = f[:len(f)-len(suf)]
				break
			}
		}
		out[f] = true
	}
	return out
}

// jaccard is |intersection| / |union|. Symmetric, in [0,1], and 0 when either
// side is empty rather than undefined.
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// Options bound what Group returns.
type Options struct {
	// Threshold is the minimum similarity for two items to join a group.
	// Below about 0.5 unrelated items about the same subsystem start merging;
	// above about 0.8 only near-verbatim repeats group. 0.6 is the default.
	Threshold float64
	// MinMembers is the smallest group worth distilling. A pair is usually a
	// coincidence of phrasing; three or more is a rule being restated.
	MinMembers int
	// MinScore rejects a group whose members do not actually cohere. Single-link
	// grouping chains: A joins B, B joins C, and C may share almost nothing with
	// A. Measured on the live corpus at Threshold 0.3, that produced a 15-member
	// group whose canonical was "Sanity check yourself with a subagent" and whose
	// evidence included "Check lane 2" -- linked through "review"/"lane" and
	// nothing else, at mean similarity 0.16, beside a genuine group at 0.68. A
	// group that does not cohere is not a rule, so it is dropped rather than
	// proposed and left for a human to catch.
	MinScore float64
}

// DefaultOptions are the measured starting point, not a tuned result.
func DefaultOptions() Options { return Options{Threshold: 0.35, MinMembers: 3, MinScore: 0.4} }

// Group partitions items into sets that state the same rule.
//
// Single-link agglomeration: an item joins a group when it clears Threshold
// against ANY member, not against the group's average. That is deliberate --
// a rule restated in drifting words forms a chain, and averaging would break
// the chain in the middle. The cost is that a long chain can wander, which is
// what Score reports so a caller can see a loose group for what it is.
//
// Items are processed oldest first so a group's shape does not depend on the
// order the caller happened to supply.
func Partition(items []Item, o Options) []Group {
	if o.Threshold <= 0 {
		o = DefaultOptions()
	}
	sorted := make([]Item, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].At != sorted[j].At {
			return sorted[i].At < sorted[j].At
		}
		return sorted[i].ID < sorted[j].ID
	})

	sets := make([]map[string]bool, len(sorted))
	for i, it := range sorted {
		sets[i] = terms(it.Body)
	}

	assigned := make([]int, len(sorted))
	for i := range assigned {
		assigned[i] = -1
	}
	var buckets [][]int
	for i := range sorted {
		if assigned[i] >= 0 {
			continue
		}
		b := len(buckets)
		buckets = append(buckets, []int{i})
		assigned[i] = b
		// Walk the bucket as it grows: a later item may match a member added
		// after this loop began, which is what makes it single-link.
		for k := 0; k < len(buckets[b]); k++ {
			for j := range sorted {
				if assigned[j] >= 0 {
					continue
				}
				if jaccard(sets[buckets[b][k]], sets[j]) >= o.Threshold {
					assigned[j] = b
					buckets[b] = append(buckets[b], j)
				}
			}
		}
	}

	var out []Group
	for _, b := range buckets {
		if len(b) < o.MinMembers {
			continue
		}
		members := make([]Item, 0, len(b))
		for _, i := range b {
			members = append(members, sorted[i])
		}
		score := meanSim(b, sets)
		if score < o.MinScore {
			continue
		}
		g := Group{Members: members, Canonical: canonical(members), Score: score}
		g.Key = key(members)
		out = append(out, g)
	}
	// Largest groups first: they are the rules stated most often, and so the
	// ones whose absence from the facts layer costs the most.
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Members) != len(out[j].Members) {
			return len(out[i].Members) > len(out[j].Members)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// canonical picks the member that states the rule best. It never composes a
// new sentence: the return value is always one of the inputs.
//
// The heuristic, in order: prefer a parameter over a directive (a parameter
// is a standing rule; a directive is usually a one-time order), then prefer
// the item whose terms overlap the group's shared vocabulary most (the one
// that says what they all say, rather than a variant with extra specifics),
// then the longest body as a tie-break, since a fuller phrasing carries more
// of the rule. Ties beyond that break on id so the choice is deterministic.
func canonical(members []Item) Item {
	shared := map[string]int{}
	for _, m := range members {
		for t := range terms(m.Body) {
			shared[t]++
		}
	}
	rank := func(it Item) (int, int, int) {
		kindScore := 0
		switch it.Kind {
		case "parameter":
			kindScore = 2
		case "correction":
			kindScore = 1
		}
		overlap := 0
		for t := range terms(it.Body) {
			overlap += shared[t]
		}
		return kindScore, overlap, len(it.Body)
	}
	best := members[0]
	bk, bo, bl := rank(best)
	for _, m := range members[1:] {
		k, o, l := rank(m)
		if k > bk || (k == bk && o > bo) || (k == bk && o == bo && l > bl) ||
			(k == bk && o == bo && l == bl && m.ID < best.ID) {
			best, bk, bo, bl = m, k, o, l
		}
	}
	return best
}

func meanSim(idx []int, sets []map[string]bool) float64 {
	if len(idx) < 2 {
		return 1
	}
	var sum float64
	var n int
	for i := 0; i < len(idx); i++ {
		for j := i + 1; j < len(idx); j++ {
			sum += jaccard(sets[idx[i]], sets[idx[j]])
			n++
		}
	}
	return sum / float64(n)
}

// key derives a stable group identity from its member ids, so the same set of
// items proposes under the same key on a rerun and a caller can tell a
// re-proposal from a new one without storing the whole group.
func key(members []Item) string {
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "|")))
	return "dst-" + hex.EncodeToString(sum[:6])
}
