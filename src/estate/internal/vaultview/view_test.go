package vaultview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectionStableIdentityAndRetirement(t *testing.T) {
	v := t.TempDir()
	rows := []Row{{Item: "b", Prompt: "p", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "acted", Body: "fixture #NNN"}, {Item: "a", Prompt: "q", At: 1788739200, Kind: "question", Weight: "hard", Status: "open", Body: "A question"}}
	first, err := Write(v, rows)
	if err != nil {
		t.Fatal(err)
	}
	if first.Mapping["a"] != "20260907000000" || first.Mapping["b"] != "20260907000001" {
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
	rows := []Row{{Item: "a", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "acted"}, {Item: "b", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "acted"}}
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

// TestWriteDoesNotCreateAnIndexInTheNotesDirectory pins Jon's recorded
// parameter that per-area index files become title-named MOC hubs: routing for
// these projections lives in "02 - MOCs", so a second index.md inside the notes
// directory is a duplicate routing surface over the same notes. The producer
// wrote one on every run until 2026-09-07.
func TestWriteDoesNotCreateAnIndexInTheNotesDirectory(t *testing.T) {
	vault := t.TempDir()
	if _, err := Write(vault, []Row{{
		Item: "it-aaaaaaaaaaaaaaaa", Prompt: "mp-bbbbbbbbbbbbbbbb", At: 1788818756,
		Kind: "parameter", Weight: "hard", Status: "acted",
		Title: "a title", Body: "a body",
	}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vault, NotesDir, "index.md")); err == nil {
		t.Fatal("Write created an index.md in the notes directory; routing belongs in 02 - MOCs")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat: %v", err)
	}
}

// TestProjectionCarriesContextAndSurvivesRegeneration pins the rule that a
// parameter without its context is a sentence an agent cannot interpret
// (corpus it-ad6b9208e64ff82, hard weight). Context is RENDERED by the
// producer from the corpus, not merged forward from the previous file:
// notemeta.Merge carries tags and a Relations section only, so context added
// out of band was silently destroyed on the next regeneration -- 3,217 notes
// lost it in a single pass on 2026-09-07.
func TestProjectionCarriesContextAndSurvivesRegeneration(t *testing.T) {
	v := t.TempDir()
	rows := []Row{{
		Item: "ctx", Prompt: "p", At: 1788739200, Kind: "parameter", Weight: "hard",
		Status: "acted", Body: "The rule body.", Title: "a rule",
		Context: "What was being discussed at the time.",
	}}
	first, err := Write(v, rows)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(v, NotesDir, first.Mapping["ctx"]+".md")
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "## Context") ||
		!strings.Contains(string(b), "What was being discussed at the time.") {
		t.Fatalf("context missing from a fresh projection:\n%s", b)
	}

	// Regenerating identical input must not strip it, and must write nothing.
	second, err := Write(v, rows)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed != 0 {
		t.Fatalf("an unchanged rerun wrote %d file(s)", second.Changed)
	}
	b2, _ := os.ReadFile(path)
	if !strings.Contains(string(b2), "What was being discussed at the time.") {
		t.Fatalf("regeneration destroyed the context:\n%s", b2)
	}

	// A description that merely says "consult the cited item" tells a reader
	// nothing and made every note read identically -- the shape Jon called junk.
	if strings.Contains(string(b2), "consult the cited item") {
		t.Fatal("description fell back to boilerplate when a real body was present")
	}
	if !strings.Contains(string(b2), "description: \"The rule body.\"") {
		t.Fatalf("description does not restate the rule:\n%s", b2)
	}
}

// TestProjectionTitleNamesTheSubject pins that a title says what the note is
// about. The producer used to fall back to "<Kind> <item-id>" -- "Directive
// it-b29425780b4cd06c" -- which names nothing, and made every hub entry and
// every search result unreadable. Jon's words on the result: they "all say the
// same bullshit".
func TestProjectionTitleNamesTheSubject(t *testing.T) {
	v := t.TempDir()
	rows := []Row{{
		Item: "t1", Prompt: "p", At: 1788739200, Kind: "directive", Weight: "hard",
		Status: "acted", Body: "Never write to the macOS keychain; a failed read is a report, not a repair.",
	}}
	res, err := Write(v, rows)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(v, NotesDir, res.Mapping["t1"]+".md"))
	if strings.Contains(string(b), "title: \"Directive t1\"") {
		t.Fatalf("title fell back to kind+id:\n%s", b)
	}
	if !strings.Contains(string(b), "Never write to the macOS keychain") {
		t.Fatalf("title does not name the subject:\n%s", b)
	}

	// A body too short to yield a clause must still produce a note, falling
	// back rather than emitting a fragment as a title.
	short := []Row{{Item: "t2", Prompt: "p", At: 1788739300, Kind: "thought",
		Weight: "hard", Status: "open", Body: "Cut #78."}}
	if _, err := Write(v, short); err != nil {
		t.Fatalf("a short body broke the producer: %v", err)
	}
}
