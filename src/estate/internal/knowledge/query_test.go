package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testIndex() Result {
	return Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Sources: []SourceResult{
			{Name: "vault-facts", OK: true, Count: 2},
			{Name: "corpus-parameters", OK: true, Count: 1},
			{Name: "github-stars", OK: false, Reason: "gh: not authenticated"},
		},
		Items: []Item{
			{
				ID: "20260903120000", Source: "vault-fact",
				Permalink: "/vault/agent/facts/auth-token-rotation.md",
				Tier1:     "auth token rotation -- rotate every 90 days",
				Tier2:     "Jon decided auth tokens rotate every 90 days after the March incident.",
				Tier3:     "open /vault/agent/facts/auth-token-rotation.md for the full fact",
				// Publishable set true on every fixture item here on
				// purpose: these tests exercise ranking/matching/citation
				// behaviour, not the publishability filter -- that has
				// its own fixtures below (TestQueryDefaultWithholds...,
				// TestQueryPrivateModeIncludes...). A fixture item left
				// at the zero value would silently fail every one of
				// these under the new default-deny filter and make this
				// file's own intent unreadable from its diff.
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
			{
				ID: "20260903120001", Source: "vault-fact",
				Permalink:   "/vault/agent/facts/deploy-window.md",
				Tier1:       "deploy window -- weekday mornings only",
				Tier2:       "Deploys happen weekday mornings only, never a Friday.",
				Tier3:       "open /vault/agent/facts/deploy-window.md for the full fact",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
			{
				ID: "20260903120002", Source: "corpus-parameter",
				Permalink:      "corpus:item:4821",
				StructuralTags: []string{"weight:hard"},
				Tier1:          "auth must use short-lived tokens, never long-lived keys",
				Tier2:          "auth must use short-lived tokens, never long-lived keys",
				Tier3:          "the corpus's own item 4821 (live_parameters) -- not this file",
				Publishable:    true, PublishBasis: "test fixture: marked publishable",
			},
		},
	}
}

func writeTestIndex(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.json")
	if err := Write(path, testIndex()); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	return path
}

// TestQueryMatchedRanksByTermOverlap also exercises the minMatchedTerms
// floor (agent-estate#1026): the question has three distinct scoreable
// terms ("decide" -- matched via "decided" -- "auth", "tokens"), so an
// item needs all three to clear the floor. The auth-token-rotation fact
// does (its own Tier2 uses "decided"); the corpus item only shares two
// of the three ("auth", "tokens") and is correctly excluded -- weaker
// evidence than the fact that matches every term, not a false positive
// like none-01's coincidental two-term match.
func TestQueryMatchedRanksByTermOverlap(t *testing.T) {
	path := writeTestIndex(t)
	got := Query(path, "what did Jon decide about auth tokens", 0, false)

	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q (reason=%q)", got.State, StateMatched, got.Reason)
	}
	if got.TotalMatched != 1 {
		t.Fatalf("TotalMatched = %d, want 1", got.TotalMatched)
	}
	if len(got.Matches) != 1 || got.Matches[0].ID != "20260903120000" {
		t.Fatalf("Matches = %+v, want just the auth-token-rotation fact", got.Matches)
	}
	// The unrelated deploy-window fact and the weaker two-of-three corpus
	// item must not appear.
	for _, m := range got.Matches {
		if m.ID == "20260903120001" {
			t.Fatalf("deploy-window fact matched an auth-token question: %+v", m)
		}
		if m.ID == "20260903120002" {
			t.Fatalf("corpus item matched only 2 of 3 terms but was still returned: %+v", m)
		}
	}
	if got.RankingBasis == "" {
		t.Error("RankingBasis is empty -- ranking basis must be legible")
	}
}

func TestQueryStatesNotReturnedCount(t *testing.T) {
	path := writeTestIndex(t)
	got := Query(path, "auth tokens", 1, false)
	if got.TotalMatched != 2 {
		t.Fatalf("TotalMatched = %d, want 2", got.TotalMatched)
	}
	if len(got.Matches) != 1 {
		t.Fatalf("len(Matches) = %d, want 1 (capped)", len(got.Matches))
	}
	if got.NotReturned != 1 {
		t.Errorf("NotReturned = %d, want 1", got.NotReturned)
	}
}

