package candidates

import (
	"errors"
	"os/exec"
	"testing"
)

func TestListRequiresDerivedTable(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	// Derive was never run -- knowledge_candidates does not exist yet.
	_, err := List(db, ListFilter{})
	if !errors.Is(err, ErrCandidatesTableMissing) {
		t.Fatalf("List before Derive: err = %v, want ErrCandidatesTableMissing", err)
	}
}

func TestListEmptyFilterIsNotAnError(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}

	res, err := List(db, ListFilter{SourceFile: "no-such-source-anywhere"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 0 || len(res.Items) != 0 {
		t.Fatalf("List with a filter matching nothing = %+v, want zero items and zero total (not an error)", res)
	}
}

func TestListFiltersBySourceFile(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1")+withOneUnit("p2", "prov2"))
	if err := exec.Command("sqlite3", db,
		"UPDATE codex_provenance SET source_file='/sessions/a.jsonl' WHERE id='prov1';"+
			"UPDATE codex_provenance SET source_file='/sessions/b.jsonl' WHERE id='prov2';").Run(); err != nil {
		t.Fatalf("seeding distinct source files: %v", err)
	}
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}

	res, err := List(db, ListFilter{SourceFile: "a.jsonl"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 1 || len(res.Items) != 1 || res.Items[0].PromptID != "p1" {
		t.Fatalf("List filtered by a.jsonl = %+v, want exactly p1", res)
	}
}

func TestListPagesInIngestionOrder(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1")+withOneUnit("p2", "prov2")+withOneUnit("p3", "prov3"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}

	page1, err := List(db, ListFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	page2, err := List(db, ListFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if page1.Total != 3 || page2.Total != 3 {
		t.Fatalf("Total should be stable across pages: page1=%d page2=%d, want 3 both", page1.Total, page2.Total)
	}
	if len(page1.Items) != 2 || len(page2.Items) != 1 {
		t.Fatalf("page sizes = %d, %d, want 2, 1", len(page1.Items), len(page2.Items))
	}
	if page1.Items[0].PromptID == page2.Items[0].PromptID {
		t.Fatalf("page1 and page2 overlap on %s -- offset did not advance", page1.Items[0].PromptID)
	}
}

// TestListSurfacesProvenanceGoneLoudly simulates a candidate row whose
// provenance citation has gone missing after Derive ran (e.g. the
// codex_provenance row was later removed by something else) -- List must
// flag it, never drop it silently or print an empty source file that reads
// like a real (if odd) value.
func TestListSurfacesProvenanceGoneLoudly(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if err := exec.Command("sqlite3", db, "DELETE FROM codex_provenance WHERE id='prov1';").Run(); err != nil {
		t.Fatalf("deleting provenance row: %v", err)
	}

	res, err := List(db, ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("List after provenance row deleted = %d items, want 1 (surfaced, not dropped)", len(res.Items))
	}
	if !res.Items[0].ProvenanceGone {
		t.Fatalf("ProvenanceGone = false, want true once codex_provenance row is gone")
	}
	if res.Items[0].SourceFile != "" || res.Items[0].RecordIndex != -1 {
		t.Fatalf("gone-provenance row = %+v, want empty SourceFile and RecordIndex -1, not a fabricated value", res.Items[0])
	}
}

func TestGetNotFound(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}

	_, err := Get(db, "no-such-id")
	if !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("Get(missing id): err = %v, want ErrCandidateNotFound", err)
	}
}

func TestGetResolvesPromptOnDemand(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	id := query(t, db, "select id from knowledge_candidates;")

	d, err := Get(db, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.PromptGone || !d.PromptFound {
		t.Fatalf("Detail = %+v, want PromptFound=true PromptGone=false", d)
	}
	if d.PromptText != "raw text" {
		t.Fatalf("PromptText = %q, want the fixture's raw text resolved fresh, not copied at Derive time", d.PromptText)
	}
	if d.ProvenanceGone {
		t.Fatalf("ProvenanceGone = true, want false -- provenance row exists")
	}
	if d.SourceFile != "f.jsonl" {
		t.Fatalf("SourceFile = %q, want f.jsonl", d.SourceFile)
	}
}

// TestGetSurfacesPromptGoneLoudly is the Get-side analogue of
// TestListSurfacesProvenanceGoneLoudly -- a prompts row disappearing after
// Derive ran must read as PromptGone=true, never as an empty-but-present
// prompt.
func TestGetSurfacesPromptGoneLoudly(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	id := query(t, db, "select id from knowledge_candidates;")
	if err := exec.Command("sqlite3", db, "DELETE FROM prompts WHERE id='p1';").Run(); err != nil {
		t.Fatalf("deleting prompt row: %v", err)
	}

	d, err := Get(db, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !d.PromptGone || d.PromptFound {
		t.Fatalf("Detail = %+v, want PromptGone=true PromptFound=false", d)
	}
	if d.PromptText != "" {
		t.Fatalf("PromptText = %q, want empty once the prompts row is gone", d.PromptText)
	}
}
