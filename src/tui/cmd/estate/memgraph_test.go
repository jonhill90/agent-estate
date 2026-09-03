package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMemgraphVault(t *testing.T, index string, facts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent", "facts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	for slug, body := range facts {
		path := filepath.Join(dir, "agent", "facts", slug+".md")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestBuildMemgraphFetchBuildsEdgesFromWikilinks: nodes come from the
// index, edges from each fact body's own [[wikilink]] -- a link to a slug
// NOT in the index (dangling) or to itself must not become an edge.
func TestBuildMemgraphFetchBuildsEdgesFromWikilinks(t *testing.T) {
	index := "# Facts\n" +
		"- [[fact-a]] — first\n" +
		"- [[fact-b]] — second\n"
	facts := map[string]string{
		"fact-a": "---\ntype: project\n---\nSee [[fact-b]] and [[fact-nonexistent]] and [[fact-a]] itself.\n",
		"fact-b": "---\ntype: feedback\n---\nNothing to link.\n",
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
	if !(e.From == "fact-a" && e.To == "fact-b") {
		t.Fatalf("Edges[0] = %+v, want fact-a -> fact-b", e)
	}
	for _, n := range g.Nodes {
		if n.ID == "fact-a" && n.Type != "project" {
			t.Fatalf("fact-a Type = %q, want \"project\"", n.Type)
		}
		if n.ID == "fact-b" && n.Type != "feedback" {
			t.Fatalf("fact-b Type = %q, want \"feedback\"", n.Type)
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

// TestBuildMemgraphFetchSkipsAStaleIndexEntry: a slug in agent/index.md
// with no backing fact file must not fail the whole graph.
func TestBuildMemgraphFetchSkipsAStaleIndexEntry(t *testing.T) {
	index := "# Facts\n" +
		"- [[fact-a]] — first\n" +
		"- [[fact-gone]] — stale, no file\n"
	facts := map[string]string{
		"fact-a": "---\ntype: project\n---\nbody\n",
	}
	dir := writeMemgraphVault(t, index, facts)

	g, err := buildMemgraphFetch(dir)()
	if err != nil {
		t.Fatalf("buildMemgraphFetch: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("Nodes = %d, want 2 (the stale slug still gets a node from the index, just no Type): %+v", len(g.Nodes), g.Nodes)
	}
}