func TestQueryNoMatchIsDistinctFromMissingIndex(t *testing.T) {
	path := writeTestIndex(t)
	got := Query(path, "kubernetes ingress rate limiting", 0, false)
	if got.State != StateNoMatch {
		t.Fatalf("State = %q, want %q", got.State, StateNoMatch)
	}
	if got.TotalMatched != 0 || len(got.Matches) != 0 {
		t.Fatalf("got non-empty matches on a no-match question: %+v", got)
	}
	// Source statuses still travel through even on a genuine no-match --
	// the caller can see github-stars was down when the index was built.
	if len(got.SourceStatuses) == 0 {
		t.Error("SourceStatuses is empty on a no-match result")
	}
}

// TestQueryFiltersACoincidentalLowScoreMatch is agent-estate#1026's
// none-01 shape reproduced against a small fixture: a five-term question
// where an unrelated item happens to share only two generic words with
// it must not be returned -- the minMatchedTerms floor exists exactly
// to keep this out of StateMatched, so the caller reaches StateNoMatch
// (a real "nothing answers this") instead of an unfalsifiable answer.
func TestQueryFiltersACoincidentalLowScoreMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Items: []Item{
			{
				ID: "20260903120010", Source: "github-stars",
				Permalink: "https://github.com/apache/airflow",
				Tier1:     "apache/airflow -- Apache Airflow - A platform to programmatically author, schedule, and monitor workflows",
			},
		},
	}
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}

	got := Query(path, "office vending machine restocking schedule", 0, false)
	if got.State != StateNoMatch {
		t.Fatalf("State = %q, want %q -- a 2-of-5 coincidental term match should not clear the floor (matches=%+v)", got.State, StateNoMatch, got.Matches)
	}
	if len(got.Matches) != 0 {
		t.Fatalf("Matches = %+v, want none", got.Matches)
	}
}

func TestQueryIndexMissingIsTypedSeparatelyFromNoMatch(t *testing.T) {
	got := Query(filepath.Join(t.TempDir(), "never-generated.json"), "auth tokens", 0, false)
	if got.State != StateIndexMissing {
		t.Fatalf("State = %q, want %q", got.State, StateIndexMissing)
	}
	if got.Reason == "" {
		t.Error("Reason is empty on a missing index")
	}
}

func TestQueryIndexUnreadableIsTypedSeparatelyFromMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Query(path, "auth tokens", 0, false)
	if got.State != StateIndexUnreadable {
		t.Fatalf("State = %q, want %q", got.State, StateIndexUnreadable)
	}
	if got.Reason == "" {
		t.Error("Reason is empty on an unreadable index")
	}
}

// TestQueryNeverReturnsAnUncitedMatch is the rejection-criterion test
// #1019 explicitly asks for: every returned item must name its source
// and id, or it must not be returned at all.
func TestQueryNeverReturnsAnUncitedMatch(t *testing.T) {
	path := writeTestIndex(t)
	got := Query(path, "auth tokens", 0, false)
	if len(got.Matches) == 0 {
		t.Fatal("test setup produced no matches to check citations on")
	}
	for _, m := range got.Matches {
		if m.ID == "" {
			t.Errorf("Match has no ID: %+v", m)
		}
		if m.Source == "" {
			t.Errorf("Match %s has no Source: %+v", m.ID, m)
		}
	}
}

func TestGetReturnsFullItemByID(t *testing.T) {
	path := writeTestIndex(t)
	item, ok, reason := Get(path, "20260903120000", false)
	if !ok {
		t.Fatalf("Get() ok=false, reason=%q", reason)
	}
	if item.Tier2 == "" {
		t.Error("Get() returned an item with no Tier2 body")
	}
	if item.Source == "" {
		t.Error("Get() returned an item with no Source citation")
	}
}

func TestGetUnknownIDIsReportedNotEmpty(t *testing.T) {
	path := writeTestIndex(t)
	_, ok, reason := Get(path, "no-such-id", false)
	if ok {
		t.Fatal("Get() ok=true for an id that was never in the index")
	}
	if reason == "" {
		t.Error("Get() gave no reason for an unknown id")
	}
}

func TestGetMissingIndexIsTypedSeparately(t *testing.T) {
	_, ok, reason := Get(filepath.Join(t.TempDir(), "never-generated.json"), "20260903120000", false)
	if ok {
		t.Fatal("Get() ok=true against a never-generated index")
	}
	if reason == "" {
		t.Error("Get() gave no reason for a missing index")
	}
}

