package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeMemgraphVault fixtures the vault's current shape (agent-estate#1304):
// index.md at the vault root, facts under 01 - Notes/01f - Facts/<id>.md --
// not the pre-relayout agent/index.md + agent/facts/<slug>.md this file
// used to fixture. ids in index and facts must agree, same as a real
// index.md bullet naming the same id its backing note is filed under.
func writeMemgraphVault(t *testing.T, index string, facts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	factsDir := filepath.Join(dir, "01 - Notes", "01f - Facts")
	if err := os.MkdirAll(factsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	for id, body := range facts {
		path := filepath.Join(factsDir, id+".md")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestBuildMemgraphFetchBuildsEdgesFromWikilinks: nodes come from the
// index, edges from each fact body's own [[wikilink]] -- a link to an id
// NOT in the index (dangling) or to itself must not become an edge.
func TestBuildMemgraphFetchBuildsEdgesFromWikilinks(t *testing.T) {
	index := "# Facts\n" +
		"- [[20260810150000]] — first\n" +
		"- [[20260810150001]] — second\n"
	facts := map[string]string{
		"20260810150000": "---\ntype: project\n---\nSee [[20260810150001]] and [[20260810159999]] and [[20260810150000]] itself.\n",
		"20260810150001": "---\ntype: feedback\n---\nNothing to link.\n",
	}
	dir := writeMemgraphVault(t, index, facts)

	g, err := buildMemgraphFetch(dir)()
	if err != nil {
		t.Fatalf("buildMemgraphFetch: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("Nodes = %d, want 2: %+v", len(g.Nodes), g.Nodes)
	}
	if len(g.Edges) != 1 {
		t.Fatalf("Edges = %d, want 1 (dangling and self links must be dropped): %+v", len(g.Edges), g.Edges)
	}
	e := g.Edges[0]
	if !(e.From == "20260810150000" && e.To == "20260810150001") {
		t.Fatalf("Edges[0] = %+v, want 20260810150000 -> 20260810150001", e)
	}
	for _, n := range g.Nodes {
		if n.ID == "20260810150000" && n.Type != "project" {
			t.Fatalf("20260810150000 Type = %q, want \"project\"", n.Type)
		}
		if n.ID == "20260810150001" && n.Type != "feedback" {
			t.Fatalf("20260810150001 Type = %q, want \"feedback\"", n.Type)
		}
	}
}

// TestBuildMemgraphFetchUnsetVaultIsAVisibleError matches
// internal/knowledge.LoadIndex's own contract: an unset vault is a real,
// visible error, never an empty graph.
func TestBuildMemgraphFetchUnsetVaultIsAVisibleError(t *testing.T) {
	if _, err := buildMemgraphFetch("")(); err == nil {
		t.Fatal("buildMemgraphFetch(\"\")() returned no error, want $AGENT_MEMORY_VAULT-not-set")
	}
}

// TestBuildMemgraphFetchSkipsAStaleIndexEntry: an id in index.md with no
// backing fact file must not fail the whole graph.
func TestBuildMemgraphFetchSkipsAStaleIndexEntry(t *testing.T) {
	index := "# Facts\n" +
		"- [[20260810150000]] — first\n" +
		"- [[20260810159998]] — stale, no file\n"
	facts := map[string]string{
		"20260810150000": "---\ntype: project\n---\nbody\n",
	}
	dir := writeMemgraphVault(t, index, facts)

	g, err := buildMemgraphFetch(dir)()
	if err != nil {
		t.Fatalf("buildMemgraphFetch: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("Nodes = %d, want 2 (the stale id still gets a node from the index, just no Type): %+v", len(g.Nodes), g.Nodes)
	}
}

// TestBuildMemgraphFetchReadsTheCurrentVaultLayout is agent-estate#1304's
// own acceptance case for the memgraph call site named in the brief, and
// fails against unmodified main: buildMemgraphFetch calls
// knowledge.LoadIndex, which used to read the deleted agent/index.md and
// returned the error table's own row 1 ("memgraph view shows a read
// error") for every real vault.
func TestBuildMemgraphFetchReadsTheCurrentVaultLayout(t *testing.T) {
	index := "# Facts\n- [[20260810150000]] — first\n"
	facts := map[string]string{"20260810150000": "---\ntype: project\n---\nbody\n"}
	dir := writeMemgraphVault(t, index, facts)
	if _, err := os.Stat(filepath.Join(dir, "agent", "index.md")); err == nil {
		t.Fatal("fixture setup bug: agent/index.md exists, so this would not exercise the current layout")
	}

	g, err := buildMemgraphFetch(dir)()
	if err != nil {
		t.Fatalf("buildMemgraphFetch against the current vault layout errored: %v", err)
	}
	if len(g.Nodes) != 1 {
		t.Fatalf("Nodes = %d, want 1", len(g.Nodes))
	}
}
