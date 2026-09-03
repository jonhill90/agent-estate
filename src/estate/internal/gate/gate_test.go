package gate

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

// reviewRecord is a completed review turn by lane on issue -- the evidence
// the gate now requires before it will believe a reviewer exists at all.
func reviewRecord(issue, lane string) ledger.Record {
	return ledger.Record{
		ID: lane + "-rev", Issue: issue, Lane: lane,
		Role: ledger.RoleReview, State: ledger.Complete,
		Result: "VERDICT: APPROVE",
	}
}

func newLedger(t *testing.T, recs ...ledger.Record) *ledger.Ledger {
	t.Helper()
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if err := l.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func TestPendingCheckIsNotAPass(t *testing.T) {
	p := &PR{HeadOID: "abc12345", Checks: []Check{
		{Name: "unit", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Name: "shards", Status: "IN_PROGRESS"},
	}}
	if bad := checksGreen(p); len(bad) == 0 {
		t.Fatal("checksGreen accepted a PR with a pending check")
	}
}

func TestNoChecksIsRefusedNotAssumedFine(t *testing.T) {
	if bad := checksGreen(&PR{HeadOID: "abc12345"}); len(bad) == 0 {
		t.Fatal("checksGreen accepted a PR reporting zero checks; absence is not success")
	}
}

func TestAllGreenPasses(t *testing.T) {
	p := &PR{HeadOID: "abc12345", Checks: []Check{{Name: "unit", Status: "COMPLETED", Conclusion: "SUCCESS"}}}
	if bad := checksGreen(p); len(bad) != 0 {
		t.Fatalf("checksGreen refused an all-green PR: %v", bad)
	}
}

func TestUnknownAuthorRefuses(t *testing.T) {
	l := newLedger(t)
	if bad := independent(l, "42", "lane-reviewer"); len(bad) == 0 {
		t.Fatal("independent() allowed a merge with no authoring lane on record")
	}
}

func TestSelfReviewRefuses(t *testing.T) {
	l := newLedger(t, ledger.Record{ID: "x", Issue: "42", Lane: "lane-a", State: ledger.Complete})
	bad := independent(l, "42", "lane-a")
	if len(bad) == 0 {
		t.Fatal("independent() allowed a lane to review its own work")
	}
	// Assert WHICH refusal. The original test only checked that some refusal
	// came back, so it passed via the unknown-author branch while the
	// self-review branch was unreachable dead code.
	if !strings.Contains(bad[0], "self-review") {
		t.Fatalf("refused for the wrong reason: %q -- the self-review branch was not reached", bad[0])
	}
}

// The regression that the original suite could not see: two lanes on one
// issue, and one of them reviews its own work.
func TestSelfReviewRefusedWhenAnotherLaneAlsoWorkedTheIssue(t *testing.T) {
	l := newLedger(t,
		ledger.Record{ID: "x", Issue: "42", Lane: "lane-a", State: ledger.Complete},
		ledger.Record{ID: "y", Issue: "42", Lane: "lane-b", State: ledger.Complete},
	)
	bad := independent(l, "42", "lane-a")
	if len(bad) == 0 {
		t.Fatal("lane-a approved its own PR because lane-b also worked the issue -- fail open")
	}
	if !strings.Contains(bad[0], "self-review") {
		t.Fatalf("refused for the wrong reason: %q", bad[0])
	}
}

func TestMissingReviewerLaneRefuses(t *testing.T) {
	l := newLedger(t, ledger.Record{ID: "x", Issue: "42", Lane: "lane-a", State: ledger.Complete})
	if bad := independent(l, "42", "  "); len(bad) == 0 {
		t.Fatal("independent() allowed an unnamed reviewer")
	}
}

func TestDistinctLanesPass(t *testing.T) {
	l := newLedger(t,
		ledger.Record{ID: "x", Issue: "42", Lane: "lane-a", State: ledger.Complete},
		reviewRecord("42", "lane-b"))
	if bad := independent(l, "42", "lane-b"); len(bad) != 0 {
		t.Fatalf("independent() refused genuinely independent lanes: %v", bad)
	}
}

// An authorship record makes the gate able to answer "who wrote this?" for a
// PR that did not come from a dispatched turn. Without one, every such PR was
// unmergeable forever -- the gate refusing correctly on a fact nothing could
// supply.
func TestAuthorshipRecordResolvesTheAuthor(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ledger.Record{
		ID: "authored-907-director", Issue: "907", Lane: "director", State: ledger.Authored,
	}); err != nil {
		t.Fatal(err)
	}

	// CHANGED by a council finding. This previously asserted that a
	// hand-written authorship record was enough to permit a merge. That was
	// the hole: `estate authored` accepts any lane, so the author could name
	// a decoy and pass itself as reviewer. An assertion narrows who may
	// review; it cannot permit a merge.
	if err := l.Append(reviewRecord("907", "reviewer-lane")); err != nil {
		t.Fatal(err)
	}
	bad0 := independent(l, "907", "reviewer-lane")
	if bad0 == nil {
		t.Error("asserted authorship alone must not permit a merge")
	} else if !strings.Contains(strings.Join(bad0, " "), "asserted") {
		t.Errorf("the refusal must name the assertion; got: %v", bad0)
	}
	// The whole point of recording it: self-review must now be CATCHABLE.
	// Before, it was unreachable because there was never an author on record.
	bad := independent(l, "907", "director")
	if bad == nil {
		t.Fatal("the author reviewing its own work must be refused as self-review")
	}
	if !strings.Contains(strings.Join(bad, " "), "self-review") {
		t.Errorf("the refusal must name self-review; got: %v", bad)
	}
}