// privateIndex builds a fixture with one publishable and one private item
// that both answer the same question -- agent-estate#1033's own shape:
// classify marks most items private at compile time, and Query/Get must
// enforce that rather than merely carry the field.
func privateIndex(t *testing.T) string {
	t.Helper()
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Items: []Item{
			{
				ID: "20260903130000", Source: "github-stars",
				Permalink:   "https://github.com/a/rotator",
				Tier1:       "a/rotator -- credential rotation keychain tool",
				Publishable: true, PublishBasis: "github-stars: already public GitHub activity",
			},
			{
				ID: "20260903130001", Source: "corpus-parameter",
				Permalink:   "corpus:item:9001",
				Tier1:       "credential rotation keychain policy -- fail closed on missing creds",
				Publishable: false, PublishBasis: "corpus-parameter: source defaults to private",
			},
		},
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestQueryDefaultWithholdsPrivateItems is the mutation target for
// agent-estate#1033: with the filter disabled (score < required is the
// only exclusion), this must fail, because the private item would then
// come back under the default call. It is the direct regression test for
// "classify marks 1,237 items private and query returns them anyway."
func TestQueryDefaultWithholdsPrivateItems(t *testing.T) {
	path := privateIndex(t)
	got := Query(path, "credential rotation keychain", 0, false)

	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q (reason=%q)", got.State, StateMatched, got.Reason)
	}
	for _, m := range got.Matches {
		if m.ID == "20260903130001" {
			t.Fatalf("private item returned under the default (non-private) call: %+v", m)
		}
	}
	if got.WithheldPrivate != 1 {
		t.Fatalf("WithheldPrivate = %d, want 1", got.WithheldPrivate)
	}
	if got.PrivateIncluded {
		t.Error("PrivateIncluded = true on a call that never asked for it")
	}
}

// TestQueryWithheldPrivateIsDistinctFromNoMatch covers the case where
// EVERY matching item is private: this must not collapse into
// StateNoMatch ("nothing matched" is a different, false, answer from
// "something matched and you may not see it").
func TestQueryWithheldPrivateIsDistinctFromNoMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Items: []Item{
			{
				ID: "20260903130002", Source: "corpus-parameter",
				Permalink:   "corpus:item:9002",
				Tier1:       "credential rotation keychain policy -- fail closed on missing creds",
				Publishable: false, PublishBasis: "corpus-parameter: source defaults to private",
			},
		},
	}
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}

	got := Query(path, "credential rotation keychain", 0, false)
	if got.State != StateWithheldPrivate {
		t.Fatalf("State = %q, want %q", got.State, StateWithheldPrivate)
	}
	if got.State == StateNoMatch {
		t.Fatal("a real private match collapsed into StateNoMatch")
	}
	if got.WithheldPrivate != 1 {
		t.Fatalf("WithheldPrivate = %d, want 1", got.WithheldPrivate)
	}
	if got.Reason == "" {
		t.Error("Reason is empty on a withheld-private result")
	}
}

// withheldMajorityIndex builds a fixture with one publishable item and
// privateCount private items, all four terms of "credential rotation
// keychain policy" scoreable against every item's Tier1 -- agent-estate#1052's
// shape: a real public answer exists and is returned, but the private
// population outnumbers it.
func withheldMajorityIndex(t *testing.T, privateCount int) string {
	t.Helper()
	items := []Item{
		{
			ID: "20260903140000", Source: "vault-fact",
			Permalink:   "/vault/agent/facts/credential-rotation.md",
			Tier1:       "credential rotation keychain policy -- public summary",
			Publishable: true, PublishBasis: "test fixture: marked publishable",
		},
	}
	for i := 0; i < privateCount; i++ {
		id := fmt.Sprintf("2026090314000%d", i+1)
		items = append(items, Item{
			ID:          id,
			Source:      "corpus-parameter",
			Permalink:   "corpus:item:" + id,
			Tier1:       "credential rotation keychain policy -- private detail",
			Publishable: false, PublishBasis: "corpus-parameter: source defaults to private",
		})
	}
	path := filepath.Join(t.TempDir(), "index.json")
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 14, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Items:         items,
	}
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestQueryMatchedWithheldMajorityFiresWhenPrivateOutnumbersPublic is the
// direct regression test for agent-estate#1052: a query with more withheld
// private matches than returned public ones must be distinguishable from an
// ordinary StateMatched, without collapsing into StateWithheldPrivate
// (which requires zero public matches) or changing what was actually
// returned.
func TestQueryMatchedWithheldMajorityFiresWhenPrivateOutnumbersPublic(t *testing.T) {
	path := withheldMajorityIndex(t, 2) // 1 public, 2 private -- majority private
	got := Query(path, "credential rotation keychain policy", 0, false)

	if got.State != StateMatchedWithheldMajority {
		t.Fatalf("State = %q, want %q (reason=%q)", got.State, StateMatchedWithheldMajority, got.Reason)
	}
	if len(got.Matches) != 1 {
		t.Fatalf("len(Matches) = %d, want 1 -- the public item must still be returned, not suppressed", len(got.Matches))
	}
	if got.WithheldPrivate != 2 {
		t.Fatalf("WithheldPrivate = %d, want 2", got.WithheldPrivate)
	}
	if got.Reason == "" {
		t.Error("Reason is empty on a majority-withheld matched result")
	}
}

