package knowledge

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestDisclosureStateEndToEnd is agent-estate#1170's own closure test.
//
// THE DESIGN DECISION, ARGUED, NOT ASSUMED: #1170 named two ways to give a
// disclosure state (StateMatchedWithheldMajority / StateMatched) golden-set
// style end-to-end coverage --
//
//  1. extend goldenset's Case schema with an expected-state field so
//     cases.json can assert a state directly, or
//  2. a dedicated test outside the goldenset fixture, in the style of
//     agent-estate#1181's TestPublicPlusWithheldEqualsPrivateTotal, that
//     builds its own fixture and asserts the state end-to-end through the
//     real Query pipeline (BM25 scoring + classify.go's publish boundary +
//     this state decision), not goldenset's cases.json/natural_cases.json/
//     star_cases.json.
//
// (2) is what this file does. Reasons, all three checked against #1170's
// own text before picking:
//
//   - Adding a case to cases.json moves the private stratum denominator
//     22->23 (#1170's own "if you touch the fixture" section) and #1175's
//     K1 closeout plan named goldenset as a single-lane-at-a-time
//     resource for exactly that reason -- any concurrent lane touching
//     cases.json or buildRatchets collides on the ratchet baseline
//     (agent-estate#1172 already demonstrated this by moving 12->21 and
//     invalidating two documents that only quoted the number). A test
//     that never touches cases.json cannot collide with anything, and
//     needs no maxMisses change, no TestBuildRatchetsMaxMissesArePinned
//     "want" map update, and no re-measurement of the 21-question
//     natural-language stratum's own hit rate.
//   - Extending Case's schema (option 1) is the more general fix, but
//     "more general" is not "needed here": nothing else in this codebase
//     has yet asked to assert a state against a real *question in the
//     fixture* rather than a real *item lookup* -- adding a field for one
//     caller, before a second caller exists to justify the generality, is
//     exactly the shape #1170 itself warns against ("more cases written
//     carelessly is worse than fewer written well", quoting #1140/#1138).
//     If a second consumer of state-assertion ever appears, extending the
//     schema then has two real call sites to design against instead of
//     one speculative one.
//   - #1181's own test rejected reusing goldenset for precisely this
//     reason ("reusing the golden set would couple this test to fixture
//     churn") and this test inherits that same rationale one-for-one: a
//     hand-authored fixture means this test's pass/fail can only move
//     because the disclosure-state arithmetic in query.go broke, never
//     because someone else's cases.json grew.
//
// COVERING BOTH BRANCHES (agent-estate#1170's "Cover both branches"
// section, itself citing agent-estate#1171's finding that coverage.state
// was a constant, not a signal): a state assertion that only ever
// exercises the positive side cannot tell a working signal from a
// constant that always reports the interesting state. This test builds
// two fixtures from the same shared question, one where withheld items
// are the strict majority (asserts StateMatchedWithheldMajority) and one
// where they are not (asserts plain StateMatched) -- so a future change
// that collapses the majority state into plain StateMatched, or the
// reverse, fails one of the two subtests rather than passing by
// coincidence.
//
// Every fixture item carries all four of the shared question's terms in
// its own Tier1, the same "guaranteed candidate, never left to BM25
// scoring chance" discipline invariantFixture (query_test.go,
// agent-estate#1179) already uses -- so the public/private split below is
// exactly what each case's field names say, not an artifact of ranking.
func TestDisclosureStateEndToEnd(t *testing.T) {
	const question = "vendor contract renewal terms"

	buildFixture := func(t *testing.T, nPublic, nPrivate int) string {
		t.Helper()
		var items []Item
		for i := 0; i < nPublic; i++ {
			id := fmt.Sprintf("2026090517%04d", i)
			items = append(items, Item{
				ID: id, Source: "vault-fact",
				Permalink:   "/vault/agent/facts/vendor-contract-" + id + ".md",
				Tier1:       "vendor contract renewal terms -- public copy " + id,
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			})
		}
		for i := 0; i < nPrivate; i++ {
			id := fmt.Sprintf("2026090518%04d", i)
			items = append(items, Item{
				ID: id, Source: "corpus-parameter",
				Permalink:   "corpus:item:" + id,
				Tier1:       "vendor contract renewal terms -- private copy " + id,
				Publishable: false, PublishBasis: "corpus-parameter: source defaults to private",
			})
		}
		path := filepath.Join(t.TempDir(), "index.json")
		res := Result{
			GeneratedAt:   time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
			StalenessRule: stalenessRule,
			Note:          derivedNote,
			Items:         items,
		}
		if err := Write(path, res); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("majority withheld reports matched_withheld_majority", func(t *testing.T) {
		// 1 public, 3 private: withheldPrivate(3) > TotalMatched(1) --
		// strict majority, the ">" (not ">=") branch query.go actually
		// tests.
		path := buildFixture(t, 1, 3)
		got := Query(path, question, 0, false)
		if got.State != StateMatchedWithheldMajority {
			t.Fatalf("majority-withheld fixture: got state %q, want %q -- public=%d matched, %d withheld (reason: %q)",
				got.State, StateMatchedWithheldMajority, got.TotalMatched, got.WithheldPrivate, got.Reason)
		}
		if got.WithheldPrivate <= got.TotalMatched {
			t.Fatalf("fixture construction broken: withheld_private(%d) must exceed total_matched(%d) for this subtest to actually exercise the majority branch",
				got.WithheldPrivate, got.TotalMatched)
		}
	})

	t.Run("non-majority withheld reports plain matched", func(t *testing.T) {
		// 3 public, 1 private: withheldPrivate(1) is not > TotalMatched(3)
		// -- the negative side #1170 named as thinner and required this
		// test to cover so the positive-only assertion above cannot pass
		// on a constant.
		path := buildFixture(t, 3, 1)
		got := Query(path, question, 0, false)
		if got.State != StateMatched {
			t.Fatalf("non-majority-withheld fixture: got state %q, want %q -- public=%d matched, %d withheld (reason: %q)",
				got.State, StateMatched, got.TotalMatched, got.WithheldPrivate, got.Reason)
		}
		if got.WithheldPrivate > got.TotalMatched {
			t.Fatalf("fixture construction broken: withheld_private(%d) must not exceed total_matched(%d) for this subtest to actually exercise the non-majority branch",
				got.WithheldPrivate, got.TotalMatched)
		}
	})
}
