package embedding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// batchSize bounds how many texts one /embeddings call carries -- a local
// server handles a few hundred comfortably; this keeps any one request's
// size bounded rather than sending several thousand inputs at once.
const batchSize = 500

// bulkBatchTimeout bounds ONE batch of batchSize items, not the whole
// preparation run (which is many such batches in sequence, and can
// legitimately run for minutes over a large corpus -- #1344's own
// measurement took ~2.5 minutes over ~7,700 items). Deliberately its OWN
// constant, never cfg.Timeout: a caller's Config.Timeout is the
// QUERY-time budget (one short question, main.go's
// semanticQueryTimeout) -- reusing it here was tried first and measured
// to fail for real against a real corpus (a 500-item batch routinely
// exceeds 10s), which is exactly the class of bug
// TestRealDeploymentSemanticQueryEndToEnd exists to catch before a caller
// hits it in production.
const bulkBatchTimeout = 2 * time.Minute

// PrepareCache embeds every item in the compiled index at indexPath and
// writes a versioned sidecar cache next to it (CachePath). This is the
// ONLY place in this package that embeds more than one text at a time, and
// it runs only when a caller explicitly invokes it (`estate knowledge
// embeddings`) -- never as a side effect of a query. The compiled index
// itself is read-only throughout; nothing here writes to indexPath.
//
// cfg.Timeout is ignored here on purpose -- see bulkBatchTimeout's own
// doc comment; every other Config field (Endpoint, Model) is used as
// given.
func PrepareCache(ctx context.Context, indexPath string, cfg Config) (itemCount int, err error) {
	res, err := knowledge.Read(indexPath)
	if err != nil {
		return 0, fmt.Errorf("reading index %s: %w", indexPath, err)
	}
	if len(res.Items) == 0 {
		return 0, fmt.Errorf("index %s has 0 items -- nothing to embed", indexPath)
	}

	bulkCfg := cfg
	bulkCfg.Timeout = bulkBatchTimeout
	client, err := NewClient(bulkCfg)
	if err != nil {
		return 0, err
	}

	ids := make([]string, 0, len(res.Items))
	texts := make([]string, 0, len(res.Items))
	for _, it := range res.Items {
		ids = append(ids, it.ID)
		texts = append(texts, itemText(it))
	}

	items := make(map[string]CacheEntry, len(ids))
	var dimension int
	for start := 0; start < len(ids); start += batchSize {
		end := start + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		vecs, berr := client.Embed(ctx, texts[start:end])
		if berr != nil {
			return 0, fmt.Errorf("embedding batch [%d:%d] of %d: %w", start, end, len(ids), berr)
		}
		for i, v := range vecs {
			if dimension == 0 {
				dimension = len(v)
			} else if len(v) != dimension {
				return 0, fmt.Errorf("embedding endpoint returned inconsistent vector dimensions (%d then %d) within one cache build -- refusing a mixed-dimension cache", dimension, len(v))
			}
			id := ids[start+i]
			items[id] = CacheEntry{TextHash: TextHash(texts[start+i]), Vector: v}
		}
	}

	c := &Cache{
		Version:   CacheVersion,
		Model:     cfg.Model,
		Dimension: dimension,
		IndexPath: indexPath,
		Items:     items,
	}
	if err := c.Save(CachePath(indexPath)); err != nil {
		return 0, fmt.Errorf("writing embedding cache: %w", err)
	}
	return len(items), nil
}

// itemText is the exact field pair BM25's own searchableText concatenates
// (bm25.go) and #1344's own ceiling measurement embedded -- the fairest
// comparison available: the reranker sees the same content BM25 does, not
// a richer or poorer view of the item.
func itemText(it knowledge.Item) string {
	return strings.TrimSpace(it.Tier1 + ". " + it.Tier2)
}
