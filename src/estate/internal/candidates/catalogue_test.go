package candidates

import "testing"

// TestRegisterCatalogueSourceNeverFabricatesAPromptRow is this task's own
// required contract (deliverable 1): a catalogue-sourced candidate must
// never require a fabricated prompts-table row. This asserts the row
// Registered leaves prompt_id/provenance_id empty rather than inventing one.
func TestRegisterCatalogueSourceNeverFabricatesAPromptRow(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, "")

	res, err := RegisterCatalogueSource(db, CatalogueSource{
		ID: "src-1", Locator: "docs/fixture.md", ContentHash: "hash-src-1",
	}, true)
	if err != nil {
		t.Fatalf("RegisterCatalogueSource: %v", err)
	}
	if res.Existed || !res.Applied {
		t.Fatalf("unexpected result: %+v", res)
	}

	promptID := query(t, db, "select prompt_id from knowledge_candidates where id='"+res.ID+"';")
	provID := query(t, db, "select provenance_id from knowledge_candidates where id='"+res.ID+"';")
	if promptID != "" || provID != "" {
		t.Fatalf("prompt_id=%q provenance_id=%q -- a catalogue row must never fabricate either", promptID, provID)
	}

	promptRows := query(t, db, "select count(*) from prompts;")
	if promptRows != "0" {
		t.Fatalf("prompts table has %s rows -- registering a catalogue source must never write a prompts row", promptRows)
	}
}

// TestRegisterCatalogueSourceIsIdempotent mirrors Derive's own
// Inserted/WouldInsert idempotence contract: a rerun against the same
// source id changes nothing and reports Existed=true.
func TestRegisterCatalogueSourceIsIdempotent(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, "")
	src := CatalogueSource{ID: "src-1", Locator: "docs/fixture.md", ContentHash: "hash-src-1"}

	first, err := RegisterCatalogueSource(db, src, true)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	second, err := RegisterCatalogueSource(db, src, true)
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	if second.ID != first.ID || !second.Existed {
		t.Fatalf("rerun result = %+v, want same id and Existed=true", second)
	}
	total := query(t, db, "select count(*) from knowledge_candidates;")
	if total != "1" {
		t.Fatalf("row count after two registrations = %s, want 1 (no duplicate)", total)
	}
}

// TestRegisterCatalogueSourceDryRunWritesNothing is the same "dry run means
// dry run" contract every other write path in this package keeps.
func TestRegisterCatalogueSourceDryRunWritesNothing(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, "")

	res, err := RegisterCatalogueSource(db, CatalogueSource{
		ID: "src-1", Locator: "docs/fixture.md", ContentHash: "hash-src-1",
	}, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Applied || res.Existed {
		t.Fatalf("dry run result = %+v, want Applied=false Existed=false", res)
	}
	exists, err := tableExists(db, "knowledge_candidates")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("knowledge_candidates exists after a dry run -- a dry run must write nothing")
	}
}

