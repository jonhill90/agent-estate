package main

import (
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge/goldenset"
)

const sampleMatchedStdout = `2 match(es) for "auth tokens" (showing 2, 0 not returned)

[20260904055010] vault-fact (score 2: auth, tokens)
  auth token rotation -- rotate every 90 days
  /vault/agent/facts/auth-token-rotation.md

[20260904055011] corpus-parameter (score 1: auth)
  auth must use short-lived tokens
  corpus:item:it-abc123

ranking: score = count of distinct question terms ...
ask ` + "`estate knowledge get <id>`" + ` for one item's full tier2/tier3
`

const sampleNoMatchStdout = `no item matches "quokka platypus giraffe zeppelin"
`

func TestParseMatchesRecoversIDSourceAndPermalink(t *testing.T) {
	got := parseMatches(sampleMatchedStdout)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].ID != "20260904055010" || got[0].Source != "vault-fact" || got[0].Score != 2 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[0].Permalink != "/vault/agent/facts/auth-token-rotation.md" {
		t.Errorf("got[0].Permalink = %q", got[0].Permalink)
	}
	if got[1].Permalink != "corpus:item:it-abc123" {
		t.Errorf("got[1].Permalink = %q", got[1].Permalink)
	}
}

func TestEvaluatePassesWhenExpectedIdentifierInTop3(t *testing.T) {
	c := goldenset.Case{
		ID: "t1", ExpectedSource: goldenset.SourceVaultFact,
		ExpectedIdentifier: "auth-token-rotation.md",
	}
	r := evaluate(c, sampleMatchedStdout, 0)
	if !r.pass {
		t.Fatalf("evaluate() pass = false, detail=%q", r.detail)
	}
}

func TestEvaluateFailsWhenExpectedIdentifierMissing(t *testing.T) {
	c := goldenset.Case{
		ID: "t2", ExpectedSource: goldenset.SourceVaultFact,
		ExpectedIdentifier: "never-appears.md",
	}
	r := evaluate(c, sampleMatchedStdout, 0)
	if r.pass {
		t.Fatal("evaluate() pass = true for an identifier that never appeared")
	}
}

func TestEvaluateFailsWhenExitCodeIsNotZeroForARealCase(t *testing.T) {
	c := goldenset.Case{
		ID: "t3", ExpectedSource: goldenset.SourceVaultFact,
		ExpectedIdentifier: "auth-token-rotation.md",
	}
	r := evaluate(c, "", 2)
	if r.pass {
		t.Fatal("evaluate() pass = true for exit code 2 (index_missing/unreadable)")
	}
}

func TestEvaluatePassesNoMatchCaseOnGenuineNoMatch(t *testing.T) {
	c := goldenset.Case{ID: "n1", ExpectedSource: goldenset.SourceNone}
	r := evaluate(c, sampleNoMatchStdout, 1)
	if !r.pass {
		t.Fatalf("evaluate() pass = false for a genuine no_match, detail=%q", r.detail)
	}
}

func TestEvaluateFailsNoMatchCaseWhenSomethingActuallyMatched(t *testing.T) {
	c := goldenset.Case{ID: "n2", ExpectedSource: goldenset.SourceNone}
	r := evaluate(c, sampleMatchedStdout, 0)
	if r.pass {
		t.Fatal("evaluate() pass = true for a no_match case that actually matched something")
	}
}

// agent-estate#1073: firstMatchRank is the natural-language stratum's own
// primitive -- it must find a match at any rank in the returned list, not
// only within a top-3 slice, so both the top-3 and top-10 numbers can be
// derived from one query per case.

func TestFirstMatchRankFindsRankBeyondThree(t *testing.T) {
	c := goldenset.Case{ID: "t4", ExpectedSource: goldenset.SourceCorpusParameter, ExpectedIdentifier: "it-abc123"}
	matches := parseMatches(sampleMatchedStdout)
	if got := firstMatchRank(c, matches); got != 2 {
		t.Fatalf("firstMatchRank() = %d, want 2", got)
	}
}

func TestFirstMatchRankZeroWhenNeverReturned(t *testing.T) {
	c := goldenset.Case{ID: "t5", ExpectedSource: goldenset.SourceVaultFact, ExpectedIdentifier: "never-appears.md"}
	matches := parseMatches(sampleMatchedStdout)
	if got := firstMatchRank(c, matches); got != 0 {
		t.Fatalf("firstMatchRank() = %d, want 0", got)
	}
}

// agent-estate#1077: scopedQuestion is the natural-language stratum's
// scoped-run primitive -- these two cases fail against the parent commit
// (df2cf75/3449f91), where the function does not exist at all, and pass
// once it's added.

func TestScopedQuestionLeavesQuestionUnchangedWhenUnscoped(t *testing.T) {
	got := scopedQuestion("how do I merge a pull request in this repo", false)
	want := "how do I merge a pull request in this repo"
	if got != want {
		t.Fatalf("scopedQuestion(unscoped) = %q, want %q", got, want)
	}
}

func TestScopedQuestionPrependsSourceRepoDocsWhenScoped(t *testing.T) {
	got := scopedQuestion("how do I merge a pull request in this repo", true)
	want := "source:repo-docs how do I merge a pull request in this repo"
	if got != want {
		t.Fatalf("scopedQuestion(scoped) = %q, want %q", got, want)
	}
}
