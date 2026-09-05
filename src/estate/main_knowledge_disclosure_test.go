package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// TestKnowledgeGetDisclosureResolutionReachesCaller is the direct
// regression test for agent-estate#1202: main.go's `estate knowledge get`
// path calls knowledge.ResolveDisclosure, but the only prior end-to-end
// `get` test used a fixture item with an empty PromptID, which
// structurally skips disclosure resolution -- so that test stayed green
// no matter what ResolveDisclosure's wiring into main.go did. This test
// uses a fixture item carrying a real (synthetic) PromptID against a real
// (synthetic) corpus DB, so the actual call in main.go actually runs, and
// asserts the resolved knowledge.Disclosure (state AND detail -- see the
// mutation note below) reaches the CLI's own --json output.
//
// Two outcomes, selected by the fixture alone (never by this harness):
// available_clean when the corpus's prompts row for this item's PromptID
// carries a non-empty text_clean, unavailable when that row exists but
// text_clean is empty. Both are exercised below so this test cannot be
// the one-sided harness agent-estate#1199/#1201 turned out to be.
//
// DisclosureRestricted ("withheld_private") is deliberately NOT exercised
// here: it is structurally unreachable through this CLI path. main.go
// passes the same --private flag to both knowledge.Get and
// knowledge.ResolveDisclosure -- when includePrivate is false, Get itself
// refuses a private item before ResolveDisclosure is ever called; when
// includePrivate is true, ResolveDisclosure's own scope check is bypassed
// by that same flag. disclosure.go's own doc comment on DisclosureRestricted
// says as much ("this state is what that refusal would have been ... made
// available to a caller that resolves disclosure directly ... rather than
// through Get's blanket item-level gate"). This was verified against the
// built binary while preparing this test (a private item with a real
// text_clean row returns `ok:false` without --private, and `available_clean`
// -- not `restricted` -- with --private), not inherited from that comment.
//
// Only the `available_clean` / `unavailable` distinction is asserted on
// Detail's exact wording as well as State: a mutation that keeps the right
// State but rewords Detail must still fail this test (see the mutation
// evidence in the PR description -- flipping the branch AND rewording the
// message while keeping the branch were both tried against this test).
func TestKnowledgeGetDisclosureResolutionReachesCaller(t *testing.T) {
	bin := buildEstateBinary(t)

	const (
		promptWithCleanText = "hp-aaaaaaaaaaaaaaaa"
		promptNeverCleaned  = "hp-bbbbbbbbbbbbbbbb"
		promptNeverInserted = "hp-cccccccccccccccc" // never inserted -- source_missing
	)

	corpus := buildKnowledgeDisclosureFixtureCorpus(t,
		`CREATE TABLE prompts (id TEXT PRIMARY KEY, text_clean TEXT);
INSERT INTO prompts (id, text_clean) VALUES
  ('`+promptWithCleanText+`', 'synthetic cleaned sentence -- not a real operator prompt'),
  ('`+promptNeverCleaned+`', '');`)

	dir := t.TempDir()
	idx := filepath.Join(dir, "index.json")
	res := knowledge.Result{
		GeneratedAt: time.Now().UTC(),
		Items: []knowledge.Item{
			{
				ID: "it-0000000000000101", Source: "corpus-parameter",
				Permalink: "synthetic://fixture/available-clean",
				Tier1:     "synthetic fixture item -- available_clean case",
				Publishable: true, PublishBasis: "synthetic test fixture, always public",
				PromptID: promptWithCleanText,
			},
			{
				ID: "it-0000000000000102", Source: "corpus-parameter",
				Permalink: "synthetic://fixture/unavailable",
				Tier1:     "synthetic fixture item -- unavailable case",
				Publishable: true, PublishBasis: "synthetic test fixture, always public",
				PromptID: promptNeverCleaned,
			},
			{
				ID: "it-0000000000000103", Source: "corpus-parameter",
				Permalink: "synthetic://fixture/source-missing",
				Tier1:     "synthetic fixture item -- source_missing case",
				Publishable: true, PublishBasis: "synthetic test fixture, always public",
				PromptID: promptNeverInserted,
			},
		},
	}
	if err := knowledge.Write(idx, res); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}

	env := append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
		"ESTATE_CORPUS="+corpus,
	)

	cases := []struct {
		name       string
		id         string
		wantState  knowledge.DisclosureState
		wantDetail string
	}{
		{
			name:       "disclosable",
			id:         "it-0000000000000101",
			wantState:  knowledge.DisclosureAvailableClean,
			wantDetail: "text_clean exists for this item's prompt -- not printed by this call",
		},
		{
			name:       "withheld_no_clean_text",
			id:         "it-0000000000000102",
			wantState:  knowledge.DisclosureUnavailable,
			wantDetail: "the prompt behind this item has no text_clean recorded -- the source exists but has never been cleaned for quoting",
		},
		{
			name:       "source_missing",
			id:         "it-0000000000000103",
			wantState:  knowledge.DisclosureSourceMissing,
			wantDetail: "prompt_id " + promptNeverInserted + " does not resolve to any row in the corpus's prompts table",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, "knowledge", "get", "--json", tc.id)
			cmd.Dir = dir
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run estate knowledge get --json %s: %v\n%s", tc.id, err, out)
			}

			var got struct {
				OK              bool                  `json:"ok"`
				Disclosure      *knowledge.Disclosure `json:"disclosure"`
				DisclosureError string                `json:"disclosure_error"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("decode --json output: %v\n%s", err, out)
			}
			if !got.OK {
				t.Fatalf("ok = false, want true\n%s", out)
			}
			if got.DisclosureError != "" {
				t.Fatalf("disclosure_error = %q, want none\n%s", got.DisclosureError, out)
			}
			if got.Disclosure == nil {
				t.Fatalf("disclosure field absent from --json output -- ResolveDisclosure's result did not reach the caller\n%s", out)
			}
			if got.Disclosure.State != tc.wantState {
				t.Fatalf("disclosure.state = %q, want %q\n%s", got.Disclosure.State, tc.wantState, out)
			}
			// Bound to the exact message, not just the branch taken: a
			// mutation that keeps the right State but rewords Detail (the
			// realistic regression named in agent-estate#1202, as opposed
			// to a deleted branch) must still fail this assertion.
			if got.Disclosure.Detail != tc.wantDetail {
				t.Fatalf("disclosure.detail = %q, want %q\n%s", got.Disclosure.Detail, tc.wantDetail, out)
			}
		})
	}
}

// buildKnowledgeDisclosureFixtureCorpus creates a throwaway sqlite3 DB
// under t.TempDir() with the given DDL/DML and returns its path -- the
// same "own temp file, sqlite3 CLI, no shared state" shape
// internal/knowledge's own buildFixtureCorpus (corpus_test.go) uses,
// duplicated here rather than exported across the package boundary
// because this is the only main-package test that needs it.
func buildKnowledgeDisclosureFixtureCorpus(t *testing.T, ddl string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "corpus.sqlite3")
	cmd := exec.Command("sqlite3", path)
	cmd.Stdin = strings.NewReader(ddl)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 fixture setup failed: %v\n%s", err, out)
	}
	return path
}
