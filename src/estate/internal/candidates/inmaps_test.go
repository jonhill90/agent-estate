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
	os.MkdirAll(filepath.Join(vault, "01 - Notes"), 0700)
	os.MkdirAll(filepath.Join(vault, "99 - Meta"), 0700)
	os.WriteFile(filepath.Join(vault, "99 - Meta/tags.md"), []byte("`kind/decision`"), 0600)
	os.WriteFile(filepath.Join(vault, "index.md"), []byte("---\nokf_version: \"0.1\"\n---\n\n# Facts\n\nintro\n"), 0600)
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
	// A2-COMPLETION (run/iteration-queue.md), fix pass (Director, OKF
	// §12/§8 adjudication): the capped index lives at the vault-root
	// index.md, not agent/index.md and not Start Here.md (a first pass
	// briefly tried the latter; OKF names the bundle-root index.md
	// FILENAME specifically as the sole legal okf_version carrier). An
	// accept must add a bullet there and leave the file's own intro prose
	// untouched.
	index, e := os.ReadFile(filepath.Join(vault, "index.md"))
	if e != nil {
		t.Fatalf("index.md not written by an INMAPS accept: %v", e)
	}
	if !strings.Contains(string(index), "intro") {
		t.Fatal("index.md's own intro prose was lost")
	}
	if !strings.Contains(string(index), r.Proposal.Title) {
		t.Fatal("accepted fact never landed in index.md")
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
	for _, d := range []string{"01 - Notes", "99 - Meta"} {
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

// TestMOCProposalsAndRefreshSeeNestedNoteSubdirs is the regression for a
// real defect found running Push 4.5's C4 against the live vault:
// MOCProposals/RefreshMOCs used filepath.Glob("01 - Notes/*.md") -- a
// FLAT, non-recursive pattern -- while the live vault's own note-subdirs
// registry (agent-estate#942) puts every real note one directory deeper,
// under "01 - Notes/01p - Parameters" or "01 - Notes/01f - Facts". Against
// that layout the glob matched zero files, so moc-propose returned no
// proposals no matter how far past the >=8 threshold a tag's count ran --
// measured directly: `moc-propose` printed `null` against a vault with
// several tags counted in the dozens. This test puts its 8 fixture notes
// in a nested subdir, the shape the flat glob could not see, so a
// regression back to Glob fails it immediately.
func TestMOCProposalsAndRefreshSeeNestedNoteSubdirs(t *testing.T) {
	v := t.TempDir()
	for _, d := range []string{"01 - Notes/01f - Facts", "99 - Meta"} {
		os.MkdirAll(filepath.Join(v, d), 0700)
	}
	os.WriteFile(filepath.Join(v, "99 - Meta/tags.md"), []byte("`kind/decision`"), 0600)
	for n := 1; n <= 8; n++ {
		name := fmt.Sprintf("20260907%04d.md", n)
		os.WriteFile(filepath.Join(v, "01 - Notes/01f - Facts", name), []byte("---\nstatus: stable\ntitle: Nested Example\ntags: [\"kind/decision\"]\n---\n"), 0600)
	}
	got, e := MOCProposals(v, true)
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 1 {
		t.Fatalf("8 nested notes past the threshold produced %d proposals, want 1: %v", len(got), got)
	}
	// The generated link must name the note's REAL nested path, not just
	// its bare filename -- a link built from filepath.Base(p) alone
	// (agent-estate#942's own subdir layout notwithstanding) points
	// Obsidian at a file that does not exist, since every real note here
	// lives one directory deeper than "01 - Notes" itself.
	draft, _ := os.ReadFile(got[0])
	if !strings.Contains(string(draft), "01f%20-%20Facts/20260907") {
		t.Fatalf("generated link does not name the note's nested subdir: %s", draft)
	}
	if e = ReviewMOC(v, filepath.Base(got[0]), "process:test", true, true); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(v, "01 - Notes/01f - Facts/202609070009.md"), []byte("---\nstatus: stable\ntitle: Nested Ninth\ntags: [\"kind/decision\"]\n---\n"), 0600)
	if _, e = RefreshMOCs(v, true); e != nil {
		t.Fatal(e)
	}
	hub := filepath.Join(v, "02 - MOCs/kind-decision.md")
	raw, _ := os.ReadFile(hub)
	if !strings.Contains(string(raw), "01f%20-%20Facts/202609070009.md") {
		t.Fatalf("refresh did not link the new nested note by its real path: %s", raw)
	}
}

// TestWalkNotesExcludesNonNoteMarkdownFiles is the negative case
// TestMOCProposalsAndRefreshSeeNestedNoteSubdirs never covered: that test
// only proves a real, canonically-named nested note is INCLUDED; nothing
// proved a non-note .md file is EXCLUDED. walkNotes's first recursive
// pass (a bare ".md" suffix check) admitted ANY markdown file under
// "01 - Notes" -- a per-subdir index.md or README, say -- while
// internal/knowledge/vault.go's own traversal of the identical directory
// has always filtered to the exact 12-digit canonical shape
// (agent-estate#1272, `^\d{12}\.md$`). Nothing of that non-conforming
// shape exists under "01 - Notes" today, so the looser check was latent,
// not live -- but it traded the old blind spot (missing every note) for a
// false-positive one (treating a future non-note file as one). This test
// puts a "README.md" and an "index.md" beside 8 real, canonically-named
// notes in the SAME nested subdir and asserts neither non-note file is
// ever counted toward the threshold, walked, or linked.
func TestWalkNotesExcludesNonNoteMarkdownFiles(t *testing.T) {
	v := t.TempDir()
	for _, d := range []string{"01 - Notes/01f - Facts", "99 - Meta"} {
		os.MkdirAll(filepath.Join(v, d), 0700)
	}
	os.WriteFile(filepath.Join(v, "99 - Meta/tags.md"), []byte("`kind/decision`"), 0600)
	os.WriteFile(filepath.Join(v, "01 - Notes/01f - Facts/README.md"), []byte("---\nstatus: stable\ntitle: Not A Note\ntags: [\"kind/decision\"]\n---\n"), 0600)
	os.WriteFile(filepath.Join(v, "01 - Notes/01f - Facts/index.md"), []byte("---\nstatus: stable\ntitle: Also Not A Note\ntags: [\"kind/decision\"]\n---\n"), 0600)
	for n := 1; n <= 8; n++ {
		name := fmt.Sprintf("20260907%04d.md", n)
		os.WriteFile(filepath.Join(v, "01 - Notes/01f - Facts", name), []byte("---\nstatus: stable\ntitle: Real Note\ntags: [\"kind/decision\"]\n---\n"), 0600)
	}
	notes, e := walkNotes(v)
	if e != nil {
		t.Fatal(e)
	}
	if len(notes) != 8 {
		t.Fatalf("walkNotes returned %d entries, want exactly the 8 canonically-named notes (README.md/index.md must be excluded): %v", len(notes), notes)
	}
	for _, p := range notes {
		if filepath.Base(p) == "README.md" || filepath.Base(p) == "index.md" {
			t.Fatalf("walkNotes included a non-note file: %s", p)
		}
	}
	got, e := MOCProposals(v, true)
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 1 {
		t.Fatalf("8 real notes past the threshold produced %d proposals, want 1: %v", len(got), got)
	}
	draft, _ := os.ReadFile(got[0])
	if strings.Contains(string(draft), "README.md") || strings.Contains(string(draft), "index.md") {
		t.Fatalf("generated links included a non-note file: %s", draft)
	}
}

// TestMOCProposalsSkipsUngovernedTagsRatherThanAborting is the regression
// for a second real defect found in the same C4 run: every real vault note
// carries structural/time tags (note, MM-YYYY, standing-rule) that are
// NOT in 99 - Meta/tags.md's governed vocabulary (they are generated
// bookkeeping, never MOC-eligible) -- and those groups clear the >=8
// threshold on nearly any vault with more than a handful of notes.
// MOCProposals treated validateINMAPS's rejection of an ungoverned tag as
// a hard error and returned it immediately, aborting the ENTIRE proposal
// batch before a single governed, genuinely MOC-worthy tag was ever
// reached. Measured directly against the live vault: `moc-propose` failed
// with "tag outside vocabulary: 07-2026" and produced zero proposals for
// tags like azure/deploy/estate that were all well past 8.
func TestMOCProposalsSkipsUngovernedTagsRatherThanAborting(t *testing.T) {
	v := t.TempDir()
	os.MkdirAll(filepath.Join(v, "01 - Notes"), 0700)
	os.MkdirAll(filepath.Join(v, "99 - Meta"), 0700)
	os.WriteFile(filepath.Join(v, "99 - Meta/tags.md"), []byte("`azure`"), 0600)
	for n := 1; n <= 9; n++ {
		name := fmt.Sprintf("20260907%04d.md", n)
		// "ungoverned" is NOT in tags.md's vocabulary; "azure" is. Both
		// clear the threshold. If the ungoverned one aborts the batch,
		// "azure" -- sorted after "ungoverned" alphabetically -- would
		// never be reached at all.
		os.WriteFile(filepath.Join(v, "01 - Notes", name), []byte("---\nstatus: stable\ntitle: Example\ntags: [\"ungoverned\",\"azure\"]\n---\n"), 0600)
	}
	got, e := MOCProposals(v, true)
	if e != nil {
		t.Fatalf("an ungoverned tag past the threshold aborted the whole batch: %v", e)
	}
	if len(got) != 1 || !strings.Contains(got[0], "moc-azure") {
		t.Fatalf("expected exactly one proposal for the governed tag 'azure', got %v", got)
	}
}

func TestSourceDriftInvalidatesWithoutRewritingMeaning(t *testing.T) {
	v := t.TempDir()
	os.MkdirAll(filepath.Join(v, "99 - Meta"), 0700)
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
// per-area index.md files were all replaced by title-named hubs there.
// The vault-root index.md is the sole index now (A2-COMPLETION,
// run/iteration-queue.md, retired agent/index.md entirely). Previously
// uncovered by any test.
func TestWriteRosterPointerTargetsMOCsHub(t *testing.T) {
	v := t.TempDir()
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

func TestNoteRegenerationPreservesAssociations(t *testing.T) {
	r := MemoryReview{Proposal: Proposal{Type: "Fact", Title: "T", Description: "D", Learning: "new", Tags: []string{"review"}}}
	old := "---\ntags: [azure]\n---\n\n## Relations\n\n- relates_to: [Other](other.md)\n"
	got := string(noteBytes("c", "202609070001", "stable", r, "2026-09-07T00:00:00Z", old))
	if !strings.Contains(got, "azure") || !strings.Contains(got, "## Relations") || !strings.Contains(got, "new") {
		t.Fatal(got)
	}
}

func TestTagNotesGovernedIdempotentAndAtomic(t *testing.T) {
	v := t.TempDir()
	os.MkdirAll(filepath.Join(v, "99 - Meta"), 0700)
	os.MkdirAll(filepath.Join(v, "01 - Notes/01f - Facts"), 0700)
	os.WriteFile(filepath.Join(v, "99 - Meta/tags.md"), []byte("`azure` `review`"), 0600)
	rel := "01 - Notes/01f - Facts/202609070001.md"
	p := filepath.Join(v, rel)
	original := "---\nid: 202609070001\ntype: Fact\ntags:\n  - review\nstatus: stable\n---\nMeaning unchanged.\ntags: body-example\n"
	os.WriteFile(p, []byte(original), 0600)
	if _, e := TagNotes(v, map[string][]string{rel: {"unregistered"}}, true); e == nil {
		t.Fatal("accepted unknown tag")
	}
	b, _ := os.ReadFile(p)
	if string(b) != original {
		t.Fatal("failed batch changed note")
	}
	if n, e := TagNotes(v, map[string][]string{rel: {"azure"}}, true); e != nil || n != 1 {
		t.Fatalf("%d %v", n, e)
	}
	b, _ = os.ReadFile(p)
	if !strings.Contains(string(b), "Meaning unchanged.") || !strings.Contains(string(b), "azure") {
		t.Fatal(string(b))
	}
	if !strings.Contains(string(b), "tags: body-example") {
		t.Fatal("body modified")
	}
	if n, e := TagNotes(v, map[string][]string{rel: {"azure"}}, true); e != nil || n != 0 {
		t.Fatalf("rerun %d %v", n, e)
	}
}

func TestTagNotesRefusesPublicationReceiptAndSymlink(t *testing.T) {
	v := t.TempDir()
	os.MkdirAll(filepath.Join(v, "99 - Meta"), 0700)
	os.MkdirAll(filepath.Join(v, "01 - Notes"), 0700)
	os.WriteFile(filepath.Join(v, "99 - Meta/tags.md"), []byte("`azure`"), 0600)
	rel := "01 - Notes/202609070001.md"
	p := filepath.Join(v, rel)
	raw := "---\nid: 202609070001\ntype: Fact\ncandidate_id: c\n---\nMeaning\n"
	os.WriteFile(p, []byte(raw), 0600)
	if _, e := TagNotes(v, map[string][]string{rel: {"azure"}}, true); e == nil {
		t.Fatal("stranded a publication receipt")
	}
	outside := filepath.Join(t.TempDir(), "target.md")
	os.WriteFile(outside, []byte(strings.Replace(raw, "candidate_id: c\n", "", 1)), 0600)
	os.Remove(p)
	os.Symlink(outside, p)
	before, _ := os.ReadFile(outside)
	if _, e := TagNotes(v, map[string][]string{rel: {"azure"}}, true); e == nil {
		t.Fatal("followed a symlink")
	}
	after, _ := os.ReadFile(outside)
	if string(before) != string(after) {
		t.Fatal("changed outside file")
	}
}
