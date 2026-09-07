package candidates

import (
	"fmt"
	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestINMAPSLifecycle(t *testing.T) {
	db := newFixtureDB(t, withOneUnit("p1", "prov1")+withOneUnit("p2", "prov2"))
	Derive(db, true)
	id := query(t, db, "select id from knowledge_candidates where prompt_id='p1'")
	vault := t.TempDir()
	os.MkdirAll(filepath.Join(vault, "agent/facts"), 0700)
	os.WriteFile(filepath.Join(vault, "agent/index.md"), []byte("# Facts\n"), 0600)
	os.MkdirAll(filepath.Join(vault, "01 - Notes"), 0700)
	os.MkdirAll(filepath.Join(vault, "99 - Meta"), 0700)
	os.WriteFile(filepath.Join(vault, "99 - Meta/tags.md"), []byte("`kind/decision`"), 0600)
	p := catalogueProposal("fixture-recovery", "memory", "01 - Notes")
	p.Tags = []string{"kind/decision"}
	p.Type = "Fact"
	r, err := Propose(db, id, p, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = StageMemory(vault, id, r, true); err != nil {
		t.Fatal(err)
	}
	other := query(t, db, "select id from knowledge_candidates where prompt_id='p2'")
	rejected := p
	rejected.Slug = "rejected-example"
	rr, e := Propose(db, other, rejected, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = StageMemory(vault, other, rr, true); e != nil {
		t.Fatal(e)
	}
	if _, e = Publish(db, vault, other, "reject", true); e != nil {
		t.Fatal(e)
	}
	againReject, e := Publish(db, vault, other, "reject", true)
	if e != nil || againReject.Changed {
		t.Fatal("rejection retry not idempotent", e)
	}
	reserved := time.Now().UTC().Format("20060102") + "0001.md"
	os.MkdirAll(filepath.Join(vault, "01 - Notes/01p - Parameters"), 0700)
	os.WriteFile(filepath.Join(vault, "01 - Notes/01p - Parameters", reserved), []byte("---\ntype: Question\nstatus: draft\n---\n"), 0600)
	first, err := Publish(db, vault, id, "accept", true)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first.NotePath) == reserved {
		t.Fatal("duplicate ID across Notes directories")
	}
	if first.NotePath == "" {
		t.Fatal("missing canonical path")
	}
	// agent/ dissolution (run/inmaps-spec.md §7b, P5 batch 1): the change
	// log moved from agent/log.md to 99 - Meta/log.md -- writeSet must
	// write there, not resurrect a dead agent/log.md path. Assert both
	// directions: the new path has real content, and the old path was
	// never created (a silent split-brain -- writes landing at both
	// locations across different code paths -- would pass a check that
	// only tested the new path in isolation).
	logBytes, e := os.ReadFile(filepath.Join(vault, "99 - Meta/log.md"))
	if e != nil {
		t.Fatalf("99 - Meta/log.md not written by an INMAPS accept: %v", e)
	}
	if !strings.Contains(string(logBytes), "process:estate-candidates") {
		t.Fatalf("99 - Meta/log.md missing expected entry: %s", logBytes)
	}
	if _, e := os.Stat(filepath.Join(vault, "agent/log.md")); !os.IsNotExist(e) {
		t.Fatal("agent/log.md was written -- the dead pre-dissolution path must never be resurrected")
	}
	again, err := Publish(db, vault, id, "accept", true)
	if err != nil || again.Changed {
		t.Fatalf("retry: %+v %v", again, err)
	}
	p.Supersedes = first.PublishedRevision
	p.Learning = "Use violet recovery."
	r, err = Propose(db, id, p, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = StageMemory(vault, id, r, true); err != nil {
		t.Fatal(err)
	}
	second, err := Publish(db, vault, id, "accept", true)
	if err != nil {
		t.Fatal(err)
	}
	if first.NotePath == second.NotePath {
		t.Fatal("supersession reused ID")
	}
	old, _ := os.ReadFile(filepath.Join(vault, first.NotePath))
	if !strings.Contains(string(old), "status: deprecated") {
		t.Fatal("old note not deprecated")
	}
	idx := filepath.Join(t.TempDir(), "index.json")
	knowledge.Write(idx, knowledge.Generate(knowledge.Config{VaultDir: vault, RunGH: func(...string) ([]byte, error) { return []byte("[]"), nil }}, time.Now()))
	q := knowledge.Query(idx, "source:vault-fact recovery", 10, true)
	if len(q.Matches) != 1 {
		t.Fatalf("current only: %+v", q)
	}
	if _, err = Publish(db, vault, id, "reject", true); err != nil {
		t.Fatal(err)
	}
	if q = knowledge.Query(idx, "source:vault-fact recovery", 10, true); len(q.Matches) != 0 {
		t.Fatal("rejected remains retrievable")
	}
}

func TestINMAPSGuardsAndMOC(t *testing.T) {
	v := t.TempDir()
	for _, d := range []string{"agent", "01 - Notes", "99 - Meta"} {
		os.MkdirAll(filepath.Join(v, d), 0700)
	}
	os.WriteFile(filepath.Join(v, "99 - Meta/tags.md"), []byte("`kind/decision`"), 0600)
	p := Proposal{Type: "Fact", Title: "T", Description: "D", Learning: "L", Tags: []string{"kind/decision"}}
	bad := p
	bad.Title = ""
	if validateINMAPS(v, bad) == nil {
		t.Fatal("schema allowed missing title")
	}
	bad = p
	bad.Tags = []string{"kind/invented"}
	if validateINMAPS(v, bad) == nil {
		t.Fatal("vocabulary allowed unknown kind")
	}
	for n := 1; n <= 8; n++ {
		name := fmt.Sprintf("20260906%04d.md", n)
		os.WriteFile(filepath.Join(v, "01 - Notes", name), []byte("---\nstatus: stable\ntitle: Example\ntags: [\"kind/decision\"]\n---\n"), 0600)
		if n == 7 {
			got, e := MOCProposals(v, true)
			if e != nil || len(got) != 0 {
				t.Fatalf("seven: %v %v", got, e)
			}
		}
	}
	got, e := MOCProposals(v, true)
	if e != nil || len(got) != 1 {
		t.Fatalf("eight: %v %v", got, e)
	}
	if e = ReviewMOC(v, filepath.Base(got[0]), "process:test", true, true); e != nil {
		t.Fatal(e)
	}
	got, e = MOCProposals(v, true)
	if e != nil || len(got) != 0 {
		t.Fatalf("hub suppression: %v %v", got, e)
	}
	hub := filepath.Join(v, "02 - MOCs/kind-decision.md")
	raw, _ := os.ReadFile(hub)
	raw = []byte(strings.Replace(string(raw), "Review these connections before accepting.", "Curated overview.", 1))
	os.WriteFile(hub, raw, 0600)
	os.WriteFile(filepath.Join(v, "01 - Notes/202609060009.md"), []byte("---\nstatus: stable\ntitle: Ninth\ntags: [\"kind/decision\"]\n---\n"), 0600)
	if _, e = RefreshMOCs(v, true); e != nil {
		t.Fatal(e)
	}
	raw, _ = os.ReadFile(hub)
	if !strings.Contains(string(raw), "Curated overview.") || !strings.Contains(string(raw), "202609060009.md") {
		t.Fatal("refresh lost overview or new link")
	}
}

func TestSourceDriftInvalidatesWithoutRewritingMeaning(t *testing.T) {
	v := t.TempDir()
	os.MkdirAll(filepath.Join(v, "agent/facts"), 0700)
	os.MkdirAll(filepath.Join(v, "01 - Notes"), 0700)
	p := filepath.Join(v, "01 - Notes/202609060001.md")
	raw := "---\ntype: Fact\nstatus: stable\ntitle: Recovery\nsource: catalogue_source=src-example; content_hash=abc\n---\nUse violet recovery.\n"
	os.WriteFile(p, []byte(raw), 0600)
	n, e := MarkSourceDrift(v, "src-example")
	if e != nil || n != 1 {
		t.Fatalf("drift %d %v", n, e)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "Use violet recovery.") || field(string(b), "review_state") != "needs_review" {
		t.Fatal("meaning lost or review missing")
	}
	n, e = MarkSourceDrift(v, "src-example")
	if n != 0 || e != nil {
		t.Fatal("drift retry changed note")
	}
	idx := filepath.Join(t.TempDir(), "index.json")
	knowledge.Write(idx, knowledge.Generate(knowledge.Config{VaultDir: v, RunGH: func(...string) ([]byte, error) { return []byte("[]"), nil }}, time.Now()))
	if q := knowledge.Query(idx, "source:vault-fact recovery", 10, true); len(q.Matches) != 0 {
		t.Fatal("needs-review note remains active")
	}
}

// TestWriteRosterPointerTargetsMOCsHub locks in P10's redirect
// (run/iteration-queue.md): the agents routing note lands at
// "02 - MOCs/Agents.md", not the retired "03 - Agents/index.md" -- the
// per-area index.md files were all replaced by title-named hubs there,
// leaving exactly one index.md in the vault (agent/index.md). Previously
// uncovered by any test.
func TestWriteRosterPointerTargetsMOCsHub(t *testing.T) {
	v := t.TempDir()
	os.MkdirAll(filepath.Join(v, "agent"), 0700)
	os.MkdirAll(filepath.Join(v, "99 - Meta"), 0700)
	os.WriteFile(filepath.Join(v, "99 - Meta/tags.md"), []byte("`source`"), 0600)
	roster := filepath.Join(t.TempDir(), "agent-roster.md")
	os.WriteFile(roster, []byte("# Agent roster\n"), 0600)

	if e := WriteRosterPointer(v, roster); e != nil {
		t.Fatal(e)
	}

	hub := filepath.Join(v, "02 - MOCs/Agents.md")
	if _, e := os.Stat(hub); e != nil {
		t.Fatalf("expected roster pointer at %s: %v", hub, e)
	}
	if _, e := os.Stat(filepath.Join(v, "03 - Agents/index.md")); e == nil {
		t.Fatal("WriteRosterPointer also wrote the retired 03 - Agents/index.md path")
	}
	raw, e := os.ReadFile(hub)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(raw), "id: agents-roster-routing") {
		t.Fatal("roster pointer lost its id frontmatter")
	}
}
