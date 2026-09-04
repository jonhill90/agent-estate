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
	if !strings.Contains(got, "docs/knowledge-system.md") {
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
