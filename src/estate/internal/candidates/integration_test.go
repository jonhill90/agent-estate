package candidates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// TestIntegratedSourceBackedWorkflow is this task's own required fixture
// (deliverable 6): candidate A (conversation-sourced, from codex_provenance)
// and candidate B (catalogue-sourced, via RegisterCatalogueSource) both
// proposed; A accepted, B rejected; A revised (superseded); retrieval
// returns ONLY A's current accepted revision; repeated
// registration/publication creates zero duplicates; superseded/rejected
// content never returns as current. Every path here is a t.TempDir() --
// nothing touches the real Agent Memory vault or the real corpus (assert on
// paths below is exactly this: every path asserted against lives under a
// directory this test created and will remove).
func TestIntegratedSourceBackedWorkflow(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("pA", "provA"))
	if _, err := Derive(db, true); err != nil {
		t.Fatalf("Derive (candidate A, conversation-sourced): %v", err)
	}
	a := query(t, db, "select id from knowledge_candidates where prompt_id='pA'")

	regB, err := RegisterCatalogueSource(db, CatalogueSource{
		ID: "catalog-src-1", Locator: "docs/fixture-source.md", ContentHash: "hash-catalog-1",
	}, true)
	if err != nil {
		t.Fatalf("RegisterCatalogueSource (candidate B): %v", err)
	}
	b := regB.ID

	vault := t.TempDir() // isolated fixture vault -- never the real Agent Memory vault
	if !filepath.IsAbs(vault) || vault == os.Getenv("AGENT_MEMORY_VAULT") {
		t.Fatalf("fixture vault is not isolated: %s", vault)
	}
	if err := os.MkdirAll(filepath.Join(vault, "agent", "facts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "agent", "index.md"), []byte("# Facts\n"), 0600); err != nil {
		t.Fatal(err)
	}

	pA := Proposal{
		Slug: "integrated-a", Type: "project", Title: "Integrated fixture A", Description: "Conversation-sourced",
		Learning: "Use the amber path for the invented integration fixture.", OperatorContext: "The invented operator chose amber.",
		AssistantContext: "The assistant suggested violet; not instruction.", Reviewer: "fixture-reviewer",
		DestinationKind: "memory", Destination: "agent/facts/integrated-a.md", Reason: "Invented reason A",
	}
	if _, err := Propose(db, a, pA, true); err != nil {
		t.Fatalf("Propose A: %v", err)
	}
	pB := Proposal{
		Slug: "integrated-b", Type: "reference", Title: "Integrated fixture B", Description: "Catalogue-sourced",
		Learning: "Invented catalogue learning that must never surface.", OperatorContext: "Invented operator context B.",
		AssistantContext: "Invented assistant context B.", Reviewer: "fixture-reviewer",
		DestinationKind: "memory", Destination: "agent/facts/integrated-b.md", Reason: "Invented reason B",
	}
	if _, err := Propose(db, b, pB, true); err != nil {
		t.Fatalf("Propose B: %v", err)
	}

	acceptedA, err := Publish(db, vault, a, "accept", true)
	if err != nil || acceptedA.State != "promoted" {
		t.Fatalf("accept A: %+v %v", acceptedA, err)
	}
	rejectedB, err := Publish(db, vault, b, "reject", true)
	if err != nil || rejectedB.State != "rejected" {
		t.Fatalf("reject B: %+v %v", rejectedB, err)
	}
	if _, err := os.Stat(filepath.Join(vault, "agent", "facts", "integrated-b.md")); !os.IsNotExist(err) {
		t.Fatal("rejected candidate B has a fact on disk")
	}

	index := filepath.Join(t.TempDir(), "index.json")
	cfg := knowledge.Config{VaultDir: vault, RunGH: func(...string) ([]byte, error) { return []byte("[]"), nil }}
	build := func() {
		t.Helper()
		if err := knowledge.Write(index, knowledge.Generate(cfg, time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	build()

	found := knowledge.Query(index, "source:vault-fact amber", 10, true)
	if len(found.Matches) != 1 {
		t.Fatalf("query for A's accepted content: %+v", found)
	}
	oldID := found.Matches[0].ID
	if got := knowledge.Query(index, "source:vault-fact violet catalogue", 10, true); len(got.Matches) != 0 {
		t.Fatalf("rejected B's content is retrievable: %+v", got)
	}

	// Revise/supersede A. The critical check here is the STALE index: a
	// compiled index generated BEFORE the revision was published must not
	// go on serving A's old (now-superseded) content just because nothing
	// has told it to regenerate -- knowledge.Get's own live-file check
	// (currentMemoryItem, internal/knowledge/vault.go) must catch this by
	// comparing the live file's memory_revision against the one baked into
	// the stale compiled snapshot, so this assertion runs BEFORE the
	// post-revision build() below, against the still-stale `index` file.
	revised := pA
	revised.Learning = "Use the crimson path for the invented integration fixture."
	revised.OperatorContext = "The invented operator revised the choice to crimson."
	revised.Supersedes = acceptedA.PublishedRevision
	if _, err := Propose(db, a, revised, true); err != nil {
		t.Fatalf("Propose A revision: %v", err)
	}
	supersededA, err := Publish(db, vault, a, "accept", true)
	if err != nil || supersededA.State != "promoted" {
		t.Fatalf("accept A revision: %+v %v", supersededA, err)
	}
	if _, ok, _ := knowledge.Get(index, oldID, true); ok {
		t.Fatal("stale index (generated before the supersede) served A's superseded revision")
	}
	build()

	found = knowledge.Query(index, "source:vault-fact fixture", 10, true)
	if len(found.Matches) != 1 || found.Matches[0].ID != oldID {
		t.Fatalf("post-supersede query: %+v (want exactly one match, same stable id %s)", found, oldID)
	}
	current, ok, _ := knowledge.Get(index, oldID, true)
	if !ok || !strings.Contains(current.Tier2, "crimson") || strings.Contains(current.Tier2, "amber") {
		t.Fatalf("current revision content = %+v", current)
	}

	// Repeated registration/publication creates zero duplicates.
	rerunDerive, err := Derive(db, true)
	if err != nil || rerunDerive.Inserted != 0 {
		t.Fatalf("Derive rerun: %+v %v", rerunDerive, err)
	}
	rerunRegister, err := RegisterCatalogueSource(db, CatalogueSource{
		ID: "catalog-src-1", Locator: "docs/fixture-source.md", ContentHash: "hash-catalog-1",
	}, true)
	if err != nil || !rerunRegister.Existed {
		t.Fatalf("RegisterCatalogueSource rerun: %+v %v", rerunRegister, err)
	}
	rerunPublish, err := Publish(db, vault, a, "accept", true)
	if err != nil || rerunPublish.Changed {
		t.Fatalf("Publish rerun: %+v %v", rerunPublish, err)
	}
	total := query(t, db, "select count(*) from knowledge_candidates;")
	if total != "2" {
		t.Fatalf("total candidates after reruns = %s, want 2 (A + B, no duplicates)", total)
	}

	// Reject A entirely, confirm current-only retrieval drops it too.
	if _, err := Publish(db, vault, a, "reject", true); err != nil {
		t.Fatalf("reject A: %v", err)
	}
	build()
	if got := knowledge.Query(index, "source:vault-fact fixture", 10, true); len(got.Matches) != 0 {
		t.Fatalf("rejected A's content still retrievable: %+v", got)
	}

	// No fixture content may enter the real vault -- assert on paths: every
	// path this test wrote under is inside t.TempDir(), never under
	// AGENT_MEMORY_VAULT.
	realVault := os.Getenv("AGENT_MEMORY_VAULT")
	for _, p := range []string{db, vault, index} {
		abs, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		if realVault != "" {
			if realAbs, err := filepath.Abs(realVault); err == nil && (abs == realAbs || len(abs) > len(realAbs) && abs[:len(realAbs)+1] == realAbs+string(filepath.Separator)) {
				t.Fatalf("fixture path %s falls under the real vault %s", abs, realAbs)
			}
		}
	}

	t.Logf("A: accept=promoted revise=%s supersede-hides-old=true reject=withdrawn; B: reject=withdrawn never-surfaced=true; derive_rerun_new=%d register_rerun_existed=%v publish_rerun_changed=%v total_rows=%s",
		supersededA.PublishedRevision, rerunDerive.Inserted, rerunRegister.Existed, rerunPublish.Changed, total)
}
