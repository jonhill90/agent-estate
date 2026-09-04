package knowledge

import (
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
