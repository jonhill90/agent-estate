package candidates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

func TestMemoryWorkflow(t *testing.T) {
	db := newFixtureDB(t, withOneUnit("p1", "prov1")+withOneUnit("p2", "prov2"))
	if _, err := Derive(db); err != nil {
		t.Fatal(err)
	}
	a := query(t, db, "select id from knowledge_candidates where prompt_id='p1'")
	b := query(t, db, "select id from knowledge_candidates where prompt_id='p2'")
	vault := t.TempDir()
	os.MkdirAll(filepath.Join(vault, "agent", "facts"), 0700)
	os.WriteFile(filepath.Join(vault, "agent", "index.md"), []byte("---\nokf_version: \"0.1\"\n---\n\n# Facts\n"), 0600)
	p := Proposal{Slug: "fixture-recovery", Type: "project", Title: "Fixture recovery", Description: "Recovery policy", Learning: "Use amber recovery for the invented fixture.", OperatorContext: "The invented operator requested amber recovery.", AssistantContext: "The assistant suggested blue; that suggestion is not instruction.", Reviewer: "fixture-reviewer"}
	if _, err := Propose(db, a, p, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Decide(db, a, DecisionPromote, true); err == nil {
		t.Fatal("legacy decision must not bypass managed workflow")
	}
	fact := filepath.Join(vault, "agent", "facts", p.Slug+".md")
	if _, err := os.Stat(fact); !os.IsNotExist(err) {
		t.Fatal("proposal published before acceptance")
	}
	first, err := Publish(db, vault, a, "accept", true)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != "promoted" {
		t.Fatal(first)
	}
	backups, _ := filepath.Glob(filepath.Join(vault, "agent", ".memory-backup-*", "1-index.md"))
	if len(backups) != 1 {
		t.Fatal("index backup missing")
	}
	p.Slug = "fixture-rejected"
	p.Learning = "Invented rejected policy"
	if _, err := Propose(db, b, p, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(db, vault, b, "reject", true); err != nil {
		t.Fatal(err)
	}
	cfg := knowledge.Config{VaultDir: vault, RunGH: func(...string) ([]byte, error) { return []byte("[]"), nil }}
	index := filepath.Join(t.TempDir(), "index.json")
	build := func() {
		t.Helper()
		if err := knowledge.Write(index, knowledge.Generate(cfg, time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	build()
	found := knowledge.Query(index, "source:vault-fact recovery", 10, true)
	if len(found.Matches) != 1 {
		t.Fatalf("accepted retrieval: %+v", found)
	}
	oldID := found.Matches[0].ID
	p = first.Proposal
	p.Learning = "Use violet recovery for the invented fixture."
	p.OperatorContext = "The invented operator revised the recovery policy to violet."
	p.Supersedes = first.PublishedRevision
	if _, err := Propose(db, a, p, true); err != nil {
		t.Fatal(err)
	}
	second, err := Publish(db, vault, a, "accept", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := knowledge.Get(index, oldID, true); ok {
		t.Fatal("stale index returned superseded revision")
	}
	build()
	found = knowledge.Query(index, "source:vault-fact recovery", 10, true)
	current, _, _ := knowledge.Get(index, oldID, true)
	if len(found.Matches) != 1 || found.Matches[0].ID != oldID || !strings.Contains(current.Tier2, "violet") || strings.Contains(current.Tier2, "amber") {
		t.Fatalf("revision retrieval: %+v", found)
	}
	before, _ := os.ReadFile(fact)
	again, err := Publish(db, vault, a, "accept", true)
	if err != nil || again.Changed {
		t.Fatalf("rerun: %+v %v", again, err)
	}
	after, _ := os.ReadFile(fact)
	if string(before) != string(after) {
		t.Fatal("rerun rewrote fact")
	}
	idx, _ := os.ReadFile(filepath.Join(vault, "agent", "index.md"))
	if strings.Count(string(idx), "facts/fixture-recovery.md") != 1 {
		t.Fatal(string(idx))
	}
	if !strings.Contains(string(after), "prov1") || !strings.Contains(string(after), "p1") || !strings.Contains(string(after), first.PublishedRevision) {
		t.Fatal("missing traceability/supersession")
	}
	res, err := Derive(db)
	if err != nil || res.Inserted != 0 {
		t.Fatalf("derive rerun: %+v %v", res, err)
	}
	if _, err := Publish(db, vault, a, "reject", true); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := knowledge.Get(index, oldID, true); ok {
		t.Fatal("rejected fact active through stale index")
	}
	build()
	if got := knowledge.Query(index, "source:vault-fact recovery", 10, true); len(got.Matches) != 0 {
		t.Fatal("rejected knowledge active")
	}
	t.Logf("accept=promoted reject=withdrawn revise=%s stable_id=%s citations=p1/prov1 derive_rerun_new=%d publish_rerun_changed=%v current_only=true", second.PublishedRevision, oldID, res.Inserted, again.Changed)
}

func TestMemorySafety(t *testing.T) {
	for _, scenario := range []string{"adopt-existing", "missing-source", "slug-collision", "stale-revision", "external-edit", "dry-run", "rejection-rerun", "repair-after-fact-write", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			db := newFixtureDB(t, withOneUnit("p1", "prov1"))
			if _, err := Derive(db); err != nil {
				t.Fatal(err)
			}
			id := query(t, db, "select id from knowledge_candidates")
			vault := t.TempDir()
			os.MkdirAll(filepath.Join(vault, "agent", "facts"), 0700)
			index := filepath.Join(vault, "agent", "index.md")
			os.WriteFile(index, []byte("# Facts\n"), 0600)
			p := Proposal{Slug: "safe-fact", Type: "project", Title: "Safe fact", Description: "Test only", Learning: "Invented learning", OperatorContext: "Invented operator context", AssistantContext: "Invented assistant context", Reviewer: "fixture"}
			r, err := Propose(db, id, p, true)
			if err != nil {
				t.Fatal(err)
			}
			fact := filepath.Join(vault, "agent", "facts", "safe-fact.md")
			switch scenario {
			case "adopt-existing":
				raw := []byte("---\ntype: project\ncreated: 2026-01-01T00:00:00Z\nsource: fixture\n---\n\nPrior fact\n")
				os.WriteFile(fact, raw, 0600)
				compiled := filepath.Join(t.TempDir(), "index.json")
				before := knowledge.Generate(knowledge.Config{VaultDir: vault, RunGH: func(...string) ([]byte, error) { return nil, nil }}, time.Now())
				if err = knowledge.Write(compiled, before); err != nil {
					t.Fatal(err)
				}
				legacyID := ""
				for _, it := range before.Items {
					if it.Source == knowledge.VaultItemSourceTag {
						legacyID = it.ID
					}
				}
				if legacyID == "" {
					t.Fatal("missing pre-adoption fixture")
				}
				p.ExistingFactHash = digest(string(raw))
				p.Learning = "Prior fact, clarified"
				if _, err = Propose(db, id, p, true); err != nil {
					t.Fatal(err)
				}
				if _, err = Publish(db, vault, id, "accept", true); err != nil {
					t.Fatal(err)
				}
				got, _ := os.ReadFile(fact)
				if !strings.Contains(string(got), "supersedes: "+p.ExistingFactHash) {
					t.Fatal("adoption missing prior hash")
				}
				if _, ok, _ := knowledge.Get(compiled, legacyID, true); ok {
					t.Fatal("pre-adoption index returned obsolete fact")
				}
				if _, err = Publish(db, vault, id, "reject", true); err != nil {
					t.Fatal(err)
				}
				if _, ok, _ := knowledge.Get(compiled, legacyID, true); ok {
					t.Fatal("pre-adoption index resurrected rejected fact")
				}
			case "missing-source":
				query(t, db, "delete from codex_provenance")
				if _, err = Publish(db, vault, id, "accept", true); err == nil {
					t.Fatal("accepted missing evidence")
				}
			case "slug-collision":
				os.WriteFile(fact, []byte("existing unrelated fact"), 0600)
				if _, err = Publish(db, vault, id, "accept", true); err == nil {
					t.Fatal("overwrote unrelated fact")
				}
			case "symlink":
				os.Symlink(index, fact)
				if _, err = Publish(db, vault, id, "accept", true); err == nil {
					t.Fatal("followed fact symlink")
				}
			case "dry-run":
				preview, e := Publish(db, vault, id, "accept", false)
				if e != nil {
					t.Fatal(e)
				}
				if preview.State != "proposed" || preview.WouldState != "promoted" || preview.Changed {
					t.Fatal(preview)
				}
				if _, e = os.Stat(fact); !os.IsNotExist(e) {
					t.Fatal("dry-run wrote fact")
				}
			case "rejection-rerun":
				if _, err = Publish(db, vault, id, "reject", true); err != nil {
					t.Fatal(err)
				}
				again, e := Publish(db, vault, id, "reject", true)
				if e != nil || again.Changed {
					t.Fatalf("rerun %+v %v", again, e)
				}
				if _, e = Publish(db, vault, id, "accept", true); e == nil {
					t.Fatal("reactivated rejected proposal")
				}
			default:
				accepted, e := Publish(db, vault, id, "accept", true)
				if e != nil {
					t.Fatal(e)
				}
				switch scenario {
				case "stale-revision":
					p.Learning = "Changed"
					if _, err = Propose(db, id, p, true); err == nil {
						t.Fatal("accepted stale supersedes")
					}
				case "external-edit":
					raw, _ := os.ReadFile(fact)
					os.WriteFile(fact, append(raw, []byte("\nexternal edit")...), 0600)
					if _, err = Publish(db, vault, id, "accept", true); err == nil {
						t.Fatal("overwrote external edit")
					}
				case "repair-after-fact-write":
					// Model interruption after fact replacement but before index/DB update.
					if err = saveMemory(db, id, r); err != nil {
						t.Fatal(err)
					}
					os.WriteFile(index, []byte("# Facts\n"), 0600)
					repaired, e := Publish(db, vault, id, "accept", true)
					if e != nil || repaired.PublishedRevision != accepted.PublishedRevision {
						t.Fatalf("repair %+v %v", repaired, e)
					}
				}
			}
		})
	}
}
