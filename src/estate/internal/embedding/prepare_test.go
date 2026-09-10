package embedding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

func writeTestKnowledgeIndex(t *testing.T, items []knowledge.Item) string {
	t.Helper()
	res := knowledge.Result{
		GeneratedAt: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC),
		Items:       items,
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := knowledge.Write(path, res); err != nil {
		t.Fatalf("writing test index: %v", err)
	}
	return path
}

// TestPrepareCacheEmbedsEveryItemAndWritesASidecar is PrepareCache's own
// central claim: every item in the index gets a cached vector, keyed by
// its own text hash, at CachePath(indexPath) -- and the index file
// itself is untouched (PrepareCache only ever reads it).
func TestPrepareCacheEmbedsEveryItemAndWritesASidecar(t *testing.T) {
	items := []knowledge.Item{
		{ID: "a", Tier1: "alpha", Tier2: "first item", Publishable: true},
		{ID: "b", Tier1: "bravo", Tier2: "second item", Publishable: true},
	}
	indexPath := writeTestKnowledgeIndex(t, items)
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	server := fakeEmbedServer(t, func(text string) []float32 { return []float32{float32(len(text)), 1, 2} })
	defer server.Close()
	cfg := Config{Endpoint: server.URL, Model: "m", Timeout: 5 * time.Second}

	n, err := PrepareCache(context.Background(), indexPath, cfg)
	if err != nil {
		t.Fatalf("PrepareCache: %v", err)
	}
	if n != len(items) {
		t.Errorf("PrepareCache embedded %d item(s), want %d", n, len(items))
	}

	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the compiled index itself changed -- PrepareCache must only READ it")
	}

	cache, reason := Load(CachePath(indexPath), indexPath, cfg.Model)
	if cache == nil {
		t.Fatalf("Load of the freshly-written cache failed: %s", reason)
	}
	for _, it := range items {
		text := itemText(it)
		if _, ok := cache.Lookup(it.ID, text); !ok {
			t.Errorf("item %q has no valid cached vector after PrepareCache", it.ID)
		}
	}
}

// TestPrepareCacheRejectsAnEmptyIndex is a cheap guard: nothing to embed
// is a build defect worth naming, not a silently-written empty cache.
func TestPrepareCacheRejectsAnEmptyIndex(t *testing.T) {
	indexPath := writeTestKnowledgeIndex(t, nil)
	server := fakeEmbedServer(t, func(string) []float32 { return []float32{1} })
	defer server.Close()
	cfg := Config{Endpoint: server.URL, Model: "m", Timeout: 5 * time.Second}

	if _, err := PrepareCache(context.Background(), indexPath, cfg); err == nil {
		t.Fatal("PrepareCache succeeded against an index with 0 items")
	}
}

// TestPrepareCacheBatches confirms more items than one batch (batchSize)
// still all get embedded -- not silently truncated to the first batch.
func TestPrepareCacheBatches(t *testing.T) {
	const n = batchSize + 3
	items := make([]knowledge.Item, n)
	for i := range items {
		items[i] = knowledge.Item{ID: itemIDFor(i), Tier1: "item", Tier2: "text", Publishable: true}
	}
	indexPath := writeTestKnowledgeIndex(t, items)
	server := fakeEmbedServer(t, func(string) []float32 { return []float32{1, 2} })
	defer server.Close()
	cfg := Config{Endpoint: server.URL, Model: "m", Timeout: 30 * time.Second}

	got, err := PrepareCache(context.Background(), indexPath, cfg)
	if err != nil {
		t.Fatalf("PrepareCache: %v", err)
	}
	if got != n {
		t.Errorf("PrepareCache embedded %d item(s) across %d batch(es), want %d", got, (n+batchSize-1)/batchSize, n)
	}
}

func itemIDFor(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	// Cheap, collision-free-enough id generator for a test fixture only.
	b := []byte{letters[i%36], letters[(i/36)%36], letters[(i/1296)%36]}
	return string(b)
}
