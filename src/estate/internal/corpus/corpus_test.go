package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A corpus that cannot be read must stop a dispatch, never degrade to
// "no constraints found".
func TestUnreadableCorpusIsAnError(t *testing.T) {
	t.Setenv("ESTATE_CORPUS", filepath.Join(t.TempDir(), "absent.sqlite3"))
	if _, err := Hard(); err == nil {
		t.Fatal("Hard() returned nil error for an absent corpus; it must refuse")
	}
}

func TestEmptyCorpusIsRefusedNotTreatedAsNoConstraints(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.sqlite3")
	if err := os.WriteFile(p, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ESTATE_CORPUS", p)
	if _, err := Hard(); err == nil {
		t.Fatal("Hard() accepted an unusable corpus; zero parameters must never read as 'no rules'")
	}
}

// The preamble must always state the true total and must always demand the
// agent query the rest -- the task-matched subset is never presented as the
// whole law.
func TestGroundingStatesFullCountAndDemandsIndependentCheck(t *testing.T) {
	ps := []Param{
		{Key: "tooling=cli_first", Body: "Prefer CLI-backed workflows."},
		{Key: "lang=go", Body: "The app is written in Go, never shell or python."},
	}
	g := Grounding("rewrite the shell dispatcher", ps)
	if !strings.Contains(g, "2 binding parameters") {
		t.Fatalf("grounding does not state the true total:\n%s", g)
	}
	if !strings.Contains(g, "NOT the whole law") {
		t.Fatalf("matched subset is not marked as partial:\n%s", g)
	}
	if !strings.Contains(g, "Query the\ncorpus yourself") {
		t.Fatalf("grounding does not require an independent check:\n%s", g)
	}
	if !strings.Contains(g, "lang=go") {
		t.Fatalf("a parameter matching the task was not surfaced:\n%s", g)
	}
}

func TestGroundingStillDemandsCheckWhenNothingMatches(t *testing.T) {
	g := Grounding("zzzz", []Param{{Key: "k", Body: "b"}})
	if !strings.Contains(g, "Query the\ncorpus yourself") {
		t.Fatal("grounding dropped the independent-check requirement when no parameter matched")
	}
}
