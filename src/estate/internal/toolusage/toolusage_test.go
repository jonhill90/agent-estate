package toolusage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

// syntheticTranscript writes a fixture that never contains real transcript
// content -- only shapes engineered to exercise the structural walk. It
// deliberately mixes:
//   - real tool_use blocks (Bash, Read, Agent, Bash again)
//   - a Bash command that runs `knowledge query` unscoped
//   - a Bash command that runs `knowledge query` WITH `source:` scoping
//   - plain assistant TEXT that mentions the words "knowledge query" in
//     prose, and a tool_result block pasting output that also contains the
//     phrase -- neither must be counted, since counting these is exactly
//     the bug (#1096 parse 1: 53 "occurrences" for a turn that may have run
//     none)
//   - a malformed JSON line, which must be counted as unparsable, not fatal
func syntheticTranscript(t *testing.T, dir, name string) string {
	t.Helper()
	lines := []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"I will run a knowledge query to check this."}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"1","name":"Bash","input":{"command":"go run ./src/estate knowledge query \"what did jon decide\""}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"ran a knowledge query, got 3 results"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"2","name":"Read","input":{"file_path":"/tmp/x.go"}}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"3","name":"Bash","input":{"command":"go run ./src/estate knowledge query \"source:corpus-directive what is decided\""}}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"4","name":"Agent","input":{"description":"scout"}}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"5","name":"Bash","input":{"command":"go build ./..."}}]}}`,
		`not json at all`,
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// TestParse_CountsStructurally_NotByRegexOverText is the failing-before,
// passing-after test for #1096: before this package existed, the only way
// to answer "how many knowledge queries did this turn run" was a regex over
// the whole transcript, which counts the prose line and the tool_result
// paste above as invocations too -- 3, not 2. Parse must report 2.
func TestParse_CountsStructurally_NotByRegexOverText(t *testing.T) {
	dir := t.TempDir()
	path := syntheticTranscript(t, dir, "session.jsonl")

	c, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got, want := c.Tools["Bash"], 3; got != want {
		t.Errorf("Bash count = %d, want %d", got, want)
	}
	if got, want := c.Tools["Read"], 1; got != want {
		t.Errorf("Read count = %d, want %d", got, want)
	}
	if got, want := c.Tools["Agent"], 1; got != want {
		t.Errorf("Agent count = %d, want %d", got, want)
	}
	// The load-bearing assertion: a regex over the raw text would find the
	// phrase "knowledge query" in the prose line AND the tool_result paste,
	// in addition to the two real Bash invocations -- 4, not 2. Structural
	// walk must find exactly the two real Bash tool_use invocations.
	if got, want := c.KnowledgeQuery, 2; got != want {
		t.Errorf("KnowledgeQuery = %d, want %d (structural walk must not count prose or tool_result text)", got, want)
	}
	if got, want := c.KnowledgeQueryScoped, 1; got != want {
		t.Errorf("KnowledgeQueryScoped = %d, want %d", got, want)
	}
	if got, want := c.Malformed, 1; got != want {
		t.Errorf("Malformed = %d, want %d", got, want)
	}
	if c.Lines == 0 {
		t.Error("Lines should be > 0")
	}
}

func TestParse_MissingFile(t *testing.T) {
	if _, err := Parse(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Error("expected an error for a missing transcript")
	}
}

func TestMerge_SumsAcrossTranscripts(t *testing.T) {
	dir := t.TempDir()
	p1 := syntheticTranscript(t, dir, "a.jsonl")
	p2 := syntheticTranscript(t, dir, "b.jsonl")
	c1, err := Parse(p1)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Parse(p2)
	if err != nil {
		t.Fatal(err)
	}
	m := Merge([]Counts{c1, c2})
	if got, want := m.Tools["Bash"], 6; got != want {
		t.Errorf("merged Bash = %d, want %d", got, want)
	}
	if got, want := m.KnowledgeQuery, 4; got != want {
		t.Errorf("merged KnowledgeQuery = %d, want %d", got, want)
	}
	if got, want := m.KnowledgeQueryScoped, 2; got != want {
		t.Errorf("merged KnowledgeQueryScoped = %d, want %d", got, want)
	}
}

// TestFindTranscript_JoinsBySessionID exercises the exact join #990 built
// and #1096 reports has no consumer: a session id resolving to a transcript
// nested arbitrarily deep under a project-directory tree, by filename alone.
func TestFindTranscript_JoinsBySessionID(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "-some-encoded-working-directory")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := syntheticTranscript(t, projDir, "abc123-session.jsonl")
	// strip .jsonl for the id FindTranscript is given
	sessionID := "abc123-session"

	got, err := FindTranscript(root, sessionID)
	if err != nil {
		t.Fatalf("FindTranscript: %v", err)
	}
	if got != want {
		t.Errorf("FindTranscript = %q, want %q", got, want)
	}
}

func TestFindTranscript_NotFound(t *testing.T) {
	root := t.TempDir()
	if _, err := FindTranscript(root, "does-not-exist"); err == nil {
		t.Error("expected an error when no transcript matches")
	}
}

func TestFindTranscript_Ambiguous(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	os.MkdirAll(a, 0o755)
	os.MkdirAll(b, 0o755)
	syntheticTranscript(t, a, "dup.jsonl")
	syntheticTranscript(t, b, "dup.jsonl")
	if _, err := FindTranscript(root, "dup"); err == nil {
		t.Error("expected an error when more than one transcript matches the same session id")
	}
}

func strPtr(s string) *string { return &s }

// TestResolve_JoinsOnIDNotIssue guards against #1096's own third failed
// attempt: joining on ledger.Record.Issue, whose type was never checked
// first. Resolve joins on ID, a plain string, and this test's records
// deliberately carry an Issue value that would break a naive int-keyed join
// if one were attempted here.
func TestResolve_JoinsOnIDNotIssue(t *testing.T) {
	records := []ledger.Record{
		{ID: "turn-a", Issue: "1080", State: ledger.Complete, SessionID: strPtr("session-a")},
		{ID: "turn-b", Issue: "not-a-number", State: ledger.Complete, SessionID: strPtr("session-b")},
	}
	r, err := Resolve(records, "turn-b")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.SessionID == nil || *r.SessionID != "session-b" {
		t.Errorf("Resolve returned wrong record: %+v", r)
	}

	if _, err := Resolve(records, "missing"); err == nil {
		t.Error("expected an error for an unknown turn id")
	}
}

func TestRecentWithSession_SkipsUnterminatedAndMissingSessions(t *testing.T) {
	records := []ledger.Record{
		{ID: "1", State: ledger.Complete, SessionID: strPtr("s1")},
		{ID: "2", State: ledger.Dispatched, SessionID: strPtr("s2")}, // not terminal
		{ID: "3", State: ledger.Complete, SessionID: nil},            // no session id
		{ID: "4", State: ledger.Complete, SessionID: strPtr("")},     // empty session id
		{ID: "5", State: ledger.Failed, SessionID: strPtr("s5")},
	}
	got := RecentWithSession(records, 10)
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2: %+v", len(got), got)
	}
	// most recent first
	if got[0].ID != "5" || got[1].ID != "1" {
		t.Errorf("wrong order: %+v", got)
	}
}

func TestRecentWithSession_RespectsLimit(t *testing.T) {
	records := []ledger.Record{
		{ID: "1", State: ledger.Complete, SessionID: strPtr("s1")},
		{ID: "2", State: ledger.Complete, SessionID: strPtr("s2")},
		{ID: "3", State: ledger.Complete, SessionID: strPtr("s3")},
	}
	got := RecentWithSession(records, 2)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].ID != "3" || got[1].ID != "2" {
		t.Errorf("wrong order/selection: %+v", got)
	}
}
