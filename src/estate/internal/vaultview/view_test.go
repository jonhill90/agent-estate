package vaultview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCollisionGuardSeesFactsSubdir closes agent-estate#1307: the id-collision
// guard read the flat "01 - Notes" root plus its own NotesDir
// ("01p - Parameters") only, so an id already claimed by a note under
// "01f - Facts" -- an earned subdir #1309 fixed candidates.go for, but never
// applied here -- was invisible to it. Obsidian ids are a single global
// namespace across every subdir; 352 Facts notes and 3,217 Parameter notes
// share it.
//
// Pre-plants a Facts note at exactly the id Write() would otherwise mint for
// the fixture row (Row.At = 1788739200 -> "20260907000000", the same fixture
// timestamp TestProjectionStableIdentityAndRetirement already uses with an
// empty `used` set) and asserts the guard steps past it rather than reusing
// it. FAILS on main before agent-estate#1307's fix: the flat-root check never
// looks under 01f - Facts at all.
func TestCollisionGuardSeesFactsSubdir(t *testing.T) {
	v := t.TempDir()
	factsDir := filepath.Join(v, "01 - Notes", "01f - Facts")
	if err := os.MkdirAll(factsDir, 0700); err != nil {
		t.Fatal(err)
	}
	collidingID := "20260907000000" // what At=1788739200 mints against an empty `used` set
	factPath := filepath.Join(factsDir, collidingID+".md")
	factBody := "---\ntype: Fact\nid: \"" + collidingID + "\"\n---\n\n# Pre-existing fact\n\nAlready claims this id.\n"
	if err := os.WriteFile(factPath, []byte(factBody), 0600); err != nil {
		t.Fatal(err)
	}

	r, err := Write(v, []Row{{Item: "a", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "open", Body: "New parameter"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Mapping["a"] == collidingID {
		t.Fatalf("minted id %q -- already claimed by 01f - Facts/%s.md, invisible to the collision guard", r.Mapping["a"], collidingID)
	}
	got, err := os.ReadFile(factPath)
	if err != nil || string(got) != factBody {
		t.Fatalf("pre-existing fact file was modified: err=%v body=%q", err, string(got))
	}
}

// TestCollisionGuardSeesFourteenDigitFlatIDs closes the adjacent bug named in
// agent-estate#1307: the flat-root check's regex matched only 12-digit ids
// (`^\d{12}\.md$`), while every live note id is 14 digits, YYYYMMDDHHMMSS
// (agent-estate#1289). Even a note the flat-root check DOES look at -- no
// subdir involved at all -- was invisible if its id used the current, real
// format. FAILS on main before agent-estate#1307's fix: a 14-digit filename
// does not match a 12-digit-exact regex.
func TestCollisionGuardSeesFourteenDigitFlatIDs(t *testing.T) {
	v := t.TempDir()
	notesRoot := filepath.Join(v, "01 - Notes")
	if err := os.MkdirAll(notesRoot, 0700); err != nil {
		t.Fatal(err)
	}
	collidingID := "20260907000000" // what At=1788739200 mints against an empty `used` set
	flatPath := filepath.Join(notesRoot, collidingID+".md")
	body := "flat 14-digit note, no subdir, no marker -- not vaultview-managed"
	if err := os.WriteFile(flatPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	r, err := Write(v, []Row{{Item: "a", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "open", Body: "New parameter"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Mapping["a"] == collidingID {
		t.Fatalf("minted id %q -- already claimed by 01 - Notes/%s.md, invisible to the 12-digit-only regex", r.Mapping["a"], collidingID)
	}
}

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
	rows := []Row{
		{Item: "a", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "acted", Body: "Selected in the first batch."},
		{Item: "b", At: 1788739200, Kind: "parameter", Weight: "hard", Status: "acted", Body: "Not selected in the second batch."},
	}
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

// TestDirectiveDoesNotPresentAsStandingLaw is the dangerous defect named in
// the brief: an old one-time task directive rendered as apparent standing
// law, because nothing distinguished a spent instruction from a live rule.
// Lifted from the abandoned PR #1288 (fix/vaultview-projection-repair);
// its third assertion is NOT lifted as written -- see below.
//
// #1288 asserted the description points at `standinglaw.go`
// (corpus.StandingLawSet). Read that file: it is a tiny, human-declared,
// hash-pinned list (1 member today) force-injected into every dispatched
// turn -- a completely different mechanism from this package's
// standing-rule tag, and unrelated to any Parameter/Directive projection
// note. Pointing a spent directive's description at it would assert an
// authority relationship that does not exist. The corpus item already
// cited in this note's own frontmatter (`corpus_item`) is the real
// authority -- that is what a reader should be sent back to, not a file
// that has nothing to do with this note.
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
	if !strings.Contains(s, "corpus item cited above") {
		t.Fatalf("scope statement does not point at the real authority (the cited corpus item): %s", s)
	}
}

// TestEnduringDirectiveThatWasActedOnKeepsStandingRule is agent-estate#1313's
// own required proof, the direction #1298 lacked: status='acted' means
// someone acted on this, not that it is spent -- an enduring rule that was
// followed is marked 'acted' identically to a one-time order that was
// carried out, and #1298 stripped both alike. Confirmed on the live vault,
// both kind=directive weight=hard status=acted: it-879d138d5eb7c570 ("The
// estate tick must never merge a PR on green CI alone...", the rule
// governing the merge protocol the Director runs on) lost the tag; the
// fixture below reproduces its exact shape.
//
// The signal used is resolved_to -- Read()'s own query already selects it
// as row.Title ("coalesce(i.resolved_to,'') title"), the corpus's own
// judgement that this specific statement became a durable, reusable
// operator parameter, not a one-time task. It was already flowing into
// this package before this fix; nothing consulted it in the directive+acted
// branch. Measured 2026-09-08: 85 of the 1,237 notes #1298 stripped already
// carried a resolved_to-shaped title and were false negatives.
//
// This does NOT close the gap completely -- see this file's own comment at
// the guard this test pins for what remains unresolved (an enduring rule
// recorded as free text, never assigned a resolved_to key, has no
// structured signal in the corpus at all).
func TestEnduringDirectiveThatWasActedOnKeepsStandingRule(t *testing.T) {
	v := t.TempDir()
	rows := []Row{{
		Item: "d3", Prompt: "p1", At: 1788739200, Kind: "directive", Weight: "hard", Status: "acted",
		Title: "merge_policy=ci_green_and_independent_review_required",
		Body:  "The estate tick must never merge a PR on green CI alone -- it needs an actual APPROVED review.",
	}}
	r, e := Write(v, rows)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(v, NotesDir, r.Mapping["d3"]+".md")
	b, _ := os.ReadFile(p)
	s := string(b)
	if !strings.Contains(s, "standing-rule") {
		t.Fatalf("an enduring rule with a corpus-canonicalized resolved_to lost standing-rule just because its status is 'acted': %s", s)
	}
	if strings.Contains(s, "## Scope") {
		t.Fatalf("an enduring, corpus-canonicalized rule was marked as a spent one-time task: %s", s)
	}
	if !strings.Contains(s, "status: stable") {
		t.Fatalf("an enduring rule must present as stable/authority-bearing: %s", s)
	}
}

// TestAmbiguousProjectionRemainsUnresolved is lifted from #1288, was
// t.Skip-ped pending agent-estate#1297, and is now implemented.
//
// Its four assertions were judged, not implemented to blindly (the same
// discipline #1298 applied to the standinglaw.go assertion it rejected):
// resolution/needs-editorial-review/not-stable/not-title all held up.
// "status: stable" -> "status: draft" reuses inmaps-spec.md §2's own closed
// status enum (draft = Inbox state) rather than inventing a fourth value;
// "resolution: unresolved" is a genuinely new fact this schema had no slot
// for (status governs publication lifecycle, not whether a subject was
// ever determined), so a new key is warranted, not redundant. Measured
// 2026-09-07: 10 of 3,217 notes hit the fallback this pins.
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
