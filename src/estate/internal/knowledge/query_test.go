package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// TestQueryMatchedRanksByTermOverlap exercises BM25 ranking, not a hard
// floor (agent-estate#1054 retired minMatchedTerms): the question has
// three distinct scoreable terms ("decide" -- matched via "decided" --
// "auth", "tokens"). The auth-token-rotation fact matches all three (its
// own Tier2 uses "decided"); the corpus item shares only two of the three
// ("auth", "tokens"). Under BM25 the weaker corpus item is still a real
// candidate and is returned -- but it must rank BELOW the fact that
// matches every term, and the unrelated deploy-window fact (zero overlap)
// must not appear at all.
func TestQueryMatchedRanksByTermOverlap(t *testing.T) {
	path := writeTestIndex(t)
	got := Query(path, "what did Jon decide about auth tokens", 0, false)

	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q (reason=%q)", got.State, StateMatched, got.Reason)
	}
	if len(got.Matches) == 0 {
		t.Fatal("no matches at all")
	}
	if got.Matches[0].ID != "20260903120000" {
		t.Fatalf("Matches[0] = %+v, want the auth-token-rotation fact (all 3 terms) ranked first", got.Matches[0])
	}
	for _, m := range got.Matches {
		if m.ID == "20260903120001" {
			t.Fatalf("deploy-window fact (zero term overlap) matched an auth-token question: %+v", m)
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

// TestQueryZeroOverlapIsStillNoMatch is the baseline #1054 keeps rather
// than replaces: an item sharing NOT ONE stemmed term with the question
// scores exactly 0 and must not be returned -- this is BM25's own
// definition of "no overlap", not a minimum-count floor.
func TestQueryZeroOverlapIsStillNoMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Items: []Item{
			{
				ID: "20260903120010", Source: "github-stars",
				Permalink:   "https://github.com/apache/airflow",
				Tier1:       "apache/airflow -- Apache Airflow - A platform to programmatically author, schedule, and monitor workflows",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
		},
	}
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}

	got := Query(path, "office vending machine restocking rotation cadence", 0, false)
	if got.State != StateNoMatch {
		t.Fatalf("State = %q, want %q (matches=%+v)", got.State, StateNoMatch, got.Matches)
	}
	if len(got.Matches) != 0 {
		t.Fatalf("Matches = %+v, want none", got.Matches)
	}
}

// TestQueryNoHardFloorASingleSharedTermStillSurfaces is agent-estate#1026's
// none-01 shape (a five-term question sharing one generic word with an
// unrelated item), but proves the OPPOSITE of what the retired
// minMatchedTerms floor asserted: #1054 explicitly retires the hard
// count floor because term WEIGHTING, not exclusion, is what should keep
// a coincidental common-term match from outranking a genuine one. A
// single real (if weak and non-discriminating) term overlap is now a
// real, if low-ranked, candidate rather than being thrown away before
// ranking is ever consulted.
func TestQueryNoHardFloorASingleSharedTermStillSurfaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Items: []Item{
			{
				ID: "20260903120010", Source: "github-stars",
				Permalink:   "https://github.com/apache/airflow",
				Tier1:       "apache/airflow -- Apache Airflow - A platform to programmatically author, schedule, and monitor workflows",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
		},
	}
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}

	got := Query(path, "office vending machine restocking schedule", 0, false)
	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q -- a real single-term overlap (\"schedule\") should surface, "+
			"not be discarded by a count floor (matches=%+v, reason=%q)", got.State, StateMatched, got.Matches, got.Reason)
	}
	if len(got.Matches) != 1 || got.Matches[0].ID != "20260903120010" {
		t.Fatalf("Matches = %+v, want the one item sharing \"schedule\"", got.Matches)
	}
	if len(got.Matches[0].MatchedTerms) != 1 || got.Matches[0].MatchedTerms[0] != "schedule" {
		t.Fatalf("MatchedTerms = %+v, want exactly the stemmed \"schedule\" term", got.Matches[0].MatchedTerms)
	}
}

