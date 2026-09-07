package vaultview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectionStableIdentityAndRetirement(t *testing.T) {
	v := t.TempDir()
	rows := []Row{{Item: "b", Prompt: "p", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "acted", Body: "fixture body text with an inline tag #NNN for escaping"}, {Item: "a", Prompt: "q", At: 1788739200, Kind: "question", Weight: "hard", Status: "open", Body: "A fixture question with enough words to name a subject"}}
	first, err := Write(v, rows)
	if err != nil {
		t.Fatal(err)
	}
	if first.Mapping["a"] != "202609070001" || first.Mapping["b"] != "202609070002" {
		t.Fatal(first)
	}
	second, err := Write(v, rows)
	if err != nil || second.Changed != 0 {
		t.Fatalf("rerun %+v %v", second, err)
	}
	rows[1].Status = "dropped"
	third, err := Write(v, rows)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(v, NotesDir, third.Mapping["a"]+".md")
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "status: deprecated") {
		t.Fatal(string(b))
	}
	rows = append(rows, Row{Item: "earlier", Prompt: "p", At: 1788739100, Kind: "thought", Weight: "hard", Status: "open"})
	fourth, err := Write(v, rows)
	if err != nil || fourth.Mapping["a"] != first.Mapping["a"] {
		t.Fatalf("identity moved: %+v %v", fourth, err)
	}
	b, _ = os.ReadFile(filepath.Join(v, NotesDir, first.Mapping["b"]+".md"))
	if strings.Contains(string(b), "fixture #NNN") {
		t.Fatal("unescaped inline tag")
	}
}
func TestProjectionRefusesUnmanagedCollision(t *testing.T) {
	v := t.TempDir()
	p := filepath.Join(v, NotesDir, "202609070001.md")
	os.MkdirAll(filepath.Dir(p), 0700)
	os.WriteFile(p, []byte("human note"), 0600)
	_, err := Write(v, []Row{{Item: "a", At: 1788739200, Kind: "parameter", Weight: "hard"}})
	if err == nil {
		t.Fatal("overwrote unmanaged note")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "human note" {
		t.Fatal("changed unmanaged bytes")
	}
}

func TestPartialBatchDoesNotWithdrawUnselectedRows(t *testing.T) {
	v := t.TempDir()
	rows := []Row{{Item: "a", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "acted", Body: "fixture parameter body with enough words to name a subject"}, {Item: "b", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "acted", Body: "another fixture parameter body with enough words to name a subject"}}
	r, e := Write(v, rows)
	if e != nil {
		t.Fatal(e)
	}
	_, e = WriteBatch(v, rows[:1])
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(v, NotesDir, r.Mapping["b"]+".md"))
	if !strings.Contains(string(b), "status: stable") {
		t.Fatal(string(b))
	}
}

func TestProjectionPreservesAssociations(t *testing.T) {
	v := t.TempDir()
	rows := []Row{{Item: "a", Prompt: "p", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "acted", Body: "Original"}}
	r, e := Write(v, rows)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(v, NotesDir, r.Mapping["a"]+".md")
	b, _ := os.ReadFile(p)
	b = []byte(strings.Replace(string(b), "tags: [", "tags: [azure, ", 1) + "\n## Relations\n\n- relates_to: [Other](other.md)\n")
	os.WriteFile(p, b, 0600)
	rows[0].Body = "Revised"
	rows[0].Status = "dropped"
	if _, e = Write(v, rows); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(p)
	if !strings.Contains(string(b), "azure") || !strings.Contains(string(b), "## Relations") || strings.Contains(string(b), "standing-rule") {
		t.Fatalf("association lost or stale structural tag: %s", b)
	}
	r, e = Write(v, rows)
	if e != nil || r.Changed != 0 {
		t.Fatalf("rerun: %+v %v", r, e)
	}
}

// TestProjectionUsesReadableMetadataWithoutBoilerplate is agent-estate's
// projection-repair slice 1, defect (1) and (3): a fallback title that
// names nothing ("Directive it-<hash>"), a generic boilerplate description
// template identical on every note, and a repetitive footer sentence.
// Real nested layout (NotesDir, not a flat temp dir).
func TestProjectionUsesReadableMetadataWithoutBoilerplate(t *testing.T) {
	v := t.TempDir()
	rows := []Row{{
		Item: "d1", Prompt: "p1", At: 1788739200, Kind: "directive", Weight: "hard", Status: "acted",
		Body: "Never write to the shared checkout directly; always dispatch through a worktree so no lane can yank the tree out from under another.",
	}}
	r, e := Write(v, rows)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(v, NotesDir, r.Mapping["d1"]+".md")
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	s := string(b)
	if strings.Contains(s, "title: \"Directive d1\"") || strings.Contains(s, "Directive d1\n") {
		t.Fatalf("title is still the type-plus-ID fallback, names nothing: %s", s)
	}
	if !strings.Contains(s, "shared checkout") {
		t.Fatalf("title/description does not name the actual subject: %s", s)
	}
	if strings.Contains(s, "consult the cited item and source prompt for authority") {
		t.Fatalf("boilerplate description template still present: %s", s)
	}
	if strings.Contains(s, "Projection of corpus item") {
		t.Fatalf("repetitive footer still present: %s", s)
	}
	// Provenance retained once, in frontmatter, not restated in the body.
	if !strings.Contains(s, "corpus_item: \"d1\"") {
		t.Fatalf("provenance dropped entirely, not just the footer: %s", s)
	}
}

