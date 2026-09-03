package main

import (
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

// WHY THIS TEST EXISTS. agent-estate#949: three distinct forms of a
// Review-Lane: trailer, and a ledger Result with no parsable Verdict: line
// at all, all appeared in one night because the reviewer contract lived
// only in prose the Director happened to type into a brief. The fix moves
// the contract into roleGrounding, appended by dispatch itself with the
// lane's own id and PR number already filled in. These tests exercise that
// function directly -- the same one main's dispatch path calls -- rather
// than re-deriving the contract text by hand.

func TestRoleGrounding_ReviewerGetsContractWithInterpolatedValues(t *testing.T) {
	got := roleGrounding(ledger.RoleReviewer, "943-1788418133", 945, "dispatch/943-1788418133")

	if !strings.Contains(got, "## Reviewer contract") {
		t.Fatalf("reviewer grounding missing the contract heading:\n%s", got)
	}
	if !strings.Contains(got, "PR #945") {
		t.Errorf("reviewer grounding does not interpolate the PR number:\n%s", got)
	}
	if !strings.Contains(got, "Review-Lane: 943-1788418133") {
		t.Errorf("reviewer grounding does not state the bare dispatch id as the required Review-Lane: value:\n%s", got)
	}
	if !strings.Contains(got, "lane `943-1788418133`") {
		t.Errorf("reviewer grounding does not name the lane's own id:\n%s", got)
	}
	// #949's first failure: a ledger Result with a prose approval and no
	// parsable Verdict: line. The contract must require one in the
	// lane's own final result text, not only the PR comment.
	if !strings.Contains(got, "Verdict: APPROVE") {
		t.Errorf("reviewer grounding does not state the required Verdict: line:\n%s", got)
	}
	if !strings.Contains(got, "own final result text") {
		t.Errorf("reviewer grounding does not require the Verdict: line in the lane's own result, not just the PR comment:\n%s", got)
	}
	// #949's second failure: Review-Lane: dispatch/938-1788417348 (branch-
	// prefixed) and Review-Lane: 941-review-pr941reviewer (an invented
	// label). The contract must forbid both explicitly.
	if !strings.Contains(got, "not `dispatch/943-1788418133`") {
		t.Errorf("reviewer grounding does not forbid the branch-prefixed form of the trailer:\n%s", got)
	}

	// It must not also carry the author's branch-discipline block --
	// review turns never open a PR, so that block has nothing to say to
	// them and would only be one more thing to ignore or misapply.
	if strings.Contains(got, "Branch discipline") {
		t.Errorf("reviewer grounding must not also carry the author's branch-discipline block:\n%s", got)
	}
}

func TestRoleGrounding_AuthorDoesNotGetReviewerContract(t *testing.T) {
	got := roleGrounding(ledger.RoleAuthor, "949-1788431203", 0, "dispatch/949-1788431203")

	if strings.Contains(got, "Reviewer contract") {
		t.Errorf("author grounding must not carry the reviewer contract:\n%s", got)
	}
	if strings.Contains(got, "agent-estate#949") {
		t.Errorf("author grounding must not reference the reviewer-contract issue:\n%s", got)
	}
	// The author's own block must be present and unchanged in shape --
	// #949 must not alter what the author path appends.
	if !strings.Contains(got, "## Branch discipline (agent-estate#940") {
		t.Errorf("author grounding lost its branch-discipline block:\n%s", got)
	}
	if !strings.Contains(got, "dispatch/949-1788431203") {
		t.Errorf("author grounding does not name its own worktree branch:\n%s", got)
	}
}