// TestQueryRareTermBeatsCommonTermCoincidence is #1054's own required
// property, stated directly: "a rare term matching once must be able to
// beat three common terms matching by coincidence." Five noise items
// each carry three words the corpus overall treats as common (they
// appear in most items, so BM25's idf discounts them close to zero); the
// target item carries none of those three words but does carry one rare
// term found nowhere else in the corpus. A question combining the rare
// term with the three common ones must rank the target first even though
// it matches only 1 of 4 query terms against the noise items' 3 of 4.
func TestQueryRareTermBeatsCommonTermCoincidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	items := []Item{
		{
			ID: "target", Source: "vault-fact",
			Permalink:   "/vault/agent/facts/target.md",
			Tier1:       "zzrareterm standalone finding",
			Publishable: true, PublishBasis: "test fixture: marked publishable",
		},
	}
	// Noise: "common", "words", "everywhere" appear in every one of these
	// five unrelated items plus the target's neighbours -- common enough
	// across the corpus that BM25's idf discounts them heavily, exactly
	// the coincidence #1026's none-01 case and #1054 both describe.
	for i := 0; i < 5; i++ {
		items = append(items, Item{
			ID:          fmt.Sprintf("noise-%d", i),
			Source:      "vault-fact",
			Permalink:   fmt.Sprintf("/vault/agent/facts/noise-%d.md", i),
			Tier1:       fmt.Sprintf("common words everywhere -- unrelated fact number %d", i),
			Publishable: true, PublishBasis: "test fixture: marked publishable",
		})
	}
	// A few more items also carrying "common"/"words"/"everywhere" so
	// those terms are genuinely corpus-common, not just repeated across
	// the five noise items themselves.
	for i := 5; i < 9; i++ {
		items = append(items, Item{
			ID:          fmt.Sprintf("filler-%d", i),
			Source:      "vault-fact",
			Permalink:   fmt.Sprintf("/vault/agent/facts/filler-%d.md", i),
			Tier1:       fmt.Sprintf("common words everywhere -- filler item %d", i),
			Publishable: true, PublishBasis: "test fixture: marked publishable",
		})
	}
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Items:         items,
	}
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}

	got := Query(path, "zzrareterm common words everywhere", 0, false)
	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q (reason=%q)", got.State, StateMatched, got.Reason)
	}
	if len(got.Matches) == 0 || got.Matches[0].ID != "target" {
		t.Fatalf("Matches[0] = %+v, want \"target\" (the rare-term item) ranked first ahead of the "+
			"common-term coincidences", got.Matches)
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

// TestQueryCoverageDegradedNamesTheFailedSource is the direct regression
// test for agent-estate#1058: a query answered from an index built with a
// failed source must say so in the structured Coverage field, naming which
// source, even though the answer itself (State/Matches/exit code) is
// unchanged -- this is #1058's own repro (vault-facts unreachable, a query
// that still finds two public items and returns exit 0 today with nothing
// about the vault's absence anywhere in the result).
func TestQueryCoverageDegradedNamesTheFailedSource(t *testing.T) {
	path := writeTestIndex(t) // testIndex()'s own github-stars entry is OK: false
	got := Query(path, "what did Jon decide about auth tokens", 0, false)

	if got.Coverage.State != CoverageDegraded {
		t.Fatalf("Coverage.State = %q, want %q", got.Coverage.State, CoverageDegraded)
	}
	var named bool
	for _, r := range got.Coverage.Reasons {
		if r.State == CoverageDegraded && r.Source == "github-stars" {
			named = true
			if r.Detail == "" {
				t.Error("degraded reason has an empty Detail")
			}
		}
	}
	if !named {
		t.Fatalf("Coverage.Reasons does not name the failed source github-stars: %+v", got.Coverage.Reasons)
	}
	// The answer itself must be unchanged -- #1058 says report, never fail
	// the query or change what was returned.
	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q -- a degraded source must not change the match state", got.State, StateMatched)
	}
}

