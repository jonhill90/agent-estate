package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// baseFetcher returns a Fetcher over two fixed skills, standing in for
// EvalStatusFetcher's own output -- InvocationFetcher only ever wraps
// another Fetcher, never Scans a directory itself.
func baseFetcher(t *testing.T) Fetcher {
	t.Helper()
	return func() ([]Skill, error) {
		return []Skill{
			{Dir: "devils-advocate", Name: "devils-advocate"},
			{Dir: "never-invoked", Name: "never-invoked"},
		}, nil
	}
}

// TestInvocationFetcher_NoCacheFile is the InvocationsNoHistory case: the
// cache path resolves but nothing has been written there yet.
func TestInvocationFetcher_NoCacheFile(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "does-not-exist.json")

	got, err := InvocationFetcher(baseFetcher(t), cachePath)()
	if err != nil {
		t.Fatalf("InvocationFetcher: %v", err)
	}
	for _, s := range got {
		if s.InvocationCount != nil {
			t.Errorf("%s: InvocationCount = %v, want nil (no cache written)", s.Dir, *s.InvocationCount)
		}
		if s.InvocationState != InvocationsNoHistory {
			t.Errorf("%s: InvocationState = %q, want %q", s.Dir, s.InvocationState, InvocationsNoHistory)
		}
	}
}

// TestInvocationFetcher_MalformedCache is the InvocationsStoreUnreadable
// case: a cache file exists but is not valid JSON in the expected shape.
func TestInvocationFetcher_MalformedCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	if err := os.WriteFile(cachePath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := InvocationFetcher(baseFetcher(t), cachePath)()
	if err != nil {
		t.Fatalf("InvocationFetcher: %v", err)
	}
	for _, s := range got {
		if s.InvocationCount != nil {
			t.Errorf("%s: InvocationCount = %v, want nil (unreadable cache)", s.Dir, *s.InvocationCount)
		}
		if s.InvocationState != InvocationsStoreUnreadable {
			t.Errorf("%s: InvocationState = %q, want %q", s.Dir, s.InvocationState, InvocationsStoreUnreadable)
		}
	}
}

// TestInvocationFetcher_EmptyCachePath treats "" (no resolvable location)
// the same as InvocationsStoreUnreadable -- there is nowhere to read from.
func TestInvocationFetcher_EmptyCachePath(t *testing.T) {
	got, err := InvocationFetcher(baseFetcher(t), "")()
	if err != nil {
		t.Fatalf("InvocationFetcher: %v", err)
	}
	for _, s := range got {
		if s.InvocationState != InvocationsStoreUnreadable {
			t.Errorf("%s: InvocationState = %q, want %q", s.Dir, s.InvocationState, InvocationsStoreUnreadable)
		}
	}
}

// TestInvocationFetcher_RealCountsIncludingGenuineZero is this test's main
// positive case, and the one #164 was actually about: a skill with a real
// recorded count of 0 renders "0" via a non-nil *0, not InvocationsNoHistory
// and not InvocationsStoreUnreadable -- a genuine zero and an absent/broken
// cache must never look the same.
func TestInvocationFetcher_RealCountsIncludingGenuineZero(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	if err := WriteInvocationCache(cachePath, "2026-08-28T00:00:00Z", map[string]int{
		"devils-advocate": 23,
		// "never-invoked" deliberately absent from the map: the cache
		// loaded fine and simply has nothing for it -- a real, counted
		// zero, not "no history".
	}); err != nil {
		t.Fatalf("WriteInvocationCache: %v", err)
	}

	got, err := InvocationFetcher(baseFetcher(t), cachePath)()
	if err != nil {
		t.Fatalf("InvocationFetcher: %v", err)
	}
	byDir := map[string]Skill{}
	for _, s := range got {
		byDir[s.Dir] = s
	}

	da := byDir["devils-advocate"]
	if da.InvocationCount == nil || *da.InvocationCount != 23 {
		t.Errorf("devils-advocate InvocationCount = %v, want 23", da.InvocationCount)
	}
	if da.InvocationState != "" {
		t.Errorf("devils-advocate InvocationState = %q, want empty (a real count is set)", da.InvocationState)
	}

	never := byDir["never-invoked"]
	if never.InvocationCount == nil {
		t.Fatalf("never-invoked InvocationCount = nil, want a real *0 -- the store was read successfully "+
			"and genuinely has nothing for this skill, which must render \"0\", not %q or %q",
			InvocationsNoHistory, InvocationsStoreUnreadable)
	}
	if *never.InvocationCount != 0 {
		t.Errorf("never-invoked InvocationCount = %d, want 0", *never.InvocationCount)
	}
	if never.InvocationState != "" {
		t.Errorf("never-invoked InvocationState = %q, want empty (a real zero is set)", never.InvocationState)
	}
}

// TestBuildInvocationCache_CountsSkillToolUseAcrossShapes is the mutation
// target: BuildInvocationCache must count a Skill tool_use block whether
// or not its input carries an "args" sibling alongside "skill" -- the two
// shapes confirmed present in the real corpus (this package's own doc
// comment). It must also ignore tool_use blocks for any other tool name,
// and must not double count across two separate transcript files.
func TestBuildInvocationCache_CountsSkillToolUseAcrossShapes(t *testing.T) {
	dir := t.TempDir()
	writeTranscriptLine(t, filepath.Join(dir, "a.jsonl"),
		`{"message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"devils-advocate"}}]}}`,
		`{"message":{"content":[{"type":"tool_use","name":"Skill","input":{"args":{"x":1},"skill":"devils-advocate"}}]}}`,
		`{"message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`,
	)
	writeTranscriptLine(t, filepath.Join(dir, "sub", "b.jsonl"),
		`{"message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"tmux"}}]}}`,
	)

	counts, err := BuildInvocationCache(dir)
	if err != nil {
		t.Fatalf("BuildInvocationCache: %v", err)
	}
	if counts["devils-advocate"] != 2 {
		t.Errorf("devils-advocate = %d, want 2 (both input shapes counted)", counts["devils-advocate"])
	}
	if counts["tmux"] != 1 {
		t.Errorf("tmux = %d, want 1 (nested file scanned)", counts["tmux"])
	}
	if _, ok := counts["ls"]; ok {
		t.Errorf("a Bash tool_use must never be counted as a Skill invocation")
	}
	if len(counts) != 2 {
		t.Errorf("got %d distinct skills, want 2: %+v", len(counts), counts)
	}
}

func writeTranscriptLine(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestWriteInvocationCache_RoundTrips confirms the on-disk shape
// loadInvocationCache expects is exactly what WriteInvocationCache
// produces -- the two halves of the same seam, verified as a pair rather
// than each in isolation.
func TestWriteInvocationCache_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	want := map[string]int{"adopt-or-build": 6, "tmux": 15}
	if err := WriteInvocationCache(path, "2026-08-28T00:00:00Z", want); err != nil {
		t.Fatalf("WriteInvocationCache: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var onDisk skillInvocationCache
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if onDisk.BuiltAt != "2026-08-28T00:00:00Z" {
		t.Errorf("BuiltAt = %q, want the timestamp passed in", onDisk.BuiltAt)
	}

	got, notBuilt, err := loadInvocationCache(path)
	if err != nil || notBuilt {
		t.Fatalf("loadInvocationCache: got=%v notBuilt=%v err=%v", got, notBuilt, err)
	}
	if got["adopt-or-build"] != 6 || got["tmux"] != 15 || len(got) != 2 {
		t.Errorf("loadInvocationCache round-trip = %v, want %v", got, want)
	}
}
