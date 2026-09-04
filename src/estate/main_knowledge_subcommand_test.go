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

// TestKnowledgeUnrecognisedSubcommandRefusesAndDoesNotWrite is the direct
// regression test for agent-estate#1061 Finding 3: `estate knowledge
// <typo>` used to fall through, silently, into knowledge.Generate/Write --
// exit 0, no complaint, and a real write to the one index path every lane
// shares (#1048). This drives the real compiled binary through the actual
// CLI dispatch (not knowledgeQueryExitCode or any function called directly)
// so a regression that reintroduces the fallthrough fails here.
func TestKnowledgeUnrecognisedSubcommandRefusesAndDoesNotWrite(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")

	// Seed a fixture index and record its mtime -- if the unrecognised
	// subcommand regenerates it (the pre-fix behaviour), the mtime changes
	// and/or the content is replaced by a real knowledge.Generate() run.
	seed := knowledge.Result{
		GeneratedAt: time.Now().UTC(),
		Items: []knowledge.Item{
			{ID: "it-0000000000000099", Source: "vault-fact", Permalink: "/tmp/seed.md",
				Tier1: "seed marker item", Publishable: true, PublishBasis: "vault fact, always public"},
		},
	}
	if err := knowledge.Write(idx, seed); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}
	before, err := os.Stat(idx)
	if err != nil {
		t.Fatalf("stat fixture index before: %v", err)
	}
	beforeBytes, err := os.ReadFile(idx)
	if err != nil {
		t.Fatalf("read fixture index before: %v", err)
	}

	cmd := exec.Command(bin, "knowledge", "notasubcommand", "xyz")
	cmd.Env = append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
	)
	out, runErr := cmd.CombinedOutput()
	code := 0
	if runErr != nil {
		exitErr, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("run estate knowledge notasubcommand: %v\n%s", runErr, out)
		}
		code = exitErr.ExitCode()
	}
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero -- an unrecognised subcommand must be refused, not fall through to regeneration\n%s", out)
	}

	after, err := os.Stat(idx)
	if err != nil {
		t.Fatalf("stat fixture index after: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("index mtime changed (%s -> %s) -- an unrecognised subcommand wrote to the shared index", before.ModTime(), after.ModTime())
	}
	afterBytes, err := os.ReadFile(idx)
	if err != nil {
		t.Fatalf("read fixture index after: %v", err)
	}
	if string(afterBytes) != string(beforeBytes) {
		t.Fatalf("index content changed -- an unrecognised subcommand wrote to the shared index")
	}
}

// TestKnowledgeBareRegeneratesAndSubcommandsUnaffected is the "must not
// happen" side of #1061 Finding 3: the fix must not touch the documented
// bare-`knowledge` regeneration path, nor `query`/`get`'s own argument
// handling or exit codes.
func TestKnowledgeBareRegeneratesAndSubcommandsUnaffected(t *testing.T) {
	bin := buildEstateBinary(t)

	t.Run("bare_knowledge_regenerates", func(t *testing.T) {
		dir := t.TempDir()
		wd, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		cmd := exec.Command(bin, "knowledge")
		// Run from this module's own root so the real corpus/repo sources
		// knowledge.DefaultConfig reads are actually present, the same way
		// a real caller would invoke it.
		cmd.Dir = wd
		cmd.Env = append(os.Environ(),
			"ESTATE_KNOWLEDGE_INDEX="+filepath.Join(dir, "index.json"),
			"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
		)
		out, runErr := cmd.CombinedOutput()
		code := 0
		if runErr != nil {
			exitErr, ok := runErr.(*exec.ExitError)
			if !ok {
				t.Fatalf("run bare estate knowledge: %v\n%s", runErr, out)
			}
			code = exitErr.ExitCode()
		}
		// This subtest's whole point is telling "refused as an unrecognised
		// subcommand" apart from "ran the regeneration path and some
		// source(s) failed" -- exit code alone cannot, since both land on a
		// non-zero code (2 for refusal, 1 for a source failure -- see
		// `case "knowledge":` in main.go). CI's sandbox has neither `gh` on
		// PATH nor $AGENT_MEMORY_VAULT set, so github-stars and vault-facts
		// genuinely FAIL there, and this must still pass -- only a laptop
		// with every source reachable gets exit 0. So: assert the refusal
		// path was NOT taken (exit 2, and its "unrecognised knowledge
		// subcommand" message), assert the regeneration path's own
		// "written to" line did print, and assert the index landed on
		// disk -- without asserting every source succeeded.
		if code == 2 {
			t.Fatalf("bare `estate knowledge` exit code = 2 -- refused as an unrecognised subcommand instead of reaching regeneration\n%s", out)
		}
		if strings.Contains(string(out), "unrecognised knowledge subcommand") {
			t.Fatalf("bare `estate knowledge` was refused as an unrecognised subcommand -- it must reach the regeneration path\n%s", out)
		}
		if !strings.Contains(string(out), "item(s) written to") {
			t.Fatalf("bare `estate knowledge` did not reach the regeneration path's own write-confirmation line (documented regeneration behaviour must be unaffected)\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
			t.Fatalf("bare `estate knowledge` did not write an index: %v\n%s", err, out)
		}
	})

	t.Run("query_no_args_still_exits_2", func(t *testing.T) {
		dir := t.TempDir()
		cmd := exec.Command(bin, "knowledge", "query")
		cmd.Env = append(os.Environ(),
			"ESTATE_KNOWLEDGE_INDEX="+filepath.Join(dir, "index.json"),
			"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
		)
		out, runErr := cmd.CombinedOutput()
		code := 0
		if runErr != nil {
			exitErr, ok := runErr.(*exec.ExitError)
			if !ok {
				t.Fatalf("run estate knowledge query (no args): %v\n%s", runErr, out)
			}
			code = exitErr.ExitCode()
		}
		if code != 2 {
			t.Fatalf("`estate knowledge query` with no question: exit code = %d, want 2\n%s", code, out)
		}
	})

	t.Run("get_unknown_id_still_exits_1", func(t *testing.T) {
		dir := t.TempDir()
		idx := filepath.Join(dir, "index.json")
		res := knowledge.Result{
			GeneratedAt: time.Now().UTC(),
			Items: []knowledge.Item{
				{ID: "it-0000000000000042", Source: "vault-fact", Permalink: "/tmp/x.md",
					Tier1: "present item", Publishable: true, PublishBasis: "vault fact, always public"},
			},
		}
		if err := knowledge.Write(idx, res); err != nil {
			t.Fatalf("write fixture index: %v", err)
		}
		cmd := exec.Command(bin, "knowledge", "get", "it-nonexistent")
		cmd.Env = append(os.Environ(),
			"ESTATE_KNOWLEDGE_INDEX="+idx,
			"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
		)
		out, runErr := cmd.CombinedOutput()
		code := 0
		if runErr != nil {
			exitErr, ok := runErr.(*exec.ExitError)
			if !ok {
				t.Fatalf("run estate knowledge get (unknown id): %v\n%s", runErr, out)
			}
			code = exitErr.ExitCode()
		}
		if code != 1 {
			t.Fatalf("`estate knowledge get` with an unknown id: exit code = %d, want 1\n%s", code, out)
		}
	})
}