// TestRegisterCatalogueSourceMigratesOldShapeTable exercises the schema
// migration (candidates.go's migrateToSourceKindSchema) against a table
// created under the pre-catalogue shape (agent-estate#1244's original DDL:
// inline UNIQUE(prompt_id), no source_kind column) -- a real conversation
// candidate must survive the rebuild with its citation intact, and a
// second catalogue source registered afterward must not collide with the
// first (proving the inline UNIQUE(prompt_id) constraint that would have
// blocked two empty-prompt_id rows is genuinely gone, not just papered
// over).
func TestRegisterCatalogueSourceMigratesOldShapeTable(t *testing.T) {
	sqliteAvailable(t)
	// Derive itself now creates the NEW shape directly (candidateDDL was
	// generalized in place) -- an old-shape table only exists in a LIVE
	// corpus that ran a pre-#1255 build of this package. Reproduce that
	// exact shape by hand (agent-estate#1244's original DDL) rather than
	// through any current entry point, so this test proves the migration
	// path a real upgrade will actually exercise.
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	oldShapeDDL := `CREATE TABLE knowledge_candidates (
	id TEXT PRIMARY KEY,
	prompt_id TEXT NOT NULL,
	provenance_id TEXT NOT NULL,
	content_hash TEXT NOT NULL,
	source TEXT NOT NULL,
	kind TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(prompt_id)
);
INSERT INTO knowledge_candidates (id, prompt_id, provenance_id, content_hash, source, kind, status, created_at)
VALUES ('prov1', 'p1', 'prov1', 'hash-1', 'codex_provenance', 'unclassified', 'candidate', '2026-09-05T00:00:00Z');`
	if err := runWrite(db, oldShapeDDL, "seed old-shape table"); err != nil {
		t.Fatalf("seeding old-shape table: %v", err)
	}
	hasSourceKind, err := columnExists(db, "knowledge_candidates", "source_kind")
	if err != nil {
		t.Fatal(err)
	}
	if hasSourceKind {
		t.Fatal("fixture precondition failed: table already has source_kind before migration")
	}

	if _, err := RegisterCatalogueSource(db, CatalogueSource{
		ID: "src-1", Locator: "docs/a.md", ContentHash: "hash-a",
	}, true); err != nil {
		t.Fatalf("register after migration: %v", err)
	}
	if _, err := RegisterCatalogueSource(db, CatalogueSource{
		ID: "src-2", Locator: "docs/b.md", ContentHash: "hash-b",
	}, true); err != nil {
		t.Fatalf("second catalogue source (proves UNIQUE(prompt_id) no longer collides on empty): %v", err)
	}

	// The pre-migration conversation candidate must still resolve exactly
	// as before: same prompt/provenance citation, source_kind defaulted to
	// 'conversation' by the migration, never silently reclassified.
	d, err := Get(db, "prov1")
	if err != nil {
		t.Fatalf("Get pre-migration candidate: %v", err)
	}
	if d.SourceKind != "conversation" || d.PromptID != "p1" || d.ProvenanceID != "prov1" {
		t.Fatalf("migrated conversation candidate = %+v, want SourceKind=conversation PromptID=p1 ProvenanceID=prov1", d)
	}

	total := query(t, db, "select count(*) from knowledge_candidates;")
	if total != "3" {
		t.Fatalf("row count after migration + 2 catalogue registrations = %s, want 3", total)
	}
}

