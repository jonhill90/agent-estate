package knowledge

import (
	"strings"
	"testing"
	"time"
)

func at() time.Time { return time.Date(2026, 9, 3, 5, 30, 0, 0, time.UTC) }

func sample() Source {
	ids := NewIDs(at(), 2)
	return Source{
		Slug: "github-stars", Name: "GitHub stars",
		Origin: "gh api user/starred --paginate",
		Note:   "A star is a dated judgement that needed no distillation.",
		Items: []Item{
			{ID: ids[0], Title: "pacifio/atlas", Detail: "Source control for agents.",
				Permalink: "https://github.com/pacifio/atlas", Signal: "starred",
				Synaptic: []string{"agent-harness"}, Structural: []string{"stars"}},
			{ID: ids[1], Title: "NousResearch/hermes-agent", Permalink: "https://github.com/NousResearch/hermes-agent",
				Signal: "starred", Structural: []string{"stars"}},
		},
	}
}

// His id convention is 14 characters, YYYYMMDDHHmmss, and its stated purpose
// is preventing collisions between agents. A compiler that emitted the same
// second twice would reintroduce what the convention exists to stop.
func TestIDsAreHisFormatAndNeverCollide(t *testing.T) {
	ids := NewIDs(at(), 500)
	seen := map[string]bool{}
	for _, id := range ids {
		if len(id) != 14 {
			t.Fatalf("id %q is %d chars, want 14", id, len(id))
		}
		for _, r := range id {
			if r < '0' || r > '9' {
				t.Fatalf("id %q is not all digits", id)
			}
		}
		if seen[id] {
			t.Fatalf("id %q emitted twice", id)
		}
		seen[id] = true
	}
	if ids[0] != "20260903053000" {
		t.Errorf("first id = %q, want 20260903053000", ids[0])
	}
}

// Both tag classes his Second Brain guide defines must survive: synaptic
// carries a hash and means association, structural is bare and means
// organisation. Collapsing them loses the distinction he draws.
func TestBothTagClassesAreRenderedDistinctly(t *testing.T) {
	out := SourceIndex(sample(), at())
	if !strings.Contains(out, "#agent-harness") {
		t.Error("a synaptic tag must render with a hash")
	}
	if strings.Contains(out, "#stars") {
		t.Error("a structural tag must render bare, not with a hash")
	}
	if !strings.Contains(out, "stars") {
		t.Error("the structural tag must still appear")
	}
}

// Every generated file must carry when it was made and when to stop
// believing it. The failure this design exists to prevent is a hand-kept
// index that drifted to 2.6x stale while still being trusted.
func TestEveryFileCarriesGeneratedAtAndAStalenessRule(t *testing.T) {
	for name, out := range map[string]string{
		"source index": SourceIndex(sample(), at()),
		"top index":    TopIndex([]Source{sample()}, at()),
	} {
		if !strings.Contains(out, "generated_at: 2026-09-03T05:30:00Z") {
			t.Errorf("%s: missing generated_at", name)
		}
		if !strings.Contains(out, "stale_after: 2026-09-10T05:30:00Z") {
			t.Errorf("%s: missing stale_after", name)
		}
		if !strings.Contains(out, "Distrust it after") {
			t.Errorf("%s: must tell the reader when to stop trusting it", name)
		}
		if !strings.Contains(out, "Do not edit it") {
			t.Errorf("%s: must say it is compiled, not hand-kept", name)
		}
		if !strings.Contains(out, "permalink:") {
			t.Errorf("%s: missing permalink field", name)
		}
	}
}

// The top tier must stay a directory. If it grows into a catalogue, an agent
// has to read everything to find one item, which is what
// docs_structure=progressive_disclosure forbids.
func TestTopIndexStaysSmallAndPointsDown(t *testing.T) {
	big := sample()
	ids := NewIDs(at(), 400)
	big.Items = nil
	for i := 0; i < 400; i++ {
		big.Items = append(big.Items, Item{ID: ids[i], Title: "repo/" + ids[i]})
	}
	top := TopIndex([]Source{big}, at())
	if strings.Contains(top, "repo/"+ids[7]) {
		t.Error("the top index must not list items -- it lists sources")
	}
	if !strings.Contains(top, "sources/github-stars.md") {
		t.Error("the top index must point at the per-source file")
	}
	if !strings.Contains(top, "400 items") {
		t.Error("the top index must say how many items a source holds")
	}
	if n := strings.Count(top, "\n"); n > 30 {
		t.Errorf("the top index grew to %d lines; it is a directory, not a catalogue", n)
	}
}

// The index points at things; it never copies them. A copy is a second
// source of truth that goes stale silently.
func TestTheIndexPointsAndDoesNotCopy(t *testing.T) {
	out := SourceIndex(sample(), at())
	if !strings.Contains(out, "https://github.com/pacifio/atlas") {
		t.Error("an item must carry its permalink so the reader can leave")
	}
	if strings.Count(out, "Source control for agents.") != 1 {
		t.Error("the one-line detail should appear once, as a pointer, not be duplicated")
	}
}

// An item with nothing to say must still be indexable: most stars carry no
// note, and dropping them would silently shrink the index.
func TestItemsWithNoTagsOrDetailStillAppear(t *testing.T) {
	out := SourceIndex(sample(), at())
	if !strings.Contains(out, "NousResearch/hermes-agent") {
		t.Error("an item with no synaptic tag and no detail must still be listed")
	}
}
