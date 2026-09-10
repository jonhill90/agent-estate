package embedding

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// TestRealDeploymentSemanticQueryEndToEnd is agent-estate#1255's own
// "real deployment path" check: warm a private cache against a REAL
// local model, then run knowledge.QueryWithRanking through the exact same
// production pieces main.go's --semantic wiring uses (Client, Cache,
// NewReranker), and confirm it actually reorders a real question's real
// candidates -- not a synthetic vector, not a fixture.
//
// Opt-in on ESTATE_RANKING_EXPERIMENT_INDEX (the same private-index
// convention #1338/#1344 established) and a reachable local embedding
// endpoint. SKIPS LOUDLY, never fails, when either is absent: this run
// checks nothing without them, same discipline as
// TestSemanticCeilingExperiment (internal/knowledge) and
// internal/corpus/standinglaw_live_test.go's liveVaultOrSkip. CI has no
// LM Studio and must never depend on this test passing.
func TestRealDeploymentSemanticQueryEndToEnd(t *testing.T) {
	idxPath := os.Getenv("ESTATE_RANKING_EXPERIMENT_INDEX")
	if idxPath == "" {
		t.Skip("ESTATE_RANKING_EXPERIMENT_INDEX not set -- SKIPPING, not passing: this run checks nothing. See agent-estate#1255.")
	}
	cfg := NewConfig()
	if v := os.Getenv("ESTATE_EMBEDDING_ENDPOINT"); v != "" {
		cfg.Endpoint = strings.TrimSuffix(v, "/")
	}
	if v := os.Getenv("ESTATE_EMBEDDING_MODEL"); v != "" {
		cfg.Model = v
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Skip("could not build an embedding client (" + err.Error() + ") -- SKIPPING, not passing: this run checks nothing.")
	}
	if _, err := client.Embed(context.Background(), []string{"connectivity probe"}); err != nil {
		t.Skip("no reachable local embedding endpoint at " + cfg.Endpoint + " with model " + cfg.Model +
			" -- SKIPPING, not passing: this run checks nothing. See agent-estate#1255.")
	}

	cachePath := CachePath(idxPath)
	if _, statErr := os.Stat(cachePath); statErr != nil {
		t0 := time.Now()
		n, perr := PrepareCache(context.Background(), idxPath, cfg)
		if perr != nil {
			t.Fatalf("PrepareCache: %v", perr)
		}
		t.Logf("prepared a fresh cache: %d item(s) in %s", n, time.Since(t0).Round(time.Millisecond))
	} else {
		t.Logf("reusing existing cache at %s", cachePath)
	}

	cache, cerr := LoadOrNil(cachePath, idxPath, cfg.Model)
	if cerr != nil {
		t.Fatalf("cache did not validate against the index it was just built for: %v", cerr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rerank := NewReranker(ctx, cache, client)

	question := "how does the estate decide whether a pull request may merge"
	bm25 := knowledge.Query(idxPath, question, 0, true)
	// StateMatchedWeak/StateMatchedWithheldMajority still carry real,
	// non-empty Matches -- ranking applies exactly the same regardless of
	// which of the three states a real, live index happens to land on for
	// a given probe question. Only an EMPTY result means this question
	// exercises nothing.
	if len(bm25.Matches) == 0 {
		t.Skipf("this question produced no matches at all on the private index (state=%s) -- pick a different probe question to exercise the real path", bm25.State)
	}
	semantic := knowledge.QueryWithRanking(idxPath, question, 0, true, rerank)

	if semantic.RankingMethod != "semantic" {
		t.Fatalf("RankingMethod = %q, want %q against a real, freshly-validated cache and a reachable model -- fallback reason: %s",
			semantic.RankingMethod, "semantic", semantic.RankingFallbackReason)
	}
	if len(semantic.Matches) != len(bm25.Matches) {
		t.Errorf("semantic path returned %d match(es), BM25 returned %d -- reordering must not change the count", len(semantic.Matches), len(bm25.Matches))
	}
	t.Logf("BM25 top match: [%s] %s", bm25.Matches[0].ID, bm25.Matches[0].Tier1)
	t.Logf("semantic top match: [%s] %s", semantic.Matches[0].ID, semantic.Matches[0].Tier1)

	// disable/server-absent comparison: a client pointed at a server that
	// is not there must fall back to identical BM25 output, proven against
	// the SAME real index and question this test just used, not a fixture.
	deadClient, err := NewClient(Config{Endpoint: "http://127.0.0.1:1", Model: cfg.Model, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewClient for the deliberately-absent server: %v", err)
	}
	deadRerank := NewReranker(context.Background(), cache, deadClient)
	fallback := knowledge.QueryWithRanking(idxPath, question, 0, true, deadRerank)
	if fallback.RankingMethod != "bm25" {
		t.Errorf("RankingMethod = %q against an absent server, want %q", fallback.RankingMethod, "bm25")
	}
	if fallback.RankingFallbackReason == "" {
		t.Error("no RankingFallbackReason set against an absent server")
	}
	if len(fallback.Matches) != len(bm25.Matches) {
		t.Fatal("fallback against an absent server did not reproduce BM25's own match count exactly")
	}
	for i := range fallback.Matches {
		if fallback.Matches[i].ID != bm25.Matches[i].ID {
			t.Fatalf("fallback Matches[%d] = %s, want BM25's own %s -- exact fallback failed", i, fallback.Matches[i].ID, bm25.Matches[i].ID)
		}
	}
}
