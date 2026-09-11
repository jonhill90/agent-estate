package main

import (
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

// WHY THIS TEST EXISTS. agent-estate#1081: knowledgeGrounding told every
// dispatched turn about `query`, `get`, `--private` and `--json`, but said
// nothing about `source:<name>` scoping -- measured, a lane following the
// grounding exactly took the unscoped path and scored 4/12 on the
// checked-in natural-language stratum where the scoped path scores 8/12.
// This test fails against that grounding and passes once it names the flag
// -- it exercises knowledgeGrounding (and roleGrounding, which appends it
// for both roles) directly, the same functions main's dispatch path calls,
// rather than re-deriving the text by hand.
func TestKnowledgeGrounding_MentionsSourceScoping(t *testing.T) {
	got := knowledgeGrounding()

	if !strings.Contains(got, "source:") {
		t.Fatalf("knowledge grounding never mentions source: scoping -- a lane following it exactly has no way to learn the flag exists:\n%s", got)
	}
	if !strings.Contains(got, "agent-estate#1081") {
		t.Errorf("knowledge grounding does not cite agent-estate#1081:\n%s", got)
	}
	if !strings.Contains(got, "docs/canonical/knowledge-system.md") {
		t.Errorf("knowledge grounding does not point at the knowledge doc for the full scoping rules:\n%s", got)
	}
}

// Both role branches append knowledgeGrounding (main.go's roleGrounding) --
// confirm the source: mention actually reaches an author turn and a
// reviewer turn, not merely the helper in isolation.
func TestRoleGrounding_BothRolesCarrySourceScoping(t *testing.T) {
	author := roleGrounding(ledger.RoleAuthor, "1081-test", 0, "dispatch/1081-test", false)
	if !strings.Contains(author, "source:") {
		t.Errorf("author grounding does not carry source: scoping:\n%s", author)
	}

	reviewer := roleGrounding(ledger.RoleReviewer, "1081-test", 945, "dispatch/1081-test", false)
	if !strings.Contains(reviewer, "source:") {
		t.Errorf("reviewer grounding does not carry source: scoping:\n%s", reviewer)
	}

	fixPass := roleGrounding(ledger.RoleAuthor, "1081-test", 957, "dispatch/1081-test", true)
	if !strings.Contains(fixPass, "source:") {
		t.Errorf("fix-pass grounding does not carry source: scoping:\n%s", fixPass)
	}
}

// WHY THIS TEST EXISTS. agent-estate#1255 (the K3 gate): a real published
// vault fact scored zero for a topically-related question in default mode
// not because retrieval failed, but because vault facts are private by
// default and the grounding never said so -- a caller checking for a
// standing constraint had no way to learn from this text that an unscoped
// query can silently miss one. Mirrors TestKnowledgeGrounding_MentionsSourceScoping's
// own discipline (agent-estate#1081): fails against grounding text that
// omits the callout, passes once it is explicit.
func TestKnowledgeGrounding_MentionsVaultFactsArePrivateByDefault(t *testing.T) {
	got := knowledgeGrounding()

	if !strings.Contains(got, "vault-fact") {
		t.Fatalf("knowledge grounding never names vault-fact as a source -- a lane has no way to learn that class exists:\n%s", got)
	}
	if !strings.Contains(got, "PRIVATE BY DEFAULT") && !strings.Contains(got, "private by default") {
		t.Fatalf("knowledge grounding does not say vault facts are private by default -- a lane checking a standing constraint in default mode has no warning it can silently miss one:\n%s", got)
	}
	if !strings.Contains(got, "agent-estate#1255") {
		t.Errorf("knowledge grounding does not cite agent-estate#1255:\n%s", got)
	}
	if !strings.Contains(got, "--private") {
		t.Errorf("knowledge grounding does not tell a caller to use --private:\n%s", got)
	}
}

// WHY THIS TEST EXISTS. agent-estate#1099: the abstract instruction alone
// ("scope whenever you already know which source holds the answer")
// measurably did not produce scoped queries -- estate toolusage --recent 20/40
// showed 0 scoped private-mode queries across two independent re-measurements
// the same night this test was added, including cases (a lane asking how the
// vault is organised) that squarely matched the abstract trigger. This pins a
// CONCRETE worked example naming real source values for the two most common
// private-mode query shapes -- an operator standing-rule/decision question,
// and a repo-mechanics question -- so a lane does not have to independently
// generalise the abstract instruction to its own query. Mirrors this file's
// own established discipline: fails against grounding text lacking the
// example, passes once it is concrete and cites agent-estate#1099.
func TestKnowledgeGrounding_PrivateModeScopingHasAWorkedExample(t *testing.T) {
	got := knowledgeGrounding()

	if !strings.Contains(got, "agent-estate#1099") {
		t.Fatalf("knowledge grounding does not cite agent-estate#1099 for the worked example it adds:\n%s", got)
	}
	if !strings.Contains(got, "source:vault-fact") {
		t.Fatalf("knowledge grounding's worked example does not name source:vault-fact for a standing-rule question:\n%s", got)
	}
	if !strings.Contains(got, "source:corpus-directive") {
		t.Fatalf("knowledge grounding's worked example does not name source:corpus-directive for an operator-decision question:\n%s", got)
	}
}