// TestQueryMatchedWithheldMajorityDoesNotFireOnATie covers the boundary:
// equal public and private counts is NOT a majority, so this must stay
// plain StateMatched -- a strict ">" comparison, not ">=", per
// StateMatchedWithheldMajority's own doc comment.
func TestQueryMatchedWithheldMajorityDoesNotFireOnATie(t *testing.T) {
	path := withheldMajorityIndex(t, 1) // 1 public, 1 private -- a tie
	got := Query(path, "credential rotation keychain policy", 0, false)

	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q (a 1-public/1-private tie is not a majority)", got.State, StateMatched)
	}
}

// TestQueryPrivateModeIncludesAndMarksPrivateItems is the explicit,
// visible private mode agent-estate#1028's point 3 asks for: not a
// silent flag -- PrivateIncluded and each private Match's own
// Publishable field must both say so in the result itself.
func TestQueryPrivateModeIncludesAndMarksPrivateItems(t *testing.T) {
	path := privateIndex(t)
	got := Query(path, "credential rotation keychain", 0, true)

	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q (reason=%q)", got.State, StateMatched, got.Reason)
	}
	if !got.PrivateIncluded {
		t.Error("PrivateIncluded = false on a call that explicitly asked for private material")
	}
	if got.WithheldPrivate != 0 {
		t.Fatalf("WithheldPrivate = %d, want 0 -- nothing is withheld in private mode", got.WithheldPrivate)
	}
	var sawPrivate bool
	for _, m := range got.Matches {
		if m.ID == "20260903130001" {
			sawPrivate = true
			if m.Publishable {
				t.Error("private match's own Publishable field reads true")
			}
		}
	}
	if !sawPrivate {
		t.Fatalf("private mode did not return the private item: %+v", got.Matches)
	}
}

// TestGetRefusesPrivateItemByDefault is #1033's direct point: stable ids
// (#1032) make a private item's id quotable and re-fetchable, so Get
// must enforce the same rule Query does rather than being a bypass.
func TestGetRefusesPrivateItemByDefault(t *testing.T) {
	path := privateIndex(t)
	_, ok, reason := Get(path, "20260903130001", false)
	if ok {
		t.Fatal("Get() ok=true for a private item with includePrivate=false")
	}
	if reason == "" {
		t.Error("Get() gave no reason for refusing a private item")
	}
}

// TestGetPrivateModeReturnsPrivateItem proves --private is not merely
// accepted but functional on Get, symmetric with Query.
func TestGetPrivateModeReturnsPrivateItem(t *testing.T) {
	path := privateIndex(t)
	item, ok, reason := Get(path, "20260903130001", true)
	if !ok {
		t.Fatalf("Get(includePrivate=true) ok=false, reason=%q", reason)
	}
	if item.Publishable {
		t.Error("fetched item's own Publishable field reads true for a private fixture item")
	}
}