// TestGetCatalogueCandidateNeverReportsProvenanceOrPromptGone: a catalogue
// row has no prompt/provenance to resolve, so it must never be reported as
// PromptGone/ProvenanceGone (which would misleadingly imply a citation
// existed and then disappeared).
func TestGetCatalogueCandidateNeverReportsProvenanceOrPromptGone(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, "")
	res, err := RegisterCatalogueSource(db, CatalogueSource{
		ID: "src-1", Locator: "docs/fixture.md", ContentHash: "hash-src-1",
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	d, err := Get(db, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.PromptGone || d.ProvenanceGone {
		t.Fatalf("catalogue candidate reported gone: %+v", d)
	}
	if d.SourceKind != "catalogue" || d.CatalogueSourceID != "src-1" ||
		d.CatalogueLocator != "docs/fixture.md" || d.ContentHash != "hash-src-1" {
		t.Fatalf("catalogue detail = %+v, want the registered citation echoed back", d)
	}
}

func catalogueProposal(slug, destKind, dest string) Proposal {
	return Proposal{
		Slug: slug, Type: "reference", Title: "Catalogue fixture", Description: "Test only",
		Learning: "Invented learning from a catalogue source.", OperatorContext: "Invented operator context.",
		AssistantContext: "Invented assistant context.", Reviewer: "fixture-reviewer",
		DestinationKind: destKind, Destination: dest, Reason: "Invented reason for the fixture",
	}
}

// TestPublishRepoRecordsReceiptOnlyOnExactDestinationMatch is deliverable
// 3's repo-publication path: a receipt is recorded only for the EXACT path
// the proposal declared, with a plausible commit SHA -- never a
// caller-supplied path/commit that doesn't match what was reviewed.
func TestPublishRepoRecordsReceiptOnlyOnExactDestinationMatch(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, "")
	res, err := RegisterCatalogueSource(db, CatalogueSource{
		ID: "src-1", Locator: "docs/fixture.md", ContentHash: "hash-src-1",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	p := catalogueProposal("repo-fixture", "repo", "docs/knowledge-workflow.md")
	if _, err := Propose(db, res.ID, p, true); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	if _, err := Publish(db, "", res.ID, "accept", true); err == nil {
		t.Fatal("Publish accepted a repo-destination proposal -- must refuse and require PublishRepo")
	}
	if _, err := PublishRepo(db, res.ID, "accept", "docs/wrong-path.md", "abc1234", true); err == nil {
		t.Fatal("PublishRepo recorded a receipt for a path that doesn't match the proposal's declared destination")
	}
	if _, err := PublishRepo(db, res.ID, "accept", "docs/knowledge-workflow.md", "not-a-sha", true); err == nil {
		t.Fatal("PublishRepo recorded a receipt for a non-SHA commit")
	}

	r, err := PublishRepo(db, res.ID, "accept", "docs/knowledge-workflow.md", "abc1234", true)
	if err != nil {
		t.Fatalf("PublishRepo accept: %v", err)
	}
	if r.State != "promoted" || r.RepoPath != "docs/knowledge-workflow.md" || r.RepoCommit != "abc1234" {
		t.Fatalf("PublishRepo result = %+v", r)
	}

	// Rerun is a zero-change no-op (idempotent, mirrors Publish's own rerun
	// contract), and Publish still refuses this repo-kind proposal.
	again, err := PublishRepo(db, res.ID, "accept", "docs/knowledge-workflow.md", "abc1234", true)
	if err != nil || again.Changed {
		t.Fatalf("rerun changed state: %+v %v", again, err)
	}
	if _, err := Publish(db, "", res.ID, "reject", true); err == nil {
		t.Fatal("Publish must still refuse a repo-kind proposal on reject too")
	}
}

// TestPublishRepoRejectClearsReceipt: rejecting a repo-kind proposal
// records state=rejected and clears any prior receipt fields, matching
// Publish's own "reject withdraws" contract for the memory path.
func TestPublishRepoRejectClearsReceipt(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, "")
	res, err := RegisterCatalogueSource(db, CatalogueSource{
		ID: "src-1", Locator: "docs/fixture.md", ContentHash: "hash-src-1",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	p := catalogueProposal("repo-fixture", "repo", "docs/knowledge-workflow.md")
	if _, err := Propose(db, res.ID, p, true); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishRepo(db, res.ID, "reject", "", "", true); err != nil {
		t.Fatalf("PublishRepo reject: %v", err)
	}
	r, err := ReadMemory(db, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "rejected" || r.RepoPath != "" || r.RepoCommit != "" {
		t.Fatalf("rejected repo review = %+v, want cleared receipt", r)
	}
	if _, err := PublishRepo(db, res.ID, "accept", "docs/knowledge-workflow.md", "abc1234", true); err == nil {
		t.Fatal("accepted a rejected repo proposal without a revised proposal first")
	}
}

// TestMemoryDestinationRequiresKindAndReason pins deliverable 2's proposal
// shape requirement: destination_kind, destination and reason are all
// required, not optional extras a caller can omit.
func TestMemoryDestinationRequiresKindAndReason(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))
	if _, err := Derive(db, true); err != nil {
		t.Fatal(err)
	}
	id := query(t, db, "select id from knowledge_candidates;")

	base := catalogueProposal("missing-fields", "memory", "agent/facts/missing-fields.md")
	for _, mutate := range []func(*Proposal){
		func(p *Proposal) { p.DestinationKind = "" },
		func(p *Proposal) { p.DestinationKind = "vault" },
		func(p *Proposal) { p.Destination = "" },
		func(p *Proposal) { p.Reason = "" },
	} {
		p := base
		mutate(&p)
		if _, err := Propose(db, id, p, true); err == nil {
			t.Fatalf("Propose accepted an invalid destination shape: %+v", p)
		}
	}
}
