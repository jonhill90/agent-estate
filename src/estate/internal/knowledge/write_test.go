package knowledge

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteThenReadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "index.json")
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Sources:       []SourceResult{{Name: "github-stars", OK: true, Count: 1}},
		Items:         []Item{{ID: "20260903120000", Source: "github-stars", Tier1: "a/one"}},
	}
	if err := Write(path, res); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "20260903120000" {
		t.Fatalf("Read() = %+v", got)
	}
	if !got.GeneratedAt.Equal(res.GeneratedAt) {
		t.Errorf("GeneratedAt = %v, want %v", got.GeneratedAt, res.GeneratedAt)
	}
}

func TestWriteFileCarriesItsOwnDerivedStatement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	res := Result{GeneratedAt: time.Now().UTC(), StalenessRule: stalenessRule, Note: derivedNote}
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Note, "derived") || !strings.Contains(got.Note, "not, and must never be treated as") {
		t.Errorf("Note does not carry the derived/never-authoritative statement: %q", got.Note)
	}
	if got.StalenessRule == "" {
		t.Error("StalenessRule is empty")
	}
}

func TestReadOfUnwrittenIndexIsAVisibleError(t *testing.T) {
	if _, err := Read(filepath.Join(t.TempDir(), "never-generated.json")); err == nil {
		t.Fatal("Read() of a path that was never written returned no error")
	}
}