// tagFilterIndex reproduces agent-estate#1024's own measured shape at
// small scale: several items carry a status: tag (most status:open,
// exactly one status:needs_review), and one item's own PROSE happens to
// contain the words "status" and "open" without ever carrying the tag --
// the exact false-positive #1024 measured against the real index
// (loop_test_harnesses=copilot,pi, "matched 'status' and 'open'" as bare
// terms). A correct exact-tag filter returns only the status:open items;
// the unrelated term-matching prose item must never appear.
func tagFilterIndex(t *testing.T) string {
	t.Helper()
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Items: []Item{
			{
				ID: "20260903140000", Source: "corpus-parameter",
				Permalink:      "corpus:item:9101",
				StructuralTags: []string{"weight:hard", "status:open"},
				Tier1:          "open question: which storage format replaces index.json",
				Publishable:    false, PublishBasis: "corpus-parameter: source defaults to private",
			},
			{
				ID: "20260903140001", Source: "corpus-parameter",
				Permalink:      "corpus:item:9102",
				StructuralTags: []string{"weight:hard", "status:open"},
				Tier1:          "open question: which harness handles the web upload path",
				Publishable:    false, PublishBasis: "corpus-parameter: source defaults to private",
			},
			{
				ID: "20260903140002", Source: "corpus-parameter",
				Permalink:      "corpus:item:9103",
				StructuralTags: []string{"weight:hard", "status:needs_review"},
				Tier1:          "needs review: retire the shell fallback",
				Publishable:    false, PublishBasis: "corpus-parameter: source defaults to private",
			},
			{
				ID: "20260903140003", Source: "corpus-parameter",
				Permalink:   "corpus:item:9104",
				Tier1:       "loop_test_harnesses=copilot,pi -- the parameter's own status is open for review",
				Publishable: false, PublishBasis: "corpus-parameter: source defaults to private",
			},
			{
				// A tag that carries "status:open" as a SUBSTRING of a
				// longer, different tag, never as the tag itself. An
				// exact filter must reject this; a substring-matching
				// filter (the mutation this file's own test guards
				// against) would wrongly accept it.
				ID: "20260903140004", Source: "corpus-parameter",
				Permalink:      "corpus:item:9105",
				StructuralTags: []string{"weight:hard", "status:open_pending_migration"},
				Tier1:          "a different, longer-named status that merely starts with status:open",
				Publishable:    false, PublishBasis: "corpus-parameter: source defaults to private",
			},
		},
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}
	return path
}

// fieldWeightIndex reproduces #1043's review finding in miniature: a
// short item whose Tier1 (title) actually names the subject, sitting
// next to a long item whose Tier1 is unrelated but whose Tier2 body
// happens to contain more of the question's own terms. #1027 compiled
// full vault fact bodies into Tier2, and an unweighted matched-term
// count let the long body outrank the on-topic title purely on raw
// count -- this fixture is that shape at the smallest size that still
// clears requiredScore(3) for both items.
func fieldWeightIndex(t *testing.T) string {
	t.Helper()
	idx := Result{
		GeneratedAt: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
		Items: []Item{
			{
				ID: "20260904000000", Source: "vault-fact",
				Permalink:   "/vault/agent/facts/on-topic-title.md",
				Tier1:       "tmux session keychain locked",
				Tier2:       "",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
			{
				ID: "20260904000001", Source: "vault-fact",
				Permalink: "/vault/agent/facts/unrelated-title-long-body.md",
				Tier1:     "an unrelated fact about deploy windows",
				Tier2: "this body happens to mention tmux, and separately a " +
					"session, and later a keychain, and further down locked, " +
					"and finally recovery -- five of the question's own terms, " +
					"none of them in this item's own title",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
		},
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := Write(path, idx); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	return path
}

// TestQueryExactTagFilterExcludesTermCoincidence is #1024's own reported
// defect, reproduced: "status:open" as a bare term search matches every
// item carrying ANY status: tag plus any item whose prose merely contains
// "status" and "open" as separate words; the exact-tag filter must return
// only the two items genuinely carrying the exact tag status:open.
func TestQueryExactTagFilterExcludesTermCoincidence(t *testing.T) {
	path := tagFilterIndex(t)
	got := Query(path, "status:open", 0, true)

	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q (reason=%q)", got.State, StateMatched, got.Reason)
	}
	if got.TotalMatched != 2 {
		t.Fatalf("TotalMatched = %d, want 2 (exactly the status:open items)", got.TotalMatched)
	}
	seen := map[string]bool{}
	for _, m := range got.Matches {
		seen[m.ID] = true
	}
	if !seen["20260903140000"] || !seen["20260903140001"] {
		t.Fatalf("Matches = %+v, want both status:open items", got.Matches)
	}
	if seen["20260903140002"] {
		t.Fatal("status:needs_review item matched a status:open filter")
	}
	if seen["20260903140003"] {
		t.Fatal("the term-coincidence item (prose contains \"status\" and \"open\" but carries no status: tag) was returned")
	}
	if seen["20260903140004"] {
		t.Fatal("an item tagged status:open_pending_migration (status:open as a SUBSTRING, not the tag itself) was returned -- filter is not exact")
	}
	if len(got.TagFilters) != 1 || got.TagFilters[0] != "status:open" {
		t.Fatalf("TagFilters = %+v, want [status:open]", got.TagFilters)
	}
}

// TestQueryTagFilterAloneWithNoQuestionIsLegal covers #1024's explicit
// requirement: "list everything tagged status:open" (a filter with no
// question terms at all) must be a real, legal query, not routed into the
// "no scoreable terms" no_match path a term-only empty question hits.
func TestQueryTagFilterAloneWithNoQuestionIsLegal(t *testing.T) {
	path := tagFilterIndex(t)
	got := Query(path, "status:open", 0, true)
	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q -- a tag filter alone must be a legal query", got.State, StateMatched)
	}
}

// TestQueryTagFilterDefaultModeWithholdsPrivateMatches is #1024's own
// composition requirement: the real corpus's status:open items are
// private (corpus-parameter defaults to private), so a default-mode tag
// query must report StateWithheldPrivate ("something matched, you may
// not see it"), never StateNoMatch ("nothing matched") and never a
// weakened filter that shows them anyway.
func TestQueryTagFilterDefaultModeWithholdsPrivateMatches(t *testing.T) {
	path := tagFilterIndex(t)
	got := Query(path, "status:open", 0, false)
	if got.State != StateWithheldPrivate {
		t.Fatalf("State = %q, want %q (reason=%q)", got.State, StateWithheldPrivate, got.Reason)
	}
	if got.WithheldPrivate != 2 {
		t.Fatalf("WithheldPrivate = %d, want 2", got.WithheldPrivate)
	}
	if len(got.Matches) != 0 {
		t.Fatalf("Matches = %+v, want none in default mode", got.Matches)
	}
}

// TestQueryUnknownTagFilterIsNoMatchNotError is #1024's explicit rule: an
// unrecognised tag must report StateNoMatch with a reason naming it,
// never silently fall through to scoring the whole index (which would
// look identical to a real, well-formed answer) and never a Go error.
func TestQueryUnknownTagFilterIsNoMatchNotError(t *testing.T) {
	path := tagFilterIndex(t)
	got := Query(path, "status:nonexistentbogus", 0, true)
	if got.State != StateNoMatch {
		t.Fatalf("State = %q, want %q", got.State, StateNoMatch)
	}
	if got.Reason == "" {
		t.Error("Reason is empty on an unknown-tag no_match")
	}
	if len(got.Matches) != 0 {
		t.Fatalf("Matches = %+v, want none for an unknown tag", got.Matches)
	}
}

// TestQueryTagFilterComposesWithQuestionTerms proves "filter first, rank
// within": a tag filter combined with question terms narrows to the
// tagged population, then ranks only inside it -- an item carrying the
// tag but not matching the terms is excluded exactly like ordinary term
// scoring already excludes non-matching items.
func TestQueryTagFilterComposesWithQuestionTerms(t *testing.T) {
	path := tagFilterIndex(t)
	got := Query(path, "status:open storage format", 0, true)
	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q (reason=%q)", got.State, StateMatched, got.Reason)
	}
	if got.TotalMatched != 1 || len(got.Matches) != 1 || got.Matches[0].ID != "20260903140000" {
		t.Fatalf("Matches = %+v, want just the storage-format item", got.Matches)
	}
}

