package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// fakeEmbedServer answers /v1/embeddings by returning, for each input
// text, whatever vector questionVecs/knownVecs names it -- a real HTTP
// round trip (so this exercises Client.Embed for real, not a mock of it),
// with fully deterministic content instead of a real model's output.
func fakeEmbedServer(t *testing.T, vecFor func(text string) []float32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		type datum struct {
			Embedding []float32 `json:"embedding"`
		}
		resp := struct {
			Data []datum `json:"data"`
		}{}
		for _, in := range req.Input {
			resp.Data = append(resp.Data, datum{Embedding: vecFor(in)})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func testClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(Config{Endpoint: server.URL, Model: "m", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestNewRerankerOrdersByCosineSimilarity is the reranker's central
// correctness claim: given a question vector and per-candidate cached
// vectors, it returns ids ordered by descending cosine similarity to the
// question -- not insertion order, not BM25 order.
func TestNewRerankerOrdersByCosineSimilarity(t *testing.T) {
	// "near" is nearly parallel to the question vector; "mid" is
	// orthogonal-ish; "far" points the opposite way -- an unambiguous,
	// hand-checkable similarity ordering.
	server := fakeEmbedServer(t, func(text string) []float32 {
		return []float32{1, 0, 0} // the question always embeds to this
	})
	defer server.Close()
	client := testClient(t, server)

	cache := &Cache{
		Version: CacheVersion, Model: "m", Dimension: 3, IndexPath: "/idx",
		Items: map[string]CacheEntry{
			"far":  {TextHash: TextHash("far text"), Vector: []float32{-1, 0, 0}},
			"near": {TextHash: TextHash("near text"), Vector: []float32{0.9, 0.1, 0}},
			"mid":  {TextHash: TextHash("mid text"), Vector: []float32{0, 1, 0}},
		},
	}
	candidates := []knowledge.RerankCandidate{
		{ID: "far", Text: "far text"},
		{ID: "mid", Text: "mid text"},
		{ID: "near", Text: "near text"},
	}

	rerank := NewReranker(context.Background(), cache, client)
	ids, err := rerank("does not matter -- vecFor ignores it", candidates)
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	want := []string{"near", "mid", "far"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got %v, want %v (near-to-far cosine order)", ids, want)
		}
	}
}

// TestNewRerankerRejectsWholePopulationOnOneMissingVector is
// agent-estate#1255's "reject incomplete or invalid ranker results as a
// whole" applied to the CACHE side of the seam, not just the CLI's
// validation of what a RerankFunc returns: one candidate with no usable
// cached vector must fail the entire rerank call, never silently rank
// the others and drop the one that's missing.
func TestNewRerankerRejectsWholePopulationOnOneMissingVector(t *testing.T) {
	server := fakeEmbedServer(t, func(string) []float32 { return []float32{1, 0} })
	defer server.Close()
	client := testClient(t, server)

	cache := &Cache{
		Version: CacheVersion, Model: "m", Dimension: 2, IndexPath: "/idx",
		Items: map[string]CacheEntry{
			"present": {TextHash: TextHash("present text"), Vector: []float32{1, 0}},
			// "missing" deliberately has no entry at all.
		},
	}
	candidates := []knowledge.RerankCandidate{
		{ID: "present", Text: "present text"},
		{ID: "missing", Text: "missing text"},
	}

	rerank := NewReranker(context.Background(), cache, client)
	if _, err := rerank("q", candidates); err == nil {
		t.Fatal("rerank succeeded despite one candidate having no cached vector -- must reject the whole call")
	}
}

// TestNewRerankerRejectsStaleTextEntirely covers the same whole-rejection
// rule for a candidate whose TEXT no longer matches its cache entry's
// hash (the item's content changed since the cache was built).
func TestNewRerankerRejectsStaleTextEntirely(t *testing.T) {
	server := fakeEmbedServer(t, func(string) []float32 { return []float32{1, 0} })
	defer server.Close()
	client := testClient(t, server)

	cache := &Cache{
		Version: CacheVersion, Model: "m", Dimension: 2, IndexPath: "/idx",
		Items: map[string]CacheEntry{
			"stale": {TextHash: TextHash("the OLD text"), Vector: []float32{1, 0}},
		},
	}
	candidates := []knowledge.RerankCandidate{{ID: "stale", Text: "the NEW, changed text"}}

	rerank := NewReranker(context.Background(), cache, client)
	if _, err := rerank("q", candidates); err == nil {
		t.Fatal("rerank succeeded against a candidate whose text no longer matches its cached hash")
	}
}

// TestNewRerankerRejectsDimensionMismatch is the "malformed rank" class
// specific to embeddings: a live model returning a different dimension
// than the cache was built with must be caught, not silently produce a
// meaningless or out-of-bounds cosine computation.
func TestNewRerankerRejectsDimensionMismatch(t *testing.T) {
	server := fakeEmbedServer(t, func(string) []float32 { return []float32{1, 0, 0, 0} }) // 4-dim
	defer server.Close()
	client := testClient(t, server)

	cache := &Cache{
		Version: CacheVersion, Model: "m", Dimension: 2, IndexPath: "/idx", // cache is 2-dim
		Items: map[string]CacheEntry{
			"a": {TextHash: TextHash("a text"), Vector: []float32{1, 0}},
		},
	}
	candidates := []knowledge.RerankCandidate{{ID: "a", Text: "a text"}}

	rerank := NewReranker(context.Background(), cache, client)
	if _, err := rerank("q", candidates); err == nil {
		t.Fatal("rerank succeeded despite the live question embedding's dimension not matching the cache's own recorded dimension")
	}
}

// TestNewRerankerPropagatesQuestionEmbeddingFailure is the "server
// unreachable/timeout" fallback path, exercised through the real
// Client.Embed call this function makes for the question.
func TestNewRerankerPropagatesQuestionEmbeddingFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("simulated failure"))
	}))
	defer server.Close()
	client := testClient(t, server)

	cache := &Cache{
		Version: CacheVersion, Model: "m", Dimension: 2, IndexPath: "/idx",
		Items: map[string]CacheEntry{
			"a": {TextHash: TextHash("a text"), Vector: []float32{1, 0}},
		},
	}
	candidates := []knowledge.RerankCandidate{{ID: "a", Text: "a text"}}

	rerank := NewReranker(context.Background(), cache, client)
	if _, err := rerank("q", candidates); err == nil {
		t.Fatal("rerank succeeded despite the embedding endpoint returning 500 for the question")
	}
}

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		name     string
		a, b     []float32
		wantSign int // -1, 0, or 1 -- exact float comparison is not the point here
	}{
		{"parallel", []float32{1, 0}, []float32{2, 0}, 1},
		{"orthogonal", []float32{1, 0}, []float32{0, 1}, 0},
		{"opposite", []float32{1, 0}, []float32{-1, 0}, -1},
	}
	for _, c := range cases {
		got := cosineSimilarity(c.a, c.b)
		switch c.wantSign {
		case 1:
			if got < 0.99 {
				t.Errorf("%s: cosineSimilarity(%v, %v) = %v, want ~1", c.name, c.a, c.b, got)
			}
		case 0:
			if got > 0.01 || got < -0.01 {
				t.Errorf("%s: cosineSimilarity(%v, %v) = %v, want ~0", c.name, c.a, c.b, got)
			}
		case -1:
			if got > -0.99 {
				t.Errorf("%s: cosineSimilarity(%v, %v) = %v, want ~-1", c.name, c.a, c.b, got)
			}
		}
	}
}