// TestQueryCoverageDegradedSurvivesAGenuineNoMatch proves the degraded
// signal is independent of whether THIS question happened to match
// anything -- a source that failed at build time is a property of the
// index, not of the query.
func TestQueryCoverageDegradedSurvivesAGenuineNoMatch(t *testing.T) {
	path := writeTestIndex(t)
	got := Query(path, "kubernetes ingress rate limiting", 0, false)
	if got.State != StateNoMatch {
		t.Fatalf("State = %q, want %q", got.State, StateNoMatch)
	}
	if got.Coverage.State != CoverageDegraded {
		t.Fatalf("Coverage.State = %q, want %q on a no-match result over a degraded index", got.Coverage.State, CoverageDegraded)
	}
}

// TestQueryCoverageCompleteWhenAllSourcesOK is the negative case: an index
// built with every source OK must report Coverage.State == complete, with
// no Reasons.
func TestQueryCoverageCompleteWhenAllSourcesOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	res := testIndex()
	res.Sources = []SourceResult{
		{Name: "vault-facts", OK: true, Count: 2},
		{Name: "corpus-parameters", OK: true, Count: 1},
		{Name: "github-stars", OK: true, Count: 5},
	}
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}
	got := Query(path, "what did Jon decide about auth tokens", 0, false)
	if got.Coverage.State != CoverageComplete {
		t.Fatalf("Coverage.State = %q, want %q", got.Coverage.State, CoverageComplete)
	}
	if len(got.Coverage.Reasons) != 0 {
		t.Fatalf("Coverage.Reasons = %+v, want none on a fully-OK index", got.Coverage.Reasons)
	}
}

// TestQueryCarriesIndexGeneratedByForward is agent-estate#1082's own
// reproduction against Query: a compiled index carrying a real
// GeneratedBy must have that same value on QueryResult.IndexGeneratedBy,
// unchanged -- Query never recomputes it, only carries it forward, the
// same discipline SourceStatuses/IndexGeneratedAt already use.
func TestQueryCarriesIndexGeneratedByForward(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	res := testIndex()
	res.GeneratedBy = GeneratedBy{
		Commit:  "abc123def4567890abc123def4567890abc123d",
		BuiltAt: res.GeneratedAt,
	}
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}
	got := Query(path, "what did Jon decide about auth tokens", 0, false)
	if got.IndexGeneratedBy.Commit != "abc123def4567890abc123def4567890abc123d" {
		t.Fatalf("IndexGeneratedBy.Commit = %q, want the index's own recorded commit", got.IndexGeneratedBy.Commit)
	}
}

// TestQueryCoverageLimitedOnWithheldPrivate folds #1055's own
// StateMatchedWithheldMajority into the same Coverage structure --
// agent-estate#1058's sequencing note: this must reuse the shape #1055
// settled on, not invent a second one.
func TestQueryCoverageLimitedOnWithheldPrivate(t *testing.T) {
	path := withheldMajorityIndex(t, 2) // no Sources set -- all-OK, so complete would otherwise apply
	got := Query(path, "credential rotation keychain policy", 0, false)

	if got.State != StateMatchedWithheldMajority {
		t.Fatalf("State = %q, want %q", got.State, StateMatchedWithheldMajority)
	}
	if got.Coverage.State != CoverageLimited {
		t.Fatalf("Coverage.State = %q, want %q", got.Coverage.State, CoverageLimited)
	}
	var named bool
	for _, r := range got.Coverage.Reasons {
		if r.State == CoverageLimited && r.Detail != "" {
			named = true
		}
	}
	if !named {
		t.Fatalf("Coverage.Reasons does not carry a limited reason: %+v", got.Coverage.Reasons)
	}
}

// TestQueryCoverageMixedWhenSourceFailedAndPrivacyWithheld is the co-occur
// case the issue's council comment calls out explicitly: a degraded source
// AND a policy withholding on the SAME result must both be visible under
// one top-level state (mixed), not silently collapse to just one of them.
func TestQueryCoverageMixedWhenSourceFailedAndPrivacyWithheld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Sources: []SourceResult{
			{Name: "vault-facts", OK: false, Reason: "cannot list /nonexistent/vault/agent/facts"},
		},
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
	if got.Coverage.State != CoverageMixed {
		t.Fatalf("Coverage.State = %q, want %q", got.Coverage.State, CoverageMixed)
	}
	var sawDegraded, sawLimited bool
	for _, r := range got.Coverage.Reasons {
		switch r.State {
		case CoverageDegraded:
			sawDegraded = true
		case CoverageLimited:
			sawLimited = true
		}
	}
	if !sawDegraded || !sawLimited {
		t.Fatalf("Coverage.Reasons missing degraded and/or limited: %+v", got.Coverage.Reasons)
	}
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

