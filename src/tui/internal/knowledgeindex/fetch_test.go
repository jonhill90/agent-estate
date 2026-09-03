package knowledgeindex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadEmptyPathIsAVisibleError(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Fatal("Load(\"\") returned no error")
	}
}

// TestLoadNeverGeneratedIsAVisibleErrorNamingTheFix is the "estate
// knowledge has never been run yet" case -- distinct from a generated
// file with zero items, and must say what to do about it.
func TestLoadNeverGeneratedIsAVisibleErrorNamingTheFix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never-generated.json")
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() of a path that was never written returned no error")
	}
	if got := err.Error(); !contains(got, "estate knowledge") {
		t.Fatalf("Load() error does not name the fix: %q", got)
	}
}

func TestLoadMalformedFileIsAVisibleError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() of a malformed file returned no error")
	}
}

func TestLoadReadsARealFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC),
		StalenessRule: "regenerate any time",
		Note:          "derived",
		Sources:       []SourceResult{{Name: "github-stars", OK: true, Count: 1}},
		Items:         []Item{{ID: "20260903000000", Source: "github-stars", Tier1: "a/one"}},
	}
	writeFixture(t, path, res)

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Tier1 != "a/one" {
		t.Fatalf("Load() = %+v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
