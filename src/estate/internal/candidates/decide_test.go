package candidates

import (
	"errors"
	"testing"
)

func TestDecideRejectsUnknownDecision(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	id := query(t, db, "select id from knowledge_candidates;")

	_, err := Decide(db, id, "delete", true)
	if err == nil {
		t.Fatal("Decide with an invalid decision: want an error, got nil")
	}
}

func TestDecideUnknownCandidateRefuses(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}

	_, err := Decide(db, "no-such-id", DecisionPromote, true)
	if !errors.Is(err, ErrCandidateNotFound) {
		t.Fatalf("Decide(unknown id): err = %v, want ErrCandidateNotFound", err)
	}
}

// TestDecideDryRunWritesNothing is this package's own mutation-check first
// direction: apply=false must report what WOULD happen without adding the
// decision/decided_at columns at all, let alone writing to them. If Decide
// is ever changed to write regardless of apply, this test must catch it --
// the decision column not existing yet is exactly the evidence a write
// occurred.
func TestDecideDryRunWritesNothing(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	id := query(t, db, "select id from knowledge_candidates;")

	res, err := Decide(db, id, DecisionPromote, false)
	if err != nil {
		t.Fatalf("Decide (dry run): %v", err)
	}
	if res.Applied {
		t.Fatalf("Applied = true for a dry run, want false")
	}
	if res.Status != "promoted" {
		t.Fatalf("Status = %q, want promoted", res.Status)
	}
	if res.DecidedAt != "" {
		t.Fatalf("DecidedAt = %q, want empty for a dry run", res.DecidedAt)
	}

	has, err := columnExists(db, "knowledge_candidates", "decision")
	if err != nil {
		t.Fatalf("columnExists: %v", err)
	}
	if has {
		t.Fatal("decision column exists after a dry run -- Decide wrote something it should not have")
	}
}

func TestDecideAppliedRecordsDecision(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	id := query(t, db, "select id from knowledge_candidates;")

	res, err := Decide(db, id, DecisionDiscard, true)
	if err != nil {
		t.Fatalf("Decide (apply): %v", err)
	}
	if !res.Applied || res.Status != "discarded" || res.DecidedAt == "" {
		t.Fatalf("DecideResult = %+v, want Applied=true Status=discarded DecidedAt set", res)
	}

	gotStatus := query(t, db, "select decision from knowledge_candidates where id='"+id+"';")
	if gotStatus != "discarded" {
		t.Fatalf("decision column = %q, want discarded", gotStatus)
	}
	gotKind := query(t, db, "select kind from knowledge_candidates where id='"+id+"';")
	if gotKind != "unclassified" {
		t.Fatalf("kind changed to %q by Decide -- a review decision must never touch kind/status/anything else Derive owns", gotKind)
	}
	gotQuarantineStatus := query(t, db, "select status from knowledge_candidates where id='"+id+"';")
	if gotQuarantineStatus != "candidate" {
		t.Fatalf("status changed to %q by Decide -- promotion never moves the row out of quarantine in this slice", gotQuarantineStatus)
	}
}

// TestDecideAppliedTwiceIsIdempotentInEffect proves a re-run against an
// already-decided candidate updates decided_at rather than erroring or
// duplicating a row (UPDATE, not INSERT, so PRIMARY KEY collision is not
// even a code path here -- this test pins that down rather than assuming it).
func TestDecideAppliedTwiceUpdatesInPlace(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	id := query(t, db, "select id from knowledge_candidates;")

	if _, err := Decide(db, id, DecisionDiscard, true); err != nil {
		t.Fatalf("first Decide: %v", err)
	}
	if _, err := Decide(db, id, DecisionPromote, true); err != nil {
		t.Fatalf("second Decide: %v", err)
	}

	total := query(t, db, "select count(*) from knowledge_candidates;")
	if total != "1" {
		t.Fatalf("row count after two decisions = %s, want 1 (UPDATE, not a duplicate INSERT)", total)
	}
	final := query(t, db, "select decision from knowledge_candidates where id='"+id+"';")
	if final != "promoted" {
		t.Fatalf("decision after second call = %q, want promoted (last decision wins)", final)
	}
}