// fieldWeightIndex reproduces #1043's review finding, isolated from term
// COUNT so it tests field weight alone: both items match exactly the
// same three question terms -- one item carries them in Tier1 (its
// title), the other carries the identical three words only in Tier2 (a
// long, otherwise-unrelated body) -- #1027 compiled full vault fact
// bodies into Tier2, and an unweighted matched-term count let a body's
// incidental mention outrank an on-topic title purely on raw presence.
// Under BM25 field weighting (agent-estate#1054) the Tier1 item must
// still score higher for the SAME term overlap, because a term found in
// Tier1 counts for more of that item's term frequency than the same term
// found only in Tier2.
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
				Permalink: "/vault/agent/facts/unrelated-title-body-only-match.md",
				Tier1:     "an unrelated fact about deploy windows",
				Tier2: "this body happens to mention tmux, and separately a " +
					"session, and later a keychain -- the same three terms as " +
					"the on-topic item, none of them in this item's own title",
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

// TestQueryUnknownTagFilterDistinguishesFailedSourceFromTypo is
// agent-estate#1120's own correction: "unknown tag(s): ... -- not present
// in the compiled index" reads as "no such source exists" even when the
// tag names a source that tried to build and failed -- the truth in that
// case is the opposite of a typo, and a caller acting on the wrong
// reading fixes the wrong thing. A tag naming a source that is actually
// absent from res.Sources (a genuine typo) must keep the original
// wording; a tag naming a source recorded !OK in res.Sources must name
// the failure instead.
func TestQueryUnknownTagFilterDistinguishesFailedSourceFromTypo(t *testing.T) {
	res := Result{
		GeneratedAt: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
		Sources: []SourceResult{
			{Name: "vault-facts", OK: false, Reason: "cannot list /nonexistent/agent/facts: no such file or directory"},
			{Name: "repo-docs", OK: true, Count: 1},
		},
		Items: []Item{
			{
				ID: "20260904000010", Source: "repo-docs",
				Permalink:      "docs/example.md",
				StructuralTags: []string{"source:repo-docs"},
				Tier1:          "an unrelated repo-docs item, present so the index is not empty",
				Publishable:    true, PublishBasis: "test fixture: marked publishable",
			},
		},
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}

	// The failed-source case: source:vault-fact names vault-facts, which
	// is recorded !OK -- no item ever carried this tag because the
	// source that would have produced it never built.
	failed := Query(path, "source:vault-fact something", 0, true)
	if failed.State != StateNoMatch {
		t.Fatalf("State = %q, want %q", failed.State, StateNoMatch)
	}
	if !strings.Contains(failed.Reason, "vault-facts") || !strings.Contains(failed.Reason, "failed to build") {
		t.Fatalf("Reason = %q, want it to name the failed source vault-facts", failed.Reason)
	}
	if !strings.Contains(failed.Reason, "no such file or directory") {
		t.Fatalf("Reason = %q, want it to carry the source's own failure detail", failed.Reason)
	}

	// The genuine-typo case: no source, live or failed, is named
	// anything resembling this -- the original wording must survive
	// unchanged.
	typo := Query(path, "source:not-a-real-source something", 0, true)
	if typo.State != StateNoMatch {
		t.Fatalf("State = %q, want %q", typo.State, StateNoMatch)
	}
	if !strings.Contains(typo.Reason, "not present in the compiled index") {
		t.Fatalf("Reason = %q, want the original typo wording preserved", typo.Reason)
	}
	if strings.Contains(typo.Reason, "failed to build") {
		t.Fatalf("Reason = %q, a genuine typo must never be reported as a failed source", typo.Reason)
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
// field-weighted ranking fix, isolated from term count (see
// fieldWeightIndex): both items match exactly the same 3 of the
// question's 3 scoreable terms, one in Tier1, the other only in Tier2 --
// the on-topic item must still rank first and score strictly higher,
// because BM25's own field weight (tier1FieldWeight, bm25.go) makes a
// Tier1 hit worth more of that item's term frequency than the identical
// term found only in Tier2. Unlike the old hierarchical sort this
// replaced, that field-weight advantage is additive, not absolute -- a
// large enough term-COUNT advantage for the Tier2-only item could still
// outweigh it (see TestQueryRareTermBeatsCommonTermCoincidence for the
// property BM25 is actually required to hold), so this fixture keeps
// term count equal on purpose to test field weight alone.
func TestQueryRanksATier1MatchAboveALongerTier2OnlyMatch(t *testing.T) {
	path := fieldWeightIndex(t)
	got := Query(path, "tmux session keychain", 0, false)

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
	if got.Matches[0].Score <= got.Matches[1].Score {
		t.Fatalf("fixture no longer exercises the case: on-topic item's printed score (%d) "+
			"must be strictly higher than the Tier2-only item's (%d) for this test to prove "+
			"field weighting, not term count, decided the order", got.Matches[0].Score, got.Matches[1].Score)
	}
}

// TestQueryParaphraseSurvivesTheRetiredFloor is agent-estate#1054's own
// traced case, reproduced as a fixture: a target item about stale model
// listings, queried with its own words ("models listed stale refreshed")
// scores well and ranks first; queried with a realistic paraphrase ("are
// the model names up to date") the OLD scorer matched only "model" as a
// substring of "models" (score 1) against requiredScore's floor of 3, so
// the target was excluded BEFORE ranking was ever consulted, while two
// unrelated items sharing "model"/"names"/"date" as separate incidental
// words scored 3 and were returned instead. This is confirmed directly
// against a3c389d (the pre-#1054 commit) in the PR body's before/after
// transcript -- the same fixture, run against the OLD Query, excludes
// "target" from paraphrase.Matches entirely. BM25 has no such floor: the
// target is now a real, nonzero-scoring candidate and must be returned,
// even though ranking it strictly ahead of the two incidental matches
// depends on this corpus's actual idf distribution and is a stronger,
// separate claim -- see TestQueryRareTermBeatsCommonTermCoincidence for
// the fixture that isolates and proves THAT property directly.
func TestQueryParaphraseSurvivesTheRetiredFloor(t *testing.T) {
	items := []Item{
		{
			ID: "target", Source: "vault-fact",
			Permalink:   "/vault/agent/facts/models-stale.md",
			Tier1:       "models listed stale -- refresh required",
			Tier2:       "The models list goes stale after 30 days and must be refreshed by hand.",
			Publishable: true, PublishBasis: "test fixture: marked publishable",
		},
		{
			ID: "unrelated-1", Source: "vault-fact",
			Permalink:   "/vault/agent/facts/unrelated1.md",
			Tier1:       "date names for the release model",
			Tier2:       "an unrelated fact that happens to use the words model, names and date incidentally",
			Publishable: true, PublishBasis: "test fixture: marked publishable",
		},
		{
			ID: "unrelated-2", Source: "vault-fact",
			Permalink:   "/vault/agent/facts/unrelated2.md",
			Tier1:       "another unrelated fact",
			Tier2:       "this one names a date and a model too, purely by coincidence",
			Publishable: true, PublishBasis: "test fixture: marked publishable",
		},
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := Write(path, Result{Items: items}); err != nil {
		t.Fatal(err)
	}

	own := Query(path, "models listed stale refreshed", 0, false)
	if own.State != StateMatched || len(own.Matches) == 0 || own.Matches[0].ID != "target" {
		t.Fatalf("own-words query: got %+v, want target ranked first", own.Matches)
	}

	paraphrase := Query(path, "are the model names up to date", 0, false)
	if paraphrase.State != StateMatched {
		t.Fatalf("paraphrase query: State = %q, want %q (reason=%q)", paraphrase.State, StateMatched, paraphrase.Reason)
	}
	var foundTarget bool
	for _, m := range paraphrase.Matches {
		if m.ID == "target" {
			foundTarget = true
		}
	}
	if !foundTarget {
		t.Fatalf("paraphrase query: Matches = %+v, want \"target\" present -- "+
			"this is the exact case the retired requiredScore floor excluded before ranking ever ran", paraphrase.Matches)
	}
}

// TestCoverageWithFreshnessReasonComposesLikeLimitedAndDegraded is the
// direct regression test for agent-estate#1080: WithFreshnessReason must
// fold CoverageStale/CoverageUnknownFreshness into Coverage using the
// exact same compose-to-mixed rule withLimitedReason already established
// for CoverageLimited/CoverageDegraded, never a differently-shaped signal.
func TestCoverageWithFreshnessReasonComposesLikeLimitedAndDegraded(t *testing.T) {
	// Complete -> Stale outright, naming the source.
	stale := Coverage{State: CoverageComplete}.WithFreshnessReason(CoverageStale, "agent-memory-vault", "changed 3m ago")
	if stale.State != CoverageStale {
		t.Fatalf("Complete + stale reason: State = %q, want %q", stale.State, CoverageStale)
	}
	if len(stale.Reasons) != 1 || stale.Reasons[0].Source != "agent-memory-vault" {
		t.Fatalf("stale.Reasons = %+v, want one reason naming agent-memory-vault", stale.Reasons)
	}

	// Complete -> UnknownFreshness outright, naming the source.
	unknown := Coverage{State: CoverageComplete}.WithFreshnessReason(CoverageUnknownFreshness, "github-stars", "no local file to stat")
	if unknown.State != CoverageUnknownFreshness {
		t.Fatalf("Complete + unknown reason: State = %q, want %q", unknown.State, CoverageUnknownFreshness)
	}
	// CoverageUnknownFreshness must never be mistaken for CoverageComplete
	// -- #1080's governing rule, checked structurally, not just by prose.
	if unknown.State == CoverageComplete {
		t.Fatalf("unknown freshness collapsed into CoverageComplete -- absence of evidence must never render as fresh")
	}

	// A second stale finding on an already-stale Coverage stays Stale, not
	// Mixed -- two causes of the SAME kind is not "more than one kind".
	twoStale := stale.WithFreshnessReason(CoverageStale, "loops-research", "changed 1h ago")
	if twoStale.State != CoverageStale {
		t.Fatalf("Stale + another stale reason: State = %q, want %q to stay", twoStale.State, CoverageStale)
	}
	if len(twoStale.Reasons) != 2 {
		t.Fatalf("twoStale.Reasons = %+v, want 2", twoStale.Reasons)
	}

	// Stale + UnknownFreshness on the same result -> Mixed, both causes
	// still individually visible in Reasons.
	mixed := stale.WithFreshnessReason(CoverageUnknownFreshness, "github-stars", "no local file to stat")
	if mixed.State != CoverageMixed {
		t.Fatalf("Stale + unknown reason: State = %q, want %q", mixed.State, CoverageMixed)
	}
	var sawStale, sawUnknown bool
	for _, r := range mixed.Reasons {
		switch r.State {
		case CoverageStale:
			sawStale = true
		case CoverageUnknownFreshness:
			sawUnknown = true
		}
	}
	if !sawStale || !sawUnknown {
		t.Fatalf("mixed.Reasons missing stale and/or unknown: %+v", mixed.Reasons)
	}

	// A freshness reason folded onto an already-Degraded/Limited Coverage
	// becomes Mixed too, exactly as Degraded+Limited already does.
	degraded := Coverage{State: CoverageDegraded, Reasons: []CoverageReason{{State: CoverageDegraded, Source: "vault-facts", Detail: "unreadable"}}}
	degradedThenStale := degraded.WithFreshnessReason(CoverageStale, "agent-memory-vault", "changed 3m ago")
	if degradedThenStale.State != CoverageMixed {
		t.Fatalf("Degraded + stale reason: State = %q, want %q", degradedThenStale.State, CoverageMixed)
	}
}