// An authorship record is not a running turn and must never occupy a dispatch
// slot -- otherwise recording who wrote a PR would consume host capacity.
func TestAuthorshipRecordDoesNotOccupyASlot(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ledger.Record{
		ID: "authored-907-director", Issue: "907", Lane: "director", State: ledger.Authored,
	}); err != nil {
		t.Fatal(err)
	}
	inflight, err := l.InFlight()
	if err != nil {
		t.Fatal(err)
	}
	if len(inflight) != 0 {
		t.Fatalf("an authorship record occupied a slot: %+v", inflight)
	}
}

// Still refuses when nothing is on record. Recording authorship must not have
// turned the unknown-author case into a pass.
func TestUnknownAuthorStillRefuses(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bad := independent(l, "999", "someone"); bad == nil {
		t.Fatal("an issue with no authorship on record must still be refused")
	}
}

// A lane dispatched to REVIEW an issue is not one of its authors. Without
// this the reviewer counts as having worked the issue and every review is
// refused as self-review -- which is exactly what happened the first time a
// review was dispatched against the issue it was reviewing.
func TestReviewerDispatchedAgainstTheIssueIsNotItsAuthor(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []ledger.Record{
		{ID: "authored-913-director", Issue: "913", Lane: "director", State: ledger.Authored},
		{ID: "913-review-1", Issue: "913", Lane: "913-review-1", Role: ledger.RoleReview, State: ledger.Complete, Result: "VERDICT: APPROVE"},
	} {
		if err := l.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	// A real dispatched author, so the scenario is one the gate can act on.
	if err := l.Append(ledger.Record{
		ID: "913-worker", Issue: "913", Lane: "913-worker", State: ledger.Complete,
	}); err != nil {
		t.Fatal(err)
	}
	if bad := independent(l, "913", "913-review-1"); bad != nil {
		t.Fatalf("a review-role lane is not an author; got refusal: %v", bad)
	}
}

// The exemption must be narrow. A lane that actually WORKED the issue is
// still an author, and must still be caught reviewing itself -- otherwise
// this is a hole rather than a fix.
func TestRoleExemptionDoesNotLetAWorkerReviewItself(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []ledger.Record{
		// Same lane: worked the issue, then came back wearing a review hat.
		{ID: "913-worker", Issue: "913", Lane: "lane-a", State: ledger.Complete},
		{ID: "913-review", Issue: "913", Lane: "lane-a", Role: ledger.RoleReview, State: ledger.Complete},
	} {
		if err := l.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	bad := independent(l, "913", "lane-a")
	if bad == nil {
		t.Fatal("a lane that authored work on the issue must still be refused, even if it later reviewed")
	}
	if !strings.Contains(strings.Join(bad, " "), "self-review") {
		t.Errorf("must name self-review; got: %v", bad)
	}
}

// Review records alone are not authorship: if the ONLY records are reviews,
// the author is still unknown and the gate must refuse.
func TestReviewsAloneDoNotEstablishAnAuthor(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ledger.Record{
		ID: "913-review", Issue: "913", Lane: "r1", Role: ledger.RoleReview, State: ledger.Complete,
	}); err != nil {
		t.Fatal(err)
	}
	if bad := independent(l, "913", "r2"); bad == nil {
		t.Fatal("reviews alone leave authorship unknown; the gate must refuse")
	}
}

// A council found that `estate authored` lets any caller write any lane as
// the author of any issue, and the gate then treated that as established
// fact. An author could name a decoy, pass its own lane as reviewer, and get
// "may merge" -- laundering a self-merge as an independent one.
//
// Authorship written by hand is an ASSERTION. Authorship derived from a
// dispatched turn is EVIDENCE. The gate may act on evidence; on an assertion
// alone it must refuse and leave the decision to a human.
func TestAssertedAuthorshipAloneNeverPermitsAMerge(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// Exactly the forgery: a decoy named as author by hand.
	if err := l.Append(ledger.Record{
		ID: "authored-916-decoy", Issue: "916", Lane: "decoy-lane", State: ledger.Authored,
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(reviewRecord("916", "director")); err != nil {
		t.Fatal(err)
	}
	bad := independent(l, "916", "director")
	if bad == nil {
		t.Fatal("a hand-written authorship record must not be enough to permit a merge")
	}
	joined := strings.Join(bad, " ")
	if !strings.Contains(joined, "asserted") {
		t.Errorf("the refusal must say the authorship was asserted, not derived; got: %v", bad)
	}
}

// Authorship derived from a real dispatched turn is evidence, and still works.
func TestDerivedAuthorshipFromADispatchStillPermitsAMerge(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ledger.Record{
		ID: "916-1788", Issue: "916", Lane: "916-1788", State: ledger.Complete,
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(reviewRecord("916", "some-other-lane")); err != nil {
		t.Fatal(err)
	}
	if bad := independent(l, "916", "some-other-lane"); bad != nil {
		t.Fatalf("a dispatched author and a real reviewer is independent; got: %v", bad)
	}
}

// An assertion still counts for CATCHING self-review -- it narrows who may
// review, it just cannot widen who may merge.
func TestAssertedAuthorshipStillCatchesSelfReview(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ledger.Record{
		ID: "authored-916-director", Issue: "916", Lane: "director", State: ledger.Authored,
	}); err != nil {
		t.Fatal(err)
	}
	bad := independent(l, "916", "director")
	if bad == nil {
		t.Fatal("the asserted author reviewing its own work must still be refused")
	}
}

// Round two of the council: the reviewer lane was an unverified string. The
// gate only checked it differed from the authors, so an author could invent
// any lane name and get "may merge". RoleReview was written but never
// required, which made the whole independence check theatre.
func TestReviewerMustHaveActuallyReviewed(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ledger.Record{
		ID: "916-real", Issue: "916", Lane: "916-real", State: ledger.Complete,
	}); err != nil {
		t.Fatal(err)
	}
	bad := independent(l, "916", "completely-made-up-lane")
	if bad == nil {
		t.Fatal("a lane with no review on record must not satisfy the independence check")
	}
	if !strings.Contains(strings.Join(bad, " "), "no completed review") {
		t.Errorf("the refusal must say the reviewer has no review on record; got: %v", bad)
	}

	// A lane that really did review the issue passes.
	if err := l.Append(ledger.Record{
		ID: "916-seat1", Issue: "916", Lane: "916-seat1",
		Role: ledger.RoleReview, State: ledger.Complete, Result: "VERDICT: APPROVE",
	}); err != nil {
		t.Fatal(err)
	}
	if bad := independent(l, "916", "916-seat1"); bad != nil {
		t.Fatalf("a lane with a completed review on this issue is a real reviewer; got: %v", bad)
	}
}

// A review turn that failed, timed out, or is still running has not reviewed
// anything. Counting it would let a crashed seat satisfy the gate.
func TestUnfinishedReviewDoesNotCountAsAReview(t *testing.T) {
	for _, st := range []ledger.State{ledger.Dispatched, ledger.Failed, ledger.Unknown} {
		l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range []ledger.Record{
			{ID: "916-real", Issue: "916", Lane: "916-real", State: ledger.Complete},
			{ID: "916-seat", Issue: "916", Lane: "916-seat", Role: ledger.RoleReview, State: st},
		} {
			if err := l.Append(r); err != nil {
				t.Fatal(err)
			}
		}
		if bad := independent(l, "916", "916-seat"); bad == nil {
			t.Errorf("a review in state %q has not reviewed anything and must not satisfy the gate", st)
		}
	}
}

// Authorship evidence must come from a turn that actually finished. A failed
// or unobserved turn is not proof a lane authored the work.
func TestOnlyACompletedTurnIsAuthorshipEvidence(t *testing.T) {
	for _, st := range []ledger.State{ledger.Failed, ledger.Unknown, ledger.Dispatched} {
		l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		// A real reviewer, so this test isolates the AUTHOR-state rule. Without
		// it the gate refuses on the missing reviewer and the test passes for
		// the wrong reason -- which it did until a mutation exposed it.
		for _, r := range []ledger.Record{
			{ID: "916-f", Issue: "916", Lane: "916-f", State: st},
			reviewRecord("916", "916-seat"),
		} {
			if err := l.Append(r); err != nil {
				t.Fatal(err)
			}
		}
		bad := independent(l, "916", "916-seat")
		if bad == nil {
			t.Errorf("a turn in state %q is not authorship evidence; the gate must refuse", st)
		} else if !strings.Contains(strings.Join(bad, " "), "asserted") && st != ledger.Dispatched {
			t.Errorf("state %q: expected the refusal to be about authorship evidence; got: %v", st, bad)
		}
	}
}

// A council seat found the gate never read what the review SAID. A review
// turn that returned REQUEST CHANGES satisfied it exactly like an approval,
// because `reviewed` was set from the record's existence, not its content.
func TestAReviewThatRefusedIsNotAnApproval(t *testing.T) {
	for _, body := range []string{
		"VERDICT: REQUEST CHANGES\n\nfound a real defect",
		"VERDICT: COULD NOT DETERMINE\n\nno network",
		"the process produced nothing useful",
		"",
	} {
		l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range []ledger.Record{
			{ID: "a", Issue: "916", Lane: "author", State: ledger.Complete},
			{ID: "s", Issue: "916", Lane: "seat", Role: ledger.RoleReview, State: ledger.Complete, Result: body},
		} {
			if err := l.Append(r); err != nil {
				t.Fatal(err)
			}
		}
		if bad := independentAt(l, "916", "seat", time.Time{}); bad == nil {
			t.Errorf("a review whose report reads %.30q is not an approval", body)
		}
	}
}

func TestAnApprovingReviewSatisfiesTheGate(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []ledger.Record{
		{ID: "a", Issue: "916", Lane: "author", State: ledger.Complete},
		{ID: "s", Issue: "916", Lane: "seat", Role: ledger.RoleReview, State: ledger.Complete,
			Result: "VERDICT: APPROVE\n\nchecked and sound"},
	} {
		if err := l.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if bad := independentAt(l, "916", "seat", time.Time{}); bad != nil {
		t.Fatalf("an approving review by a non-author satisfies the gate; got: %v", bad)
	}
}

// A review of an earlier version of the same issue is not a review of what is
// being merged now. The old supervisor recorded a Reviewed-SHA for exactly
// this; the Go gate had no equivalent until a council seat pointed it out.
func TestAReviewThatPredatesTheHeadIsStale(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	reviewedAt := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	for _, r := range []ledger.Record{
		{ID: "a", Issue: "916", Lane: "author", State: ledger.Complete, At: reviewedAt.Add(-time.Hour)},
		{ID: "s", Issue: "916", Lane: "seat", Role: ledger.RoleReview, State: ledger.Complete,
			At: reviewedAt, Result: "VERDICT: APPROVE"},
	} {
		if err := l.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	// Head pushed AFTER the review: the review saw something else.
	if bad := independentAt(l, "916", "seat", reviewedAt.Add(time.Minute)); bad == nil {
		t.Fatal("a review predating the head has not reviewed what is being merged")
	}
	// Head pushed BEFORE the review: the review saw this.
	if bad := independentAt(l, "916", "seat", reviewedAt.Add(-time.Minute)); bad != nil {
		t.Fatalf("a review after the head is current; got: %v", bad)
	}
}
