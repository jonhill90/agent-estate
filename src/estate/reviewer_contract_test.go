package main

import (
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/gate"
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
	// label). The contract must name the bare id as what's requested.
	// It must NOT claim the branch-prefixed form is refused -- internal/gate's
	// normaliseLaneID has stripped exactly one leading dispatch/ prefix
	// before comparing since #945, so that form is accepted, just not the
	// requested style. Stating it as forbidden was #957's own bug; see
	// TestReviewerContractAgreesWithGateAcceptance below, which fails if
	// this claim and the gate's real behaviour ever drift apart again.
	// It must go on to say that form is accepted, not refused -- checked
	// as one sentence, not a whole-string scan for "will refuse", since
	// the contract's second requirement legitimately uses "will refuse"
	// about an unrelated failure (an unparsable Verdict: line).
	if !strings.Contains(got, "not `dispatch/943-1788418133` or any other label. (The gate strips one leading `dispatch/` before comparing, so the branch-prefixed form would still pass") {
		t.Errorf("reviewer grounding must state the branch-prefixed Review-Lane: form is accepted (not requested, not refused) by the gate:\n%s", got)
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

// WHY THIS TEST EXISTS. agent-estate#957 shipped a reviewer contract
// stating that a `dispatch/`-prefixed Review-Lane: trailer is refused,
// when internal/gate's normaliseLaneID has stripped exactly one leading
// `dispatch/` before comparing since #945 -- the gate accepts that form.
// A contract confidently stating the wrong format is worse than none: a
// reviewer who emits the prefixed form was being told it was wrong when
// it wasn't. This test ties reviewerContract's claim to the real gate
// behaviour it describes, via internal/gate's exported
// DispatchBranchPrefix and AcceptsReviewLane, so it fails if the two
// drift apart again -- either because reviewerContract starts claiming
// refusal again, or because internal/gate is tightened to actually refuse
// the prefixed form while the contract still says it "would still pass".
func TestReviewerContractAgreesWithGateAcceptance(t *testing.T) {
	const id = "943-1788418133"
	prefixed := gate.DispatchBranchPrefix + id

	if !gate.AcceptsReviewLane(prefixed, id) {
		t.Fatalf("internal/gate no longer accepts a %q-prefixed Review-Lane: trailer for lane %q -- "+
			"reviewerContract's claim that this form \"would still pass\" is now false and must be corrected", gate.DispatchBranchPrefix, id)
	}

	got := reviewerContract(id, 945)
	if !strings.Contains(got, prefixed) {
		t.Fatalf("reviewer contract does not mention the branch-prefixed trailer form %q at all:\n%s", prefixed, got)
	}
	// Checked as one sentence, not a whole-string scan for "will refuse":
	// the contract's second requirement legitimately uses "will refuse"
	// about an unrelated failure (an unparsable Verdict: line), and a
	// substring match against the whole block would false-positive on it.
	if !strings.Contains(got, "the branch-prefixed form would still pass") {
		t.Errorf("reviewer contract no longer states that the branch-prefixed form is accepted by the gate, contradicting AcceptsReviewLane:\n%s", got)
	}
}
