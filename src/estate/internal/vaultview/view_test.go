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
