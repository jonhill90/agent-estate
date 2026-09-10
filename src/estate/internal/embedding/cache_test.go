package embedding

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCache(t *testing.T, c *Cache) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.json.embeddings.json")
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return path
}

func validCache() *Cache {
	return &Cache{
		Version:   CacheVersion,
		Model:     "text-embedding-nomic-embed-text-v1.5",
		Dimension: 3,
		IndexPath: "/private/tmp/example/index.json",
		Items: map[string]CacheEntry{
			"it-1": {TextHash: TextHash("hello. world"), Vector: []float32{0.1, 0.2, 0.3}},
		},
	}
}

// TestLoadAcceptsAValidCache is the baseline: a cache built for the
// exact model and index path it is loaded against must succeed.
func TestLoadAcceptsAValidCache(t *testing.T) {
	c := validCache()
	path := writeCache(t, c)
	got, reason := Load(path, c.IndexPath, c.Model)
	if got == nil {
		t.Fatalf("Load rejected a valid cache: %s", reason)
	}
	if len(got.Items) != 1 {
		t.Errorf("loaded %d item(s), want 1", len(got.Items))
	}
}

// TestLoadRejectsMissingFile is agent-estate#1255's "missing... cache
// falls back" requirement, checked first.
func TestLoadRejectsMissingFile(t *testing.T) {
	got, reason := Load(filepath.Join(t.TempDir(), "does-not-exist.json"), "/anything", "any-model")
	if got != nil {
		t.Fatal("Load succeeded against a nonexistent file")
	}
	if reason == "" {
		t.Error("Load gave no reason for a missing cache")
	}
}

// TestLoadRejectsCorruptJSON is the "corrupt cache falls back" case.
func TestLoadRejectsCorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, reason := Load(path, "/anything", "any-model")
	if got != nil {
		t.Fatal("Load succeeded against corrupt JSON")
	}
	if reason == "" {
		t.Error("Load gave no reason for corrupt JSON")
	}
}

// TestLoadRejectsVersionMismatch, TestLoadRejectsModelMismatch,
// TestLoadRejectsIndexPathMismatch each cover one field of the "stale
// cache falls back" requirement -- every one of Version/Model/IndexPath
// must independently invalidate the whole cache, not just be ignored.
func TestLoadRejectsVersionMismatch(t *testing.T) {
	c := validCache()
	c.Version = CacheVersion + 1
	path := writeCache(t, c)
	if got, _ := Load(path, c.IndexPath, c.Model); got != nil {
		t.Fatal("Load accepted a cache with a different Version")
	}
}

func TestLoadRejectsModelMismatch(t *testing.T) {
	c := validCache()
	path := writeCache(t, c)
	if got, _ := Load(path, c.IndexPath, "a-different-model"); got != nil {
		t.Fatal("Load accepted a cache built for a different model")
	}
}

func TestLoadRejectsIndexPathMismatch(t *testing.T) {
	c := validCache()
	path := writeCache(t, c)
	if got, _ := Load(path, "/a/different/index.json", c.Model); got != nil {
		t.Fatal("Load accepted a cache built for a different index path")
	}
}

func TestLoadRejectsEmptyCache(t *testing.T) {
	c := validCache()
	c.Items = map[string]CacheEntry{}
	path := writeCache(t, c)
	if got, _ := Load(path, c.IndexPath, c.Model); got != nil {
		t.Fatal("Load accepted a cache with zero items")
	}
}

// TestCacheLookupRejectsStaleText is per-item staleness: an item whose
// text has changed since the cache was built must be treated as
// uncached, never returned as if the vector still describes it.
func TestCacheLookupRejectsStaleText(t *testing.T) {
	c := validCache()
	if _, ok := c.Lookup("it-1", "hello. world"); !ok {
		t.Fatal("Lookup rejected the exact text the cache was built from")
	}
	if _, ok := c.Lookup("it-1", "hello. world, but the text changed"); ok {
		t.Fatal("Lookup accepted a vector for text that no longer matches its cached hash")
	}
	if _, ok := c.Lookup("no-such-id", "hello. world"); ok {
		t.Fatal("Lookup returned a vector for an id that was never cached")
	}
}

// TestSaveIsAtomic exercises the "atomic private writes" requirement two
// ways: the file that lands at path is exactly what was saved (never a
// half-written file, which a temp+rename save cannot produce even under
// this test's own single-goroutine timing), and no stray temp file is
// left behind once Save returns.
func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json.embeddings.json")
	c := validCache()
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, reason := Load(path, c.IndexPath, c.Model)
	if got == nil {
		t.Fatalf("Load after Save failed: %s", reason)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("directory has %d entr(y/ies) after Save, want exactly 1 (the final file, no leftover temp file): %v", len(entries), names)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache file permissions = %o, want 0600 (private)", perm)
	}
}

// TestSavePreservesOldCacheOnFailedWrite is the other half of "atomic":
// simulate a failure mid-write (an unwritable destination DIRECTORY,
// forcing the final os.Rename to fail) and confirm the ORIGINAL file at
// path survives completely unchanged -- never truncated, never replaced
// with a partial write.
func TestSavePreservesOldCacheOnFailedWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json.embeddings.json")
	original := validCache()
	if err := original.Save(path); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	originalBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Make the directory read-only so CreateTemp (and thus the whole
	// second Save) fails before ever reaching Rename -- this exercises
	// "a failed write must not touch the existing file" without relying
	// on a real crash mid-syscall, which no portable Go test can force.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700) // restore so t.TempDir() can clean up

	updated := validCache()
	updated.Items["it-2"] = CacheEntry{TextHash: TextHash("second"), Vector: []float32{9, 9, 9}}
	if err := updated.Save(path); err == nil {
		t.Fatal("Save succeeded against a read-only directory -- expected it to fail before touching the original file")
	}

	os.Chmod(dir, 0o700)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(originalBytes) {
		t.Fatal("the original cache file was modified despite the write that was supposed to replace it failing")
	}
}

func TestCachePath(t *testing.T) {
	got := CachePath("/private/tmp/x/index.json")
	want := "/private/tmp/x/index.json.embeddings.json"
	if got != want {
		t.Errorf("CachePath = %q, want %q", got, want)
	}
}
