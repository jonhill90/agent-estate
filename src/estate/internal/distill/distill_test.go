package distill

import (
	"strings"
	"testing"
)

func it(id, kind, body string, at int64) Item {
	return Item{ID: id, Kind: kind, Status: "acted", Body: body, At: at}
}

// The load-bearing property: a rule Jon stated several ways forms one group,
// and an unrelated rule stays out of it.
func TestGroupsRestatementsAndExcludesUnrelated(t *testing.T) {
	items := []Item{
		it("a", "directive", "Query the corpus before asking Jon a question", 10),
		it("b", "parameter", "Exhaust the recorded record before a question reaches Jon", 20),
		it("c", "directive", "Always query the recorded record before asking Jon", 30),
		it("d", "parameter", "Never write to the macOS keychain; a failed read is a report", 40),
	}
	gs := Partition(items, Options{Threshold: 0.3, MinMembers: 2})
	if len(gs) != 1 {
		t.Fatalf("want exactly one group, got %d: %+v", len(gs), gs)
	}
	if len(gs[0].Members) != 3 {
		t.Fatalf("want the three restatements grouped, got %d", len(gs[0].Members))
	}
	for _, m := range gs[0].Members {
		if m.ID == "d" {
			t.Fatal("the unrelated keychain rule was merged into the group")
		}
	}
}

// Distillation must never author wording. Whatever it names as the group's
// statement has to be a body one of the members actually carries.
func TestCanonicalIsAlwaysAnExistingMemberBody(t *testing.T) {
	items := []Item{
		it("a", "directive", "check the record first before asking", 10),
		it("b", "parameter", "check the record first before asking anything of Jon", 20),
		it("c", "directive", "check the record first", 30),
	}
	gs := Partition(items, Options{Threshold: 0.3, MinMembers: 2})
	if len(gs) != 1 {
		t.Fatalf("want one group, got %d", len(gs))
	}
	found := false
	for _, m := range gs[0].Members {
		if m.Body == gs[0].Canonical.Body && m.ID == gs[0].Canonical.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("canonical %q is not one of the members -- wording was invented", gs[0].Canonical.Body)
	}
}

// A parameter outranks a directive as the canonical statement: a parameter is
// a standing rule, a directive is usually a one-time order.
func TestCanonicalPrefersParameterOverDirective(t *testing.T) {
	items := []Item{
		it("a", "directive", "record the decision and the reason it was taken", 10),
		it("b", "parameter", "record the decision and the reason it was taken", 20),
		it("c", "directive", "record the decision and the reason it was taken now", 30),
	}
	gs := Partition(items, Options{Threshold: 0.3, MinMembers: 2})
	if gs[0].Canonical.Kind != "parameter" {
		t.Fatalf("canonical kind = %q, want parameter (%q chosen)", gs[0].Canonical.Kind, gs[0].Canonical.ID)
	}
}

// MinMembers exists so a coincidental pair does not become a fact.
func TestPairsBelowMinMembersAreNotProposed(t *testing.T) {
	items := []Item{
		it("a", "parameter", "rotate the deployment credential every quarter", 10),
		it("b", "parameter", "rotate the deployment credential every quarter or sooner", 20),
	}
	if gs := Partition(items, Options{Threshold: 0.3, MinMembers: 3}); len(gs) != 0 {
		t.Fatalf("a pair was proposed under MinMembers=3: %+v", gs)
	}
}

// Same input, same groups and same keys -- otherwise a rerun re-proposes work
// already reviewed, and no caller can tell a new proposal from an old one.
func TestGroupingIsDeterministicRegardlessOfInputOrder(t *testing.T) {
	base := []Item{
		it("a", "directive", "verify the claim against the record before reporting it", 10),
		it("b", "parameter", "verify every claim against the record before reporting", 20),
		it("c", "directive", "verify claims against the record first", 30),
	}
	rev := []Item{base[2], base[0], base[1]}
	g1 := Partition(base, Options{Threshold: 0.3, MinMembers: 2})
	g2 := Partition(rev, Options{Threshold: 0.3, MinMembers: 2})
	if len(g1) != len(g2) || g1[0].Key != g2[0].Key {
		t.Fatalf("order changed the result: %v vs %v", g1[0].Key, g2[0].Key)
	}
	if g1[0].Canonical.ID != g2[0].Canonical.ID {
		t.Fatalf("order changed the canonical: %q vs %q", g1[0].Canonical.ID, g2[0].Canonical.ID)
	}
}

// "corpus" and "vault" are stop words here for a measured reason: tagging on
// them swallowed 1,752 and 1,748 notes, because nearly every item is about
// them. If they ever count as signal again, unrelated items will merge.
func TestSubjectWideWordsDoNotBindUnrelatedItems(t *testing.T) {
	items := []Item{
		it("a", "parameter", "the corpus vault holds the deployment schedule", 10),
		it("b", "parameter", "the corpus vault governs keychain access rules", 20),
		it("c", "parameter", "the corpus vault names the review protocol", 30),
	}
	if gs := Partition(items, Options{Threshold: 0.5, MinMembers: 2}); len(gs) != 0 {
		t.Fatalf("items sharing only subject-wide words were grouped: %+v", gs[0].Members)
	}
}

func TestKeyIsStableAndPrefixed(t *testing.T) {
	m := []Item{it("b", "parameter", "x", 2), it("a", "parameter", "y", 1)}
	k1 := key(m)
	k2 := key([]Item{m[1], m[0]})
	if k1 != k2 {
		t.Fatalf("key depends on member order: %q vs %q", k1, k2)
	}
	if !strings.HasPrefix(k1, "dst-") {
		t.Fatalf("key %q lacks the dst- prefix", k1)
	}
}

// A chain that wanders must be dropped, not proposed. Single-link grouping
// lets A join B and B join C while A and C share almost nothing; measured on
// the live corpus this produced a 15-member group whose canonical was about
// sanity-checking and whose evidence was about reviewing lanes, at mean
// similarity 0.16. MinScore is the guard.
func TestIncoherentChainsAreRejected(t *testing.T) {
	items := []Item{
		it("a", "parameter", "sanity check reasoning with a dedicated reviewer lane", 10),
		it("b", "directive", "review the reviewer lane", 20),
		it("c", "directive", "review the lane", 30),
		it("d", "directive", "check lane two", 40),
	}
	loose := Partition(items, Options{Threshold: 0.25, MinMembers: 3, MinScore: 0})
	if len(loose) == 0 {
		t.Skip("fixture did not chain; nothing to guard in this shape")
	}
	if loose[0].Score >= 0.4 {
		t.Skip("fixture chained tightly; not the wandering case")
	}
	guarded := Partition(items, Options{Threshold: 0.25, MinMembers: 3, MinScore: 0.4})
	for _, g := range guarded {
		if g.Score < 0.4 {
			t.Fatalf("a group below MinScore was proposed: %.2f %+v", g.Score, g.Key)
		}
	}
}
