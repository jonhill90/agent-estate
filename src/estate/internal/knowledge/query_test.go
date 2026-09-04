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
			},
			{
				ID: "20260903120001", Source: "vault-fact",
				Permalink: "/vault/agent/facts/deploy-window.md",
				Tier1:     "deploy window -- weekday mornings only",
				Tier2:     "Deploys happen weekday mornings only, never a Friday.",
				Tier3:     "open /vault/agent/facts/deploy-window.md for the full fact",
			},
			{
				ID: "20260903120002", Source: "corpus-parameter",
				Permalink:      "corpus:item:4821",
				StructuralTags: []string{"weight:hard"},
				Tier1:          "auth must use short-lived tokens, never long-lived keys",
				Tier2:          "auth must use short-lived tokens, never long-lived keys",
				Tier3:          "the corpus's own item 4821 (live_parameters) -- not this file",
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

func TestQueryMatchedRanksByTermOverlap(t *testing.T) {
	path := writeTestIndex(t)
	got := Query(path, "what did Jon decide about auth tokens", 0)

	if got.State != StateMatched {
		t.Fatalf("State = %q, want %q (reason=%q)", got.State, StateMatched, got.Reason)
	}
	if got.TotalMatched != 2 {
		t.Fatalf("TotalMatched = %d, want 2", got.TotalMatched)
	}
	if len(got.Matches) != 2 {
		t.Fatalf("len(Matches) = %d, want 2", len(got.Matches))
	}
	// Both auth items should outrank the unrelated deploy-window fact,
	// which must not appear at all (score 0 -- "auth"/"tokens" don't
	// occur in its text).
	for _, m := range got.Matches {
		if m.ID == "20260903120001" {
			t.Fatalf("deploy-window fact matched an auth-token question: %+v", m)
		}
	}
	if got.RankingBasis == "" {
		t.Error("RankingBasis is empty -- ranking basis must be legible")
	}
}

func TestQueryStatesNotReturnedCount(t *testing.T) {
	path := writeTestIndex(t)
	got := Query(path, "auth tokens", 1)
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
	got := Query(path, "kubernetes ingress rate limiting", 0)
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

func TestQueryIndexMissingIsTypedSeparatelyFromNoMatch(t *testing.T) {
	got := Query(filepath.Join(t.TempDir(), "never-generated.json"), "auth tokens", 0)
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
	got := Query(path, "auth tokens", 0)
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
	got := Query(path, "auth tokens deploy window", 0)
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
	item, ok, reason := Get(path, "20260903120000")
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
	_, ok, reason := Get(path, "no-such-id")
	if ok {
		t.Fatal("Get() ok=true for an id that was never in the index")
	}
	if reason == "" {
		t.Error("Get() gave no reason for an unknown id")
	}
}

func TestGetMissingIndexIsTypedSeparately(t *testing.T) {
	_, ok, reason := Get(filepath.Join(t.TempDir(), "never-generated.json"), "20260903120000")
	if ok {
		t.Fatal("Get() ok=true against a never-generated index")
	}
	if reason == "" {
		t.Error("Get() gave no reason for a missing index")
	}
}
