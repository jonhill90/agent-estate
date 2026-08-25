// Mutation-check for Resolve's comment-verdict gate -- the four
// directions b5-verdict-tui.md itself names, ported from skills#255's
// test_pr_verdict.py:
//
//  1. same-lane verdict                          -> must FAIL (unknown)
//  2. genuine cross-lane verdict at current head  -> must PASS (approved)
//  3. cross-lane verdict against a stale SHA      -> must FAIL (unknown)
//  4. no verdict at all                           -> must FAIL (none)
//
// Every case constructs a Payload directly -- no gh subprocess, no
// network, matching this repo's other seam tests (internal/board's own
// fixture-runner tests).
package prverdict

import (
	"strings"
	"testing"
)

const (
	head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	old  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func payload(body string, comments []Comment, headSHA string) Payload {
	if headSHA == "" {
		headSHA = head
	}
	return Payload{Body: body, HeadSHA: headSHA, Comments: comments}
}

func comment(body string) Comment {
	return Comment{Author: "jonhill90", Body: body}
}

func TestMutationCheckFourDirections(t *testing.T) {
	t.Run("1_same_lane_verdict_fails", func(t *testing.T) {
		p := payload("Author-Lane: build-5\n", []Comment{
			comment("Verdict: APPROVE\nReview-Lane: build-5\nReviewed-SHA: " + head + "\n"),
		}, "")
		got := Resolve(p)
		if got.Decision != Unknown {
			t.Fatalf("decision = %q, want unknown", got.Decision)
		}
		if !contains(got.Detail, "self-review") {
			t.Fatalf("detail = %q, want it to mention self-review", got.Detail)
		}
	})

	t.Run("2_genuine_cross_lane_verdict_at_current_head_passes", func(t *testing.T) {
		p := payload("Author-Lane: build-2\n", []Comment{
			comment("Verdict: APPROVE\nReview-Lane: build-5\nReviewed-SHA: " + head + "\n"),
		}, "")
		got := Resolve(p)
		if got.Decision != Approved {
			t.Fatalf("decision = %q, want approved (detail: %s)", got.Decision, got.Detail)
		}
	})

	t.Run("3_cross_lane_verdict_against_stale_sha_fails", func(t *testing.T) {
		p := payload("Author-Lane: build-2\n", []Comment{
			comment("Verdict: APPROVE\nReview-Lane: build-5\nReviewed-SHA: " + old + "\n"),
		}, head)
		got := Resolve(p)
		if got.Decision != Unknown {
			t.Fatalf("decision = %q, want unknown", got.Decision)
		}
		if !contains(got.Detail, "stale") {
			t.Fatalf("detail = %q, want it to mention stale", got.Detail)
		}
	})

	t.Run("4_no_verdict_at_all_fails", func(t *testing.T) {
		p := payload("Author-Lane: build-2\n", nil, "")
		got := Resolve(p)
		if got.Decision != None {
			t.Fatalf("decision = %q, want none", got.Decision)
		}
	})
}

func TestExitCodesMatchDecision(t *testing.T) {
	if Approved.ExitCode() != 0 {
		t.Fatalf("Approved.ExitCode() = %d, want 0", Approved.ExitCode())
	}
	if Rejected.ExitCode() == 0 {
		t.Fatalf("Rejected.ExitCode() must not be 0")
	}
	if None.ExitCode() == 0 {
		t.Fatalf("None.ExitCode() must not be 0")
	}
	if Unknown.ExitCode() == 0 {
		t.Fatalf("Unknown.ExitCode() must not be 0")
	}
	codes := map[int]bool{
		Approved.ExitCode(): true,
		Rejected.ExitCode(): true,
		None.ExitCode():     true,
		Unknown.ExitCode():  true,
	}
	if len(codes) != 4 {
		t.Fatalf("exit codes are not all distinct: %v", codes)
	}
}

func TestIndependenceEdgeCases(t *testing.T) {
	t.Run("missing_author_lane_refuses_even_with_a_clean_review", func(t *testing.T) {
		p := payload("no trailer here\n", []Comment{
			comment("Verdict: APPROVE\nReview-Lane: build-5\nReviewed-SHA: " + head + "\n"),
		}, "")
		got := Resolve(p)
		if got.Decision != Unknown || !contains(got.Detail, "Author-Lane") {
			t.Fatalf("got %+v, want unknown mentioning Author-Lane", got)
		}
	})

	t.Run("missing_review_lane_refuses", func(t *testing.T) {
		p := payload("Author-Lane: build-2\n", []Comment{
			comment("Verdict: APPROVE\nReviewed-SHA: " + head + "\n"),
		}, "")
		got := Resolve(p)
		if got.Decision != Unknown || !contains(got.Detail, "Review-Lane") {
			t.Fatalf("got %+v, want unknown mentioning Review-Lane", got)
		}
	})

	t.Run("missing_reviewed_sha_refuses", func(t *testing.T) {
		p := payload("Author-Lane: build-2\n", []Comment{
			comment("Verdict: APPROVE\nReview-Lane: build-5\n"),
		}, "")
		got := Resolve(p)
		if got.Decision != Unknown || !contains(got.Detail, "Reviewed-SHA") {
			t.Fatalf("got %+v, want unknown mentioning Reviewed-SHA", got)
		}
	})

	t.Run("rejected_cross_lane_at_current_head_is_rejected_not_approved", func(t *testing.T) {
		p := payload("Author-Lane: build-2\n", []Comment{
			comment("Verdict: REQUEST CHANGES\nReview-Lane: build-5\nReviewed-SHA: " + head + "\n"),
		}, "")
		got := Resolve(p)
		if got.Decision != Rejected {
			t.Fatalf("decision = %q, want rejected", got.Decision)
		}
	})

	t.Run("later_comment_supersedes_an_earlier_self_approval_attempt", func(t *testing.T) {
		// agent-supervisor#196's own rule, ported: the LATER decision
		// wins, exercised across two independent lanes -- a second lane
		// re-reviewing after the first one's verdict went stale, the
		// shape this estate actually produces.
		p := payload("Author-Lane: build-2\n", []Comment{
			comment("Verdict: REQUEST CHANGES\nReview-Lane: build-4\nReviewed-SHA: " + head + "\n"),
			comment("Verdict: APPROVE\nReview-Lane: build-5\nReviewed-SHA: " + head + "\n"),
		}, "")
		got := Resolve(p)
		if got.Decision != Approved {
			t.Fatalf("decision = %q, want approved (detail: %s)", got.Decision, got.Detail)
		}
	})

	t.Run("negated_decision_text_is_not_read_as_approval", func(t *testing.T) {
		// agent-supervisor#198's own regression, ported directly.
		p := payload("Author-Lane: build-2\n", []Comment{
			comment("Verdict: NOT APPROVED\nReview-Lane: build-5\nReviewed-SHA: " + head + "\n"),
		}, "")
		got := Resolve(p)
		if got.Decision != Unknown || !contains(got.Detail, "not recognised") {
			t.Fatalf("got %+v, want unknown mentioning \"not recognised\"", got)
		}
	})

	t.Run("verdict_quoted_inside_a_fenced_code_block_does_not_count", func(t *testing.T) {
		// agent-supervisor#192's own regression, ported directly -- an
		// example, not a real verdict.
		p := payload("Author-Lane: build-2\n", []Comment{
			comment("Here's the format:\n```\nVerdict: APPROVE\nReview-Lane: build-5\n```\n"),
		}, "")
		got := Resolve(p)
		if got.Decision != None {
			t.Fatalf("decision = %q, want none", got.Decision)
		}
	})

	t.Run("conflicting_verdict_lines_in_one_comment_refuse", func(t *testing.T) {
		// agent-supervisor#196's own regression, ported directly -- two
		// DIFFERING decisions in one comment must not pick either one.
		p := payload("Author-Lane: build-2\n", []Comment{
			comment("Verdict: APPROVE\nVerdict: REQUEST CHANGES\nReview-Lane: build-5\nReviewed-SHA: " + head + "\n"),
		}, "")
		got := Resolve(p)
		if got.Decision != Unknown || !contains(got.Detail, "conflicting") {
			t.Fatalf("got %+v, want unknown mentioning conflicting", got)
		}
	})
}

// TestBlankReviewLaneSelfApprovalBypass ports jonhill90/skills#260's
// BlankReviewLaneSelfApprovalBypass regression test class near-verbatim:
// a security regression found by build-3 in jonhill90/skills's
// pr_verdict.py (fixed there in agent-supervisor#260) and independently confirmed present
// here in this Go port under the identical bug shape. A BLANK
// `Review-Lane:` value let reviewLaneRE's post-colon `\s*` consume the
// line break and capture the NEXT line's text (`Reviewed-SHA: ...`)
// instead of matching empty. That non-empty garbage never equals a real
// Author-Lane: value, so the same-lane self-review check below it
// silently never fired -- a same-lane author posting a comment with a
// blank Review-Lane: trailer got treated as a valid, different reviewer
// and the PR resolved approved. This exact shape -- blank trailer, same
// lane as Author-Lane, real head SHA on the very next line -- must never
// again resolve to anything but unknown.
func TestBlankReviewLaneSelfApprovalBypass(t *testing.T) {
	t.Run("blank_review_lane_same_author_lane_is_not_approved", func(t *testing.T) {
		p := payload("Author-Lane: build-3\n", []Comment{
			comment("Verdict: APPROVE\nAuthor-Lane: build-3\nReview-Lane: \nReviewed-SHA: " + head + "\n"),
		}, "")
		got := Resolve(p)
		if got.Decision != Unknown {
			t.Fatalf("decision = %q, want unknown (detail: %s)", got.Decision, got.Detail)
		}
		if !contains(got.Detail, "Review-Lane") {
			t.Fatalf("detail = %q, want it to mention Review-Lane", got.Detail)
		}
	})

	t.Run("blank_review_lane_does_not_capture_the_next_line", func(t *testing.T) {
		// Direct regression on the regex itself, not just Resolve's
		// outcome -- pins the exact failure mode build-3 reported so a
		// future refactor of the pattern can't silently reopen it while
		// still passing the Resolve-level test above by coincidence.
		body := "Verdict: APPROVE\nAuthor-Lane: build-3\nReview-Lane: \nReviewed-SHA: abc123\n"
		_, ok := parseTrailer(reviewLaneRE, body)
		if ok {
			t.Fatalf("parseTrailer(reviewLaneRE, ...) matched, want no match for a blank trailer")
		}
	})
}

func TestMissingHeadSHAFailsClosed(t *testing.T) {
	got := Resolve(Payload{Body: "", Comments: nil})
	if got.Decision != Unknown {
		t.Fatalf("decision = %q, want unknown", got.Decision)
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
