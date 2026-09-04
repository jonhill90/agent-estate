package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/src/tui/internal/library"
)

func TestDefaultCorpusPath_JoinsHomeCorpusLedger(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := defaultCorpusPath()
	want := filepath.Join(home, "corpus", "ledger.sqlite3")
	if got != want {
		t.Fatalf("got %q, want %q -- must match src/estate/internal/corpus.Path()'s own default", got, want)
	}
}

func TestDefaultCorpusPath_EmptyHomeIsUndiscoverable(t *testing.T) {
	t.Setenv("HOME", "")
	if got := defaultCorpusPath(); got != "" {
		t.Fatalf("got %q, want empty -- an unset $HOME must be treated as \"nothing found\", not guessed at", got)
	}
}

func TestCorpusReadOnlyURI_UsesModeRoImmutable(t *testing.T) {
	got := corpusReadOnlyURI("/some/path/ledger.sqlite3")
	want := "file:/some/path/ledger.sqlite3?mode=ro&immutable=1"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveCorpusSource_ExplicitMissingPathIsUnavailable(t *testing.T) {
	_, ok, unavailable := resolveCorpusSource(filepath.Join(t.TempDir(), "absent.sqlite3"))
	if ok {
		t.Fatal("got ok=true for a path that does not exist")
	}
	if unavailable == "" {
		t.Error("got empty unavailable message, want one naming the path looked for")
	}
}

func TestResolveCorpusSource_NoConfigAndNoDefaultIsUnavailable(t *testing.T) {
	t.Setenv("HOME", "") // defaultCorpusPath() resolves to "" too
	_, ok, unavailable := resolveCorpusSource("")
	if ok {
		t.Fatal("got ok=true with neither an explicit path nor a discoverable HOME")
	}
	if unavailable == "" {
		t.Error("got empty unavailable message")
	}
}

// TestResolveCorpusSource_ExplicitPathIsWrappedReadOnly proves the path this
// package hands to library.ReadItems is the mode=ro URI form, never the
// bare path -- the read-only boundary agent-estate#1088 requires stated and
// checked, not merely asserted in a comment.
func TestResolveCorpusSource_ExplicitPathIsWrappedReadOnly(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "corpus-*.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	src, ok, unavailable := resolveCorpusSource(f.Name())
	if !ok || unavailable != "" {
		t.Fatalf("got ok=%v unavailable=%q, want ok with no message", ok, unavailable)
	}
	path, err := src()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, "file:") || !strings.Contains(path, "mode=ro") {
		t.Fatalf("got %q, want the file:...?mode=ro URI form, never a bare path", path)
	}
}

// TestOperatorCorpusReadsThroughTheReadOnlyURI is the fixture-database,
// end-to-end proof this pane's real seam (buildLibraryFetch composed with
// resolveCorpusSource's own ledgerSource) can actually read a corpus opened
// the mode=ro way -- a synthetic fixture, never the operator's real
// ~/corpus (agent-estate#1088's own no-real-corpus-in-tests constraint).
// This is the test that fails before this change and passes after: on the
// parent commit (2df4d3f) there is no resolveCorpusSource, no
// corpusReadOnlyURI, and no second Source for buildLibraryFetch to compose
// with -- the pane had exactly one corpus, the shared ledger.
func TestOperatorCorpusReadsThroughTheReadOnlyURI(t *testing.T) {
	sqlitePath, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 binary not on PATH")
	}

	dir := t.TempDir()
	corpus := filepath.Join(dir, "ledger.sqlite3")
	setup := `
CREATE TABLE prompts (id TEXT PRIMARY KEY, at INTEGER, context TEXT, text_clean TEXT, text_raw TEXT);
CREATE TABLE items (id TEXT PRIMARY KEY, prompt_id TEXT, kind TEXT, weight TEXT, status TEXT, status_reason TEXT, resolved_to TEXT, body TEXT);
CREATE VIEW live_parameters AS SELECT * FROM items WHERE weight='hard' AND resolved_to IS NOT NULL;
INSERT INTO prompts VALUES ('mp-fixture0000001', 1787424786, '', 'fixture prompt text', '');
INSERT INTO items VALUES ('it-fixture00000001', 'mp-fixture0000001', 'parameter', 'hard', 'acknowledged', '', 'fixture_key=fixture_value', 'a fixture body, never real corpus text');
`
	if out, err := exec.Command(sqlitePath, corpus, setup).CombinedOutput(); err != nil {
		t.Fatalf("setting up fixture corpus: %v: %s", err, out)
	}

	corpusSrc, ok, unavailable := resolveCorpusSource(corpus)
	if !ok || unavailable != "" {
		t.Fatalf("got ok=%v unavailable=%q", ok, unavailable)
	}

	fetch := buildLibraryFetch(corpusSrc, sqlitePath)
	if fetch == nil {
		t.Fatal("buildLibraryFetch returned nil for a configured source")
	}
	rows, err := fetch(library.ViewLiveParameters, "", "")
	if err != nil {
		t.Fatalf("fetch via the mode=ro URI: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "it-fixture00000001" {
		t.Fatalf("rows = %+v, want the one fixture row", rows)
	}
}

// TestBuildLibrarySources_NamesBothSlotsRegardlessOfConfiguration proves
// [c] always has the same two named slots to cycle through -- an
// unconfigured operator corpus (corpusSrc == nil) still gets a "operator"
// Source, rendered "not configured" rather than omitted.
func TestBuildLibrarySources_NamesBothSlotsRegardlessOfConfiguration(t *testing.T) {
	ledgerSrc := staticLedgerSource("/some/shared/copy.sqlite3")
	sources := buildLibrarySources(ledgerSrc, nil, "sqlite3")
	if len(sources) != 2 {
		t.Fatalf("got %d sources, want 2", len(sources))
	}
	if sources[0].Name != "shared" || sources[0].Fetch == nil {
		t.Fatalf("sources[0] = %+v, want a configured shared source", sources[0])
	}
	if sources[1].Name != "operator" || sources[1].Fetch != nil {
		t.Fatalf("sources[1] = %+v, want an unconfigured operator source", sources[1])
	}
}