// TestDirectiveDoesNotPresentAsStandingLaw is the dangerous defect named in
// the brief: an old one-time task directive rendered as apparent standing
// law, because nothing distinguished a spent instruction from a live rule.
// The fix is explicit scope, not a judgment call on which directives are
// "really" resolved (the corpus has no lifecycle column to support that).
func TestDirectiveDoesNotPresentAsStandingLaw(t *testing.T) {
	v := t.TempDir()
	rows := []Row{{
		Item: "d2", Prompt: "p1", At: 1788739200, Kind: "directive", Weight: "hard", Status: "acted",
		Body: "Rename the corpus.sqlite3 file to ledger.sqlite3 across every reference in the estate.",
	}}
	r, e := Write(v, rows)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(v, NotesDir, r.Mapping["d2"]+".md")
	b, _ := os.ReadFile(p)
	s := string(b)
	if strings.Contains(s, "standing-rule") {
		t.Fatalf("directive still tagged standing-rule -- exactly the misleading-authority defect: %s", s)
	}
	if !strings.Contains(s, "may already be resolved") && !strings.Contains(s, "one-time task instruction") {
		t.Fatalf("no explicit scope statement distinguishing a task directive from standing law: %s", s)
	}
	if !strings.Contains(s, "standinglaw.go") {
		t.Fatalf("description does not point at the real authority (StandingLawSet): %s", s)
	}
}

// TestAmbiguousProjectionRemainsUnresolved is the harder case the brief
// names explicitly: a fragment whose text does not determine a subject.
// SPEC §2 requires this be labelled, not forced into an invented title.
func TestAmbiguousProjectionRemainsUnresolved(t *testing.T) {
	v := t.TempDir()
	rows := []Row{{Item: "frag1", Prompt: "p1", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "acted", Body: "tmux"}}
	r, e := Write(v, rows)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(v, NotesDir, r.Mapping["frag1"]+".md")
	b, _ := os.ReadFile(p)
	s := string(b)
	if !strings.Contains(s, "resolution: unresolved") {
		t.Fatalf("an undeterminable-subject fragment was not marked unresolved: %s", s)
	}
	if !strings.Contains(s, "needs-editorial-review") {
		t.Fatalf("unresolved fragment carries no review tag: %s", s)
	}
	if strings.Contains(s, "status: stable") {
		t.Fatalf("unresolved fragment must not present as stable/authority-bearing: %s", s)
	}
	if strings.Contains(s, "title: \"tmux\"") {
		t.Fatalf("a 1-word fragment was accepted as a determined subject rather than flagged: %s", s)
	}
}

// TestProjectionRegenerationPreservesIdentityAndEditorialFields is the
// "previously edited/tagged note (must survive regeneration intact)" harder
// case, plus SPEC §2's "accepted editorial title/description must survive
// regeneration" -- using the existing managed publication mechanism
// (notemeta.Merge plus an editorial marker), not a new sidecar store.
func TestProjectionRegenerationPreservesIdentityAndEditorialFields(t *testing.T) {
	v := t.TempDir()
	rows := []Row{{Item: "e1", Prompt: "p1", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "acted", Body: "raw corpus body text, not yet reviewed"}}
	r, e := Write(v, rows)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(v, NotesDir, r.Mapping["e1"]+".md")
	b, _ := os.ReadFile(p)
	// A reviewer accepts an editorial title/description and an extra
	// associative tag, and marks the note reviewed -- rewrite the exact
	// frontmatter lines rather than a fragile substring replace.
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, "title:"):
			out = append(out, `title: "Reviewer-accepted subject"`)
		case strings.HasPrefix(line, "description:"):
			out = append(out, `description: "Reviewer-accepted description text."`)
		case strings.HasPrefix(line, "tags:"):
			out = append(out, strings.Replace(line, "tags: [", "tags: [azure, ", 1))
		default:
			out = append(out, line)
		}
	}
	s := strings.Join(out, "\n")
	// editorial: reviewed must land inside frontmatter, before the closing "---".
	s = strings.Replace(s, "\n---\n", "\neditorial: reviewed\n---\n", 1)
	if err := os.WriteFile(p, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}

	// Regenerate against a CHANGED corpus body -- the editorial fields and
	// the human tag must survive, not be overwritten by fresh derivation.
	rows[0].Body = "a completely different corpus body from a later revision"
	r2, e := Write(v, rows)
	if e != nil {
		t.Fatal(e)
	}
	if r2.Mapping["e1"] != r.Mapping["e1"] {
		t.Fatalf("identity moved across regeneration: %+v vs %+v", r2, r)
	}
	b2, _ := os.ReadFile(p)
	s2 := string(b2)
	if !strings.Contains(s2, "Reviewer-accepted subject") {
		t.Fatalf("accepted editorial title did not survive regeneration: %s", s2)
	}
	if !strings.Contains(s2, "azure") {
		t.Fatalf("previously added associative tag did not survive regeneration: %s", s2)
	}
	if !strings.Contains(s2, "Reviewer-accepted description text") {
		t.Fatalf("accepted editorial description did not survive regeneration: %s", s2)
	}
	// Body content itself is NOT editorially frozen -- the corpus remains
	// authoritative for the item's actual text (package doc comment); only
	// title/description/tags are the accepted editorial layer. The new
	// corpus body must still come through.
	if !strings.Contains(s2, "a completely different corpus body") {
		t.Fatalf("body did not update from the current corpus row -- the corpus is supposed to remain authoritative for content: %s", s2)
	}
}
