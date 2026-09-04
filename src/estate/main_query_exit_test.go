package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// TestKnowledgeQueryExitCodesAreDistinct is the CLI-dispatch-level guard
// #1037's review asked for: it builds the real `estate` binary and drives
// `estate knowledge query` through actual subprocess exits, one per
// knowledge.QueryState, so a regression that collapses two states back onto
// the same code fails here rather than only in a unit test of the mapping
// function. See knowledgeQueryExitCode's own doc comment in main.go for why
// each number was picked.
func TestKnowledgeQueryExitCodesAreDistinct(t *testing.T) {
	bin := buildEstateBinary(t)

	publicItem := knowledge.Item{
		ID:           "it-0000000000000001",
		Source:       "vault-fact",
		Permalink:    "/tmp/public.md",
		Tier1:        "authentication tokens rotate every ninety days",
		Publishable:  true,
		PublishBasis: "vault fact, always public",
	}
	privateItem := knowledge.Item{
		ID:           "it-0000000000000002",
		Source:       "corpus-parameter",
		Permalink:    "/tmp/private.md",
		Tier1:        "credentials keychain failure escalation procedure",
		Publishable:  false,
		PublishBasis: "corpus parameter, private by default",
	}

	cases := []struct {
		name     string
		items    []knowledge.Item
		question string
		wantCode int
		wantErr  bool // index_missing/unreadable exit nonzero via os.Exit(2)
	}{
		{
			name:     "matched",
			items:    []knowledge.Item{publicItem},
			question: "authentication tokens rotate ninety days",
			wantCode: 0,
		},
		{
			name:     "no_match",
			items:    []knowledge.Item{publicItem},
			question: "zzzznonexistentqqqq wibbleflorp unmatched",
			wantCode: 1,
		},
		{
			name:     "withheld_private",
			items:    []knowledge.Item{privateItem},
			question: "credentials keychain failure escalation",
			wantCode: 3,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			idx := filepath.Join(dir, "index.json")
			res := knowledge.Result{
				GeneratedAt: time.Now().UTC(),
				Items:       c.items,
			}
			if err := knowledge.Write(idx, res); err != nil {
				t.Fatalf("write fixture index: %v", err)
			}
			code := runEstateKnowledgeQuery(t, bin, dir, idx, c.question)
			if code != c.wantCode {
				t.Fatalf("%s: exit code = %d, want %d", c.name, code, c.wantCode)
			}
		})
	}

	// index_missing: point ESTATE_KNOWLEDGE_INDEX at a path that does not
	// exist, and index_unreadable: point it at a file that is not valid
	// JSON. Both must exit 2, and -- the point of this test -- neither of
	// those two may collide with 1 (no_match) or 3 (withheld_private).
	t.Run("index_missing", func(t *testing.T) {
		dir := t.TempDir()
		missing := filepath.Join(dir, "does-not-exist.json")
		code := runEstateKnowledgeQuery(t, bin, dir, missing, "anything")
		if code != 2 {
			t.Fatalf("index_missing: exit code = %d, want 2", code)
		}
	})
	t.Run("index_unreadable", func(t *testing.T) {
		dir := t.TempDir()
		bad := filepath.Join(dir, "corrupt.json")
		if err := os.WriteFile(bad, []byte("not json"), 0o644); err != nil {
			t.Fatalf("write corrupt fixture: %v", err)
		}
		code := runEstateKnowledgeQuery(t, bin, dir, bad, "anything")
		if code != 2 {
			t.Fatalf("index_unreadable: exit code = %d, want 2", code)
		}
	})
}

// TestKnowledgeQueryMatchedWithheldMajorityExitsZero is the direct
// regression test for agent-estate#1052: a query that returns real public
// matches but withholds MORE private matches than it returned must still
// exit 0 -- see StateMatchedWithheldMajority's own doc comment in query.go
// for why the exit code deliberately does not change -- while printing an
// unmistakable banner that a caller reading stdout (not just $?) cannot
// miss. Before this fix, this exact shape (1 public item, 2 private items,
// all matching) exited 0 with no distinguishable signal from an ordinary,
// fully-answered query -- printKnowledgeQuery's banner and Query's new
// state are what this test would fail without.
func TestKnowledgeQueryMatchedWithheldMajorityExitsZero(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	items := []knowledge.Item{
		{
			ID: "it-0000000000000010", Source: "vault-fact",
			Permalink: "/tmp/public.md", Tier1: "credential rotation keychain policy",
			Publishable: true, PublishBasis: "vault fact, always public",
		},
		{
			ID: "it-0000000000000011", Source: "corpus-parameter",
			Permalink: "/tmp/private1.md", Tier1: "credential rotation keychain escalation",
			Publishable: false, PublishBasis: "corpus parameter, private by default",
		},
		{
			ID: "it-0000000000000012", Source: "corpus-parameter",
			Permalink: "/tmp/private2.md", Tier1: "credential rotation keychain review",
			Publishable: false, PublishBasis: "corpus parameter, private by default",
		},
	}
	res := knowledge.Result{GeneratedAt: time.Now().UTC(), Items: items}
	if err := knowledge.Write(idx, res); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}

	cmd := exec.Command(bin, "knowledge", "query", "credential rotation keychain")
	cmd.Env = append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
	)
	out, runErr := cmd.CombinedOutput()
	code := 0
	if runErr != nil {
		exitErr, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("run estate knowledge query: %v\n%s", runErr, out)
		}
		code = exitErr.ExitCode()
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (a genuinely-matched, majority-withheld result must not exit non-zero)\n%s", code, out)
	}
	if !strings.Contains(string(out), "MOSTLY WITHHELD") {
		t.Fatalf("stdout carries no majority-withheld banner for a 1-public/2-private match:\n%s", out)
	}
}

// buildEstateBinary (shared with tick_observed_spend_test.go) compiles the
// estate CLI once per call into a temp dir, so the exit-code assertions
// below exercise the same dispatch path a real caller invokes, not
// knowledge.Query or knowledgeQueryExitCode called directly from Go.

// runEstateKnowledgeQuery runs `estate knowledge query <question>` against
// idx as the compiled index, with a scratch ledger so the ledger.Open call
// at the top of main never touches the operator's real ledger, and returns
// the process's exit code.
func runEstateKnowledgeQuery(t *testing.T, bin, scratchDir, idx, question string) int {
	t.Helper()
	cmd := exec.Command(bin, "knowledge", "query", question)
	cmd.Env = append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(scratchDir, "ledger.jsonl"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run estate knowledge query: %v\n%s", err, out)
	}
	return exitErr.ExitCode()
}