// TestQueryRanksATier1MatchAboveALongerTier2OnlyMatch pins the
// field-weighted ranking fix: the on-topic-title item matches only 3 of
// the question's 5 scoreable terms (all in Tier1), the unrelated item
// matches all 5 (all in Tier2 only, i.e. a strictly higher unweighted
// score) -- yet the on-topic item must rank first, because a term found
// in Tier1 outweighs the same term found only in Tier2 (see rankingWeight
// in query.go). Before this fix, sort was by unweighted score alone and
// the long-body item would have ranked first.
func TestQueryRanksATier1MatchAboveALongerTier2OnlyMatch(t *testing.T) {
	path := fieldWeightIndex(t)
	got := Query(path, "tmux session keychain locked recovery", 0, false)

	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q (reason=%q)", got.State, StateMatched, got.Reason)
	}
	if len(got.Matches) != 2 {
		t.Fatalf("len(Matches) = %d, want 2", len(got.Matches))
	}
	if got.Matches[0].ID != "20260904000000" {
		t.Fatalf("Matches[0].ID = %q, want the on-topic-title item ranked first (got %+v)",
			got.Matches[0].ID, got.Matches)
	}
	if got.Matches[0].Score >= got.Matches[1].Score {
		t.Fatalf("fixture no longer exercises the case: on-topic item's printed score (%d) "+
			"must be lower than the long-body item's (%d) for this test to prove weighting, "+
			"not just raw score, decided the order", got.Matches[0].Score, got.Matches[1].Score)
	}
}
