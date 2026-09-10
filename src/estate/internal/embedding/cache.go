package embedding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CacheVersion is bumped whenever this file's own on-disk shape changes in
// a way old data cannot satisfy -- a version mismatch is treated exactly
// like a missing cache (agent-estate#1255: "missing, stale or corrupt cache
// falls back", never a partial or best-effort read of an old shape).
const CacheVersion = 1

// CacheEntry is one item's cached vector, plus enough to detect staleness
// without re-embedding to check: TextHash is sha256 of the exact text that
// produced Vector, so a later item whose Tier1/Tier2 changed is caught
// without comparing full text strings on every load.
type CacheEntry struct {
	TextHash string    `json:"text_hash"`
	Vector   []float32 `json:"vector"`
}

// Cache is the versioned sidecar `estate knowledge embeddings` writes and
// the reranker reads. Every field participates in validity: a cache built
// for a different model, a different vector dimension, an older format
// version, or a different source index is treated as absent, not
// partially reused (agent-estate#1255's "reject incomplete or invalid
// ranker results as a whole" applies to the cache itself, not just a
// single rerank call).
type Cache struct {
	Version   int                   `json:"version"`
	Model     string                `json:"model"`
	Dimension int                   `json:"dimension"`
	IndexPath string                `json:"index_path"`
	Items     map[string]CacheEntry `json:"items"`
}

// TextHash hashes exactly the field the reranker embeds and compares
// against -- Tier1+". "+Tier2, the SAME pair BM25's own searchableText
// concatenates (bm25.go) and #1344's own measurement used, so "the cached
// vector no longer matches this item's text" means the identical text a
// human would recognise as changed, not some other derived form.
func TextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// CachePath derives the sidecar path from the compiled index path it
// belongs beside: <indexPath>.embeddings.json. Deriving it rather than
// taking a second independent path argument everywhere means a cache can
// never silently point at a DIFFERENT index than the one a caller is
// actually querying -- the two paths are structurally the same index,
// never two configuration values that could drift apart.
func CachePath(indexPath string) string {
	return indexPath + ".embeddings.json"
}

// Load reads and validates a cache against the expected model and index
// path. Dimension is NOT checked here -- it is validated lazily, against
// the live question vector, the first time this cache is actually used to
// rerank (see NewReranker) -- querying the model just to learn its
// dimension before every load would be a second network round trip this
// package does not otherwise need. A missing file, unreadable file,
// corrupt JSON, or a model/index/emptiness mismatch returns a plain (nil,
// reason) -- never an error a caller has to distinguish from "works but
// empty"; see LoadOrNil for the fallback-shaped convenience most callers
// actually want.
func Load(path, indexPath, model string) (*Cache, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "no embedding cache at " + path + " -- run `estate knowledge embeddings` first"
		}
		return nil, "could not read embedding cache: " + err.Error()
	}
	var c Cache
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, "embedding cache is corrupt (not valid JSON): " + err.Error()
	}
	if c.Version != CacheVersion {
		return nil, fmt.Sprintf("embedding cache version %d does not match the current version %d -- rebuild with `estate knowledge embeddings`", c.Version, CacheVersion)
	}
	if c.Model != model {
		return nil, fmt.Sprintf("embedding cache was built for model %q, current model is %q -- rebuild with `estate knowledge embeddings`", c.Model, model)
	}
	if c.IndexPath != indexPath {
		return nil, fmt.Sprintf("embedding cache was built for index %q, current index is %q -- rebuild with `estate knowledge embeddings`", c.IndexPath, indexPath)
	}
	if len(c.Items) == 0 {
		return nil, "embedding cache has zero cached items -- rebuild with `estate knowledge embeddings`"
	}
	return &c, ""
}

// LoadOrNil is Load with the two-value "cache or reason" shape collapsed
// to a single err -- the shape most call sites (the reranker seam, CLI
// disclosure) want: nil, err means fall back to BM25 and say why.
func LoadOrNil(path, indexPath, model string) (*Cache, error) {
	c, reason := Load(path, indexPath, model)
	if c == nil {
		return nil, fmt.Errorf("%s", reason)
	}
	return c, nil
}

// Lookup returns the cached vector for id, only if its TextHash matches
// currentText's own hash -- a cache entry whose item text has since
// changed is exactly as invalid as a missing one, never returned as if it
// still described the current item.
func (c *Cache) Lookup(id, currentText string) ([]float32, bool) {
	e, ok := c.Items[id]
	if !ok {
		return nil, false
	}
	if e.TextHash != TextHash(currentText) {
		return nil, false
	}
	return e.Vector, true
}

// Save writes c atomically: a temp file in the SAME directory (so the
// final rename is on one filesystem, never a cross-device copy that could
// itself be interrupted), private permissions throughout, then rename over
// the destination. A process killed mid-write leaves either the old cache
// file untouched or nothing at the temp path -- never a half-written file
// visible at the real path (agent-estate#1255: "atomic private writes").
func (c *Cache) Save(path string) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".embeddings-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if anything below fails before the rename --
	// once renamed, tmpPath no longer exists, so this is a no-op then.
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
