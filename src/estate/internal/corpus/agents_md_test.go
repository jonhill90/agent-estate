package corpus

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// WHY THIS TEST EXISTS. AGENTS.md's "Before you ask Jon anything" rule once
// named ~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3 -- a database
// with 0 live_parameters -- while every dispatch's own grounding actually
// read ~/corpus/ledger.sqlite3 via Path() (agent-estate#942). An agent
// following the doc exactly found nothing and could reasonably have
// concluded the record was empty -- the same "instrument that cannot see a
// thing looks exactly like the thing being absent" failure this repo names
// as its most common defect, sitting inside the rule written to prevent it.
//
// This pins the doc's claimed path to Path()'s real resolution so the two
// cannot drift apart again without the build noticing.
var corpusPathRE = regexp.MustCompile("Query the corpus\\.\\*\\* `([^`]+)`")

func TestAgentsMDCorpusPathMatchesResolvedPath(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(root, "AGENTS.md")
	doc, err := os.ReadFile(docPath)
	if err != nil {
		// Refuse rather than pass: a check that cannot find its subject has
		// not verified anything.
		t.Fatalf("cannot read %s, so nothing was verified: %v", docPath, err)
	}

	m := corpusPathRE.FindSubmatch(doc)
	if m == nil {
		t.Fatal("AGENTS.md's \"Before you ask Jon anything\" section no longer names a corpus path " +
			"in the expected form (`Query the corpus.** ...`) -- update this regex, don't drop the check")
	}
	claimed := string(m[1])

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ESTATE_CORPUS", "") // exercise the real default resolution, not a test override
	resolved, err := Path()
	if err != nil {
		t.Fatalf("Path() failed: %v", err)
	}

	want := "~" + resolved[len(home):]
	if claimed != want {
		t.Fatalf("AGENTS.md's \"query the corpus\" rule names %q but internal/corpus.Path() resolves to %q (%q with $HOME abbreviated).\n"+
			"Every dispatch's own grounding reads Path()'s resolution -- a documented path that disagrees with it is exactly agent-estate#942 again.",
			claimed, resolved, want)
	}
}
