package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

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

// ---------------------------------------------------------------------
// authorLanes -- constraints 1 and 2: role recorded at dispatch, never
// inferred from the issue a review turn happens to share with its subject.
// ---------------------------------------------------------------------

func TestAuthorLanesIgnoresReviewerRoleOnSameIssue(t *testing.T) {
	// The exact bug report: a review turn dispatched against issue 926,
	// sharing the issue with the work it reviews, must never be read as an
	// author.
	l := newLedger(t, ledger.Record{ID: "x", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, State: ledger.Complete})
	got, err := authorLanes(l, map[string]bool{"926": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("authorLanes(%v) treated a role=reviewer record as an author", got)
	}
}

func TestAuthorLanesFindsRoleAuthor(t *testing.T) {
	l := newLedger(t, ledger.Record{ID: "x", Issue: "42", Lane: "lane-a", Role: ledger.RoleAuthor, State: ledger.Complete})
	got, err := authorLanes(l, map[string]bool{"42": true})
	if err != nil {
		t.Fatal(err)
	}
	if !got["lane-a"] {
		t.Fatalf("authorLanes(%v) missed a genuine role=author record", got)
	}
}

func TestAuthorLanesUnsetRoleDefaultsToAuthor(t *testing.T) {
	// Backward compatibility: a record written before Role existed is a
	// pre-fix authoring turn, not an unknown one.
	l := newLedger(t, ledger.Record{ID: "x", Issue: "42", Lane: "lane-a", State: ledger.Complete})
	got, err := authorLanes(l, map[string]bool{"42": true})
	if err != nil {
		t.Fatal(err)
	}
	if !got["lane-a"] {
		t.Fatal("authorLanes did not default an unset Role to author")
	}
}

// ---------------------------------------------------------------------
// reviewerCompleted -- constraint 3: a dispatched-but-unfinished review is
// not independence.
// ---------------------------------------------------------------------

func TestReviewerCompletedRequiresComplete(t *testing.T) {
	l := newLedger(t, ledger.Record{ID: "r1", Lane: "lane-b", Role: ledger.RoleReviewer, PR: 926, State: ledger.Dispatched})
	_, ok, err := reviewerCompleted(l, 926, "lane-b")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("reviewerCompleted accepted a dispatched-but-unfinished review turn")
	}
}

func TestReviewerCompletedRequiresMatchingPR(t *testing.T) {
	l := newLedger(t, ledger.Record{ID: "r1", Lane: "lane-b", Role: ledger.RoleReviewer, PR: 111, State: ledger.Complete})
	_, ok, err := reviewerCompleted(l, 926, "lane-b")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("reviewerCompleted matched a completed review turn on a DIFFERENT PR")
	}
}

func TestReviewerCompletedPasses(t *testing.T) {
	at := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	l := newLedger(t, ledger.Record{ID: "r1", Lane: "lane-b", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: at})
	got, ok, err := reviewerCompleted(l, 926, "lane-b")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !got.Equal(at) {
		t.Fatalf("reviewerCompleted(926, lane-b) = %v, %v; want %v, true", got, ok, at)
	}
}

// ---------------------------------------------------------------------
// resolveLaneVerdict -- constraint 4: a Verdict: line, never a substring
// match. Council comments in this repo quote prior verdicts in a seat
// table; a substring match must not read a quoted APPROVE as a fresh one.
// ---------------------------------------------------------------------

func TestVerdictSubstringInQuotedTableIsNotApproval(t *testing.T) {
	body := "Review-Lane: lane-b\n" +
		"## Council: NOT CLEAN\n" +
		"| seat | verdict |\n" +
		"|---|---|\n" +
		"| x | **APPROVE** |\n" +
		"\nVerdict: REQUEST CHANGES\n"
	lv := resolveLaneVerdict([]Comment{{Body: body}}, "lane-b")
	if !lv.found || !lv.ok {
		t.Fatalf("resolveLaneVerdict did not resolve a real Verdict: line: %+v", lv)
	}
	if lv.decision != verdictRejected {
		t.Fatalf("resolveLaneVerdict read %q as the verdict -- a quoted APPROVE in a table leaked in as a substring match", lv.decision)
	}
}

func TestVerdictRequiresReviewLaneTrailerMatchingReviewer(t *testing.T) {
	body := "Review-Lane: someone-else\nVerdict: APPROVE\n"
	lv := resolveLaneVerdict([]Comment{{Body: body}}, "lane-b")
	if lv.found {
		t.Fatal("resolveLaneVerdict matched a comment whose Review-Lane: names a different lane")
	}
}

func TestVerdictInFencedBlockIsIgnored(t *testing.T) {
	body := "Review-Lane: lane-b\n```\nVerdict: APPROVE\n```\n"
	lv := resolveLaneVerdict([]Comment{{Body: body}}, "lane-b")
	if lv.found {
		t.Fatal("resolveLaneVerdict read a Verdict: line inside a fenced code block")
	}
}

func TestVerdictApprovePasses(t *testing.T) {
	body := "Review-Lane: lane-b\nReviewed-SHA: abc123\nVerdict: APPROVE\n"
	lv := resolveLaneVerdict([]Comment{{Body: body}}, "lane-b")
	if !lv.found || !lv.ok || lv.decision != verdictApproved {
		t.Fatalf("resolveLaneVerdict did not read a plain approval: %+v", lv)
	}
	if lv.reviewedSHA != "abc123" {
		t.Fatalf("resolveLaneVerdict reviewedSHA = %q, want abc123", lv.reviewedSHA)
	}
}

// ---------------------------------------------------------------------
// agent-estate#943 -- reviewLaneRE must absorb the one fixed,
// estate-written "dispatch/" branch prefix internal/isolate.Create puts
// on a lane's own checkout, without loosening to a substring or suffix
// match (that family of holes took six review rounds to close --
// agent-estate#934).
// ---------------------------------------------------------------------

func TestVerdictReviewLaneDispatchPrefixMatches(t *testing.T) {
	body := "Review-Lane: dispatch/938-1788417348\nReviewed-SHA: abc123\nVerdict: APPROVE\n"
	lv := resolveLaneVerdict([]Comment{{Body: body}}, "938-1788417348")
	if !lv.found || !lv.ok || lv.decision != verdictApproved {
		t.Fatalf("resolveLaneVerdict did not match a Review-Lane: trailer carrying the dispatch/ branch prefix: %+v", lv)
	}
}

func TestVerdictReviewLaneBareFormStillMatches(t *testing.T) {
	body := "Review-Lane: 938-1788417348\nReviewed-SHA: abc123\nVerdict: APPROVE\n"
	lv := resolveLaneVerdict([]Comment{{Body: body}}, "938-1788417348")
	if !lv.found || !lv.ok || lv.decision != verdictApproved {
		t.Fatalf("resolveLaneVerdict did not match the bare (unprefixed) Review-Lane: form: %+v", lv)
	}
}

func TestVerdictReviewLaneEmbeddedInLongerStringRefuses(t *testing.T) {
	body := "Review-Lane: not-really-938-1788417348\nVerdict: APPROVE\n"
	lv := resolveLaneVerdict([]Comment{{Body: body}}, "938-1788417348")
	if lv.found {
		t.Fatal("resolveLaneVerdict matched a Review-Lane: trailer that only embeds the lane id inside a longer string")
	}
}

func TestVerdictReviewLaneMatchingSuffixRefuses(t *testing.T) {
	body := "Review-Lane: 8938-1788417348\nVerdict: APPROVE\n"
	lv := resolveLaneVerdict([]Comment{{Body: body}}, "938-1788417348")
	if lv.found {
		t.Fatal("resolveLaneVerdict matched a different lane whose id merely ends with the wanted lane id")
	}
}

func TestVerdictReviewLaneDoubledPrefixRefuses(t *testing.T) {
	body := "Review-Lane: dispatch/dispatch/938-1788417348\nVerdict: APPROVE\n"
	lv := resolveLaneVerdict([]Comment{{Body: body}}, "938-1788417348")
	if lv.found {
		t.Fatal("resolveLaneVerdict matched a doubled dispatch/ prefix -- only the single, estate-written prefix may be stripped")
	}
}

func TestVerdictReviewLaneQuotedInTableDoesNotMatchDifferentLane(t *testing.T) {
	// The council-comment shape that caused a real hole before
	// (agent-estate#926/#934): a comment quotes another lane's own
	// trailer inside a table or code block. reviewLaneRE is anchored to
	// the START of a line, so a quoted trailer buried mid-line (a table
	// cell) must never be read as this comment's own Review-Lane:.
	body := "Review-Lane: dispatch/938-1788417348\nVerdict: APPROVE\n\n" +
		"## Council\n" +
		"| seat | trailer |\n|---|---|\n" +
		"| council | Review-Lane: dispatch/999-1788417348 |\n"
	lv := resolveLaneVerdict([]Comment{{Body: body}}, "999-1788417348")
	if lv.found {
		t.Fatal("resolveLaneVerdict matched a different lane's Review-Lane: trailer quoted inside a table cell")
	}
}

// ---------------------------------------------------------------------
// earliestCheckStart -- constraint 5: staleness against when checks
// actually started, not a committer date.
// ---------------------------------------------------------------------

func TestEarliestCheckStartPicksTheEarliest(t *testing.T) {
	p := &PR{Checks: []Check{
		{Name: "a", StartedAt: "2026-09-03T02:10:41Z"},
		{Name: "b", StartedAt: "2026-09-03T02:09:00Z"},
	}}
	got, ok := earliestCheckStart(p)
	if !ok {
		t.Fatal("earliestCheckStart found nothing")
	}
	want := time.Date(2026, 9, 3, 2, 9, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("earliestCheckStart = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------
// Evaluate end to end, and the "write the bypass" mutations for each
// constraint: each test below is the ONLY guard standing between an
// otherwise-clean fixture and Allow=true. Removing the corresponding
// check in gate.go turns the matching test green->never-refuses, i.e. red.
// ---------------------------------------------------------------------

// evaluateWithPR drives the REAL evaluate() (gate.go's unexported core of
// Evaluate) against a fixture PR, so these bypass tests exercise the same
// function Evaluate and main.go call -- not a reimplementation that could
// drift from it and stop catching a real regression.
func evaluateWithPR(p *PR, reviewerLane string, l *ledger.Ledger) Decision {
	return evaluate(p, reviewerLane, l)
}

// cleanFixture is a PR and ledger that satisfy every constraint at once --
// the baseline every bypass test starts from, breaking exactly one thing.
// The PR's head ref is "dispatch/a1", matching the role=author ledger
// record's own ID -- the structural join agent-estate#940 establishes --
// AND the PR's headRefOid ("deadbeef") matches that record's own HeadSHA,
// the second half of the join #940's follow-up review added: the branch
// name alone is not enough, the estate's own recorded commit must agree
// with what the PR actually points at.
func cleanFixture(t *testing.T) (*PR, *ledger.Ledger) {
	t.Helper()
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(1 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "deadbeef",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments:    []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: deadbeef\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	return p, l
}

func TestCleanFixturePasses(t *testing.T) {
	p, l := cleanFixture(t)
	d := evaluateWithPR(p, "lane-review", l)
	if !d.Allow {
		t.Fatalf("evaluateWithPR refused a genuinely clean fixture: %v", d.Reasons)
	}
}

// Bypass 1: the lane the head ref's dispatch id resolves to (structurally,
// via authorFromHeadRef + authorLaneForDispatchID) IS the reviewer lane.
// Removing the self-review check must be the ONLY way this passes.
func TestBypass_SelfReview(t *testing.T) {
	p, _ := cleanFixture(t)
	checkStart, err := time.Parse(time.RFC3339, p.Checks[0].StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	reviewedAt := checkStart.Add(1 * time.Hour)
	l := newLedger(t,
		// The dispatch id "a1" the head ref names ("dispatch/a1") was
		// authored by lane-review itself. HeadSHA matches the PR's head so
		// the HeadSHA check passes and this test isolates the self-review
		// check specifically, not the newer provenance check.
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-review", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a lane that also authored the dispatch this PR's head ref names was allowed to review its own PR")
	}
}

// The lane that dispatched the code that closed a related issue, but is NOT
// the lane whose dispatch id the PR's own head ref names, must NOT be
// treated as a self-review. authorLanes()'s issue-keyed matching (superseded
// by the head-ref join for authorship, but still tested in isolation above)
// must never leak back into evaluate()'s self-review check -- an unrelated
// authoring turn sharing the issue is not this PR's author.
func TestUnrelatedAuthorOnSameIssueIsNotSelfReview(t *testing.T) {
	p, l := cleanFixture(t)
	if err := l.Append(ledger.Record{ID: "a2", Issue: "926", Lane: "lane-review", Role: ledger.RoleAuthor, State: ledger.Complete}); err != nil {
		t.Fatal(err)
	}
	d := evaluateWithPR(p, "lane-review", l)
	if !d.Allow {
		t.Fatalf("evaluateWithPR refused a PR whose head-ref-derived author was NOT the reviewer, merely because the reviewer separately authored something on the same issue: %v", d.Reasons)
	}
}

// Bypass 2: reviewer never completed a review turn for this PR (dispatched
// but unfinished). The PR reports checks with NO startedAt on purpose: with
// a real startedAt present, an unset reviewedAt (the zero time, since no
// Complete record exists) would ALSO be caught by the staleness guard
// (constraint 5), masking whether constraint 3's own check is what
// refused. Dropping startedAt removes that safety net so this test is
// isolated to constraint 3 -- confirmed by running it against gate.go with
// the "no completed role=reviewer turn" refusal deleted: it goes green
// (see the mutation note in this package's PR body).
func TestBypass_UnfinishedReview(t *testing.T) {
	p, _ := cleanFixture(t)
	p.Checks = []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS"}}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Dispatched},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a dispatched-but-unfinished review turn was accepted as independence")
	}
}

// Bypass 3: the only "approval" on the PR is a quoted one inside a table,
// with the real verdict being REQUEST CHANGES.
func TestBypass_QuotedApprovalSubstring(t *testing.T) {
	p, l := cleanFixture(t)
	p.Comments = []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: deadbeef\n" +
		"| seat | verdict |\n|---|---|\n| x | **APPROVE** |\n\nVerdict: REQUEST CHANGES\n"}}
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a REQUEST CHANGES comment that merely quotes APPROVE in a table was read as an approval")
	}
}

// Bypass 4: the review completed BEFORE the current head's checks started
// -- it reviewed stale code, even though its comment's Reviewed-SHA lies
// about matching head (or is simply missing, which must not default to
// "fine").
func TestBypass_StaleReview(t *testing.T) {
	p, _ := cleanFixture(t)
	checkStart, _ := time.Parse(time.RFC3339, p.Checks[0].StartedAt)
	staleAt := checkStart.Add(-1 * time.Hour)
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: staleAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a review completed before the current head's checks started was accepted as current")
	}
}

// Bypass 5 (agent-estate#940, superseding its own former shape): caller-
// supplied issue is still not accepted at all -- Evaluate takes no issue
// argument. Identity now comes from the PR's own head ref rather than
// closingIssuesReferences, so the exploit this used to guard against
// (pointing the gate at an unrelated, cleanly-authored ISSUE) no longer has
// an issue-keyed door to walk through in the first place: a PR whose head
// ref is an arbitrary feature branch is refused regardless of what it
// claims to close, or what any unrelated issue's ledger history looks like.
func TestBypass_HeadRefNotDispatchBranchRefuses(t *testing.T) {
	p, l := cleanFixture(t)
	p.HeadRefName = "feat/some-hand-named-branch"
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a PR whose head ref is not a dispatch branch was allowed to merge")
	}
	joined := strings.Join(d.Reasons, " | ")
	if !strings.Contains(joined, "is not a dispatch branch") {
		t.Fatalf("refused, but not for the expected reason (head ref is not a dispatch branch): %v", d.Reasons)
	}
}

// A dispatch branch whose id has no matching role=author ledger record --
// e.g. the worktree existed but the ledger was wiped, or the id is simply
// wrong -- must refuse rather than treat "found no record" as "no author,
// so nothing to self-review against."
func TestBypass_DispatchBranchWithNoMatchingAuthorRecordRefuses(t *testing.T) {
	p, l := cleanFixture(t)
	p.HeadRefName = "dispatch/does-not-exist"
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a dispatch branch naming an id with no role=author ledger record was allowed to merge")
	}
	joined := strings.Join(d.Reasons, " | ")
	if !strings.Contains(joined, "role=author ledger record") {
		t.Fatalf("refused, but not for the expected reason (no matching author record): %v", d.Reasons)
	}
}

// Bypass 5.1 (agent-estate#940's follow-up review, which blocked #952 over
// exactly this): the exact forgery demonstrated in that review. A lane with
// no relationship to dispatch id "a1" pushes ITS OWN content to a branch it
// names "dispatch/a1" -- borrowing a real, completed, unrelated dispatch's
// id -- and opens a PR from it. The branch-name join alone would resolve
// "a1" to the real author's lane and let this straight through. Requiring
// the PR's own head commit to match the HeadSHA the estate itself recorded
// for "a1" is what catches it: the attacker's commit ("attackercommit123")
// is real content, but it is not the content dispatch id "a1" actually
// produced, so it cannot equal the recorded SHA without literally
// reproducing that exact commit object.
func TestBypass_HeadSHAMismatchRefuses(t *testing.T) {
	p, l := cleanFixture(t)
	// cleanFixture's a1 record carries HeadSHA "deadbeef" -- the estate's own
	// recorded observation of what dispatch id a1 actually produced. Simulate
	// an attacker's PR pointing dispatch/a1 at different, self-pushed content.
	p.HeadOID = "attackercommit123"
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("SECURITY: a PR whose head ref names a real dispatch id, but whose head commit does not match that dispatch's own recorded HeadSHA, was allowed to merge -- branch-name forgery")
	}
	joined := strings.Join(d.Reasons, " | ")
	if !strings.Contains(joined, "does not match the HeadSHA") {
		t.Fatalf("refused, but not for the expected reason (HeadSHA mismatch): %v", d.Reasons)
	}
}

// A role=author record written before HeadSHA existed (or one whose
// worktree observation itself failed -- see main.go's dispatch case) has no
// recorded commit to compare against. That absence must refuse, not be
// treated as "no evidence either way, so nothing contradicts it."
func TestBypass_MissingHeadSHARefuses(t *testing.T) {
	p, l := newLedgerWithAuthorRecord(t, ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete})
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a role=author record with no recorded HeadSHA was treated as provenance-confirmed")
	}
	joined := strings.Join(d.Reasons, " | ")
	if !strings.Contains(joined, "no recorded HeadSHA") {
		t.Fatalf("refused, but not for the expected reason (missing HeadSHA): %v", d.Reasons)
	}
}

// An in-flight or abandoned dispatch (State != Complete) must not resolve
// authorship even if it happens to carry a HeadSHA from some earlier,
// incomplete observation -- the same requirement reviewerRecord already
// applies to review turns. A turn that never finished cannot vouch for what
// its own worktree currently holds.
func TestBypass_IncompleteAuthorRecordRefuses(t *testing.T) {
	p, l := newLedgerWithAuthorRecord(t, ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Dispatched, HeadSHA: "deadbeef"})
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a role=author record that was never Complete was accepted as authorship")
	}
}

// newLedgerWithAuthorRecord builds cleanFixture's PR (head ref dispatch/a1,
// head commit deadbeef) paired with a caller-supplied a1 record and a
// genuine, unrelated reviewer completion -- for tests exercising only the
// author-record shape.
func newLedgerWithAuthorRecord(t *testing.T, authorRec ledger.Record) (*PR, *ledger.Ledger) {
	t.Helper()
	p, _ := cleanFixture(t)
	checkStart, err := time.Parse(time.RFC3339, p.Checks[0].StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	reviewedAt := checkStart.Add(1 * time.Hour)
	l := newLedger(t,
		authorRec,
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	return p, l
}

// The structural fix's whole point (agent-estate#940's #944 case): a PR
// closing NO issue GitHub can confirm -- or closing an issue filed after the
// dispatch that will close it -- must still merge when its head ref is a
// genuine dispatch branch with a real role=author record. Proves the old
// mandatory closing-issue requirement is actually gone, not merely unused.
func TestAllowsWithNoClosingIssueWhenHeadRefIsAGenuineDispatchBranch(t *testing.T) {
	p, l := cleanFixture(t)
	p.ClosingIssues = nil
	d := evaluateWithPR(p, "lane-review", l)
	if !d.Allow {
		t.Fatalf("evaluateWithPR refused a PR with a genuine dispatch head ref merely because it closed no issue: %v", d.Reasons)
	}
}

// A PR body's self-declared Author-Lane: trailer naming a lane OUTSIDE the
// verified author chain must refuse -- the same class of forgery
// agent-estate#934 closed for Review-Lane:, applied to authorship.
func TestBypass_ForgedAuthorLaneTrailerContradictsHeadRefRefuses(t *testing.T) {
	p, l := cleanFixture(t)
	p.Body = "Author-Lane: someone-else-entirely\n"
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a PR body's Author-Lane: trailer contradicting the head-ref-derived author was allowed to merge")
	}
	joined := strings.Join(d.Reasons, " | ")
	if !strings.Contains(joined, "names a lane outside the verified author chain") {
		t.Fatalf("refused, but not for the expected reason (Author-Lane outside chain): %v", d.Reasons)
	}
}

// An ABSENT Author-Lane: trailer, or one that agrees with the head-ref
// derived author, must never itself cause a refusal -- the trailer is only
// ever a contradiction check, never a requirement.
func TestAuthorLaneTrailerAgreeingOrAbsentIsFine(t *testing.T) {
	for _, body := range []string{"", "Author-Lane: lane-author\n"} {
		p, l := cleanFixture(t)
		p.Body = body
		d := evaluateWithPR(p, "lane-review", l)
		if !d.Allow {
			t.Fatalf("evaluateWithPR refused over Author-Lane: body %q: %v", body, d.Reasons)
		}
	}
}

// ---------------------------------------------------------------------
// agent-estate#940's own follow-up: "the join works for a fresh dispatch,
// and does not survive a fix pass." Condition 2c -- authorRecordForFixPassChain.
// ---------------------------------------------------------------------

// The root dispatch (a1) opened the PR at "deadbeef". A LATER, independently
// dispatched fix pass (fix1) continued the SAME branch: its own Base is
// exactly where the root left off, and its own estate-observed HeadSHA is
// the PR's current head. The gate must walk that one-hop chain and allow.
func TestFixPassChainResolves(t *testing.T) {
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(2 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "fixedcafe01",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments:    []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: fixedcafe01\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		ledger.Record{ID: "fix1", Issue: "926", Lane: "lane-fix", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "deadbeef", HeadSHA: "fixedcafe01"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if !d.Allow {
		t.Fatalf("evaluateWithPR refused a genuine one-hop fix-pass chain that continues the root dispatch's own branch: %v", d.Reasons)
	}
}

// The chain must walk MULTIPLE hops, not just one -- a second fix pass
// continuing the first's own output.
func TestFixPassChainResolvesMultipleHops(t *testing.T) {
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(3 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "thirdhopsha",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments:    []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: thirdhopsha\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		ledger.Record{ID: "fix1", Issue: "926", Lane: "lane-fix1", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "deadbeef", HeadSHA: "secondhopsha"},
		ledger.Record{ID: "fix2", Issue: "926", Lane: "lane-fix2", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "secondhopsha", HeadSHA: "thirdhopsha"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if !d.Allow {
		t.Fatalf("evaluateWithPR refused a genuine two-hop fix-pass chain: %v", d.Reasons)
	}
}

// Bypass (the "Prove it" forgery case): a PR's current head SHA matches a
// real, completed, PR-scoped role=author record's own HeadSHA -- but that
// record's Base does NOT continue from this PR's root dispatch. Its HeadSHA
// happens to equal the PR's current head (e.g. a director mistake, or an
// attempt to borrow a real record's credibility), but nothing in the ledger
// explains how the code got from the root's "deadbeef" to here. The chain
// must not resolve on a HeadSHA/PR match alone.
func TestBypass_FixPassRecordWithWrongBaseDoesNotChain(t *testing.T) {
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(2 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "fixedcafe01",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments:    []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: fixedcafe01\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		// Same PR number, same HeadSHA as the PR's current head -- but its
		// OWN Base does not equal the root's HeadSHA. Nothing in the ledger
		// accounts for how the code moved from "deadbeef" to here.
		ledger.Record{ID: "fix1", Issue: "926", Lane: "lane-fix", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "somewhereunrelated", HeadSHA: "fixedcafe01"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("SECURITY: a PR-scoped role=author record whose HeadSHA matches the PR's current head, but whose own Base does not continue from the root dispatch, was accepted as a fix-pass chain")
	}
	joined := strings.Join(d.Reasons, " | ")
	if !strings.Contains(joined, "does not match the HeadSHA") {
		t.Fatalf("refused, but not for the expected reason (chain does not resolve): %v", d.Reasons)
	}
}

// Bypass: a genuine, completed, PR-scoped role=author record whose HeadSHA
// happens to equal the target PR's current head, but which was scoped to a
// DIFFERENT pull request entirely. This is the exact shape of "a PR whose
// head carries code from a dispatch that did not produce it" -- the dispatch
// really did produce that commit, but for PR 200, not PR 926, and PR 926's
// own root dispatch never accounts for it.
func TestBypass_FixPassChainCannotBorrowUnrelatedPRsDispatch(t *testing.T) {
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(2 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "unrelatedsha01",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments:    []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: unrelatedsha01\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		// A genuine, completed fix-pass dispatch -- for PR 200, not PR 926 --
		// whose own Base ("deadbeef") coincidentally equals PR 926's root
		// HeadSHA. Scoped to the wrong PR, so it must not chain here.
		ledger.Record{ID: "other", Issue: "200", Lane: "lane-other", Role: ledger.RoleAuthor, PR: 200, State: ledger.Complete, Base: "deadbeef", HeadSHA: "unrelatedsha01"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("SECURITY: a PR's head SHA matching a real, completed dispatch scoped to a DIFFERENT PR number was accepted as this PR's own fix-pass chain")
	}
	joined := strings.Join(d.Reasons, " | ")
	if !strings.Contains(joined, "does not match the HeadSHA") {
		t.Fatalf("refused, but not for the expected reason (chain does not resolve): %v", d.Reasons)
	}
}

// A fix-pass record that committed NOTHING (Base == HeadSHA) shares its
// Base with whatever a REAL next hop off the same commit also carries. The
// chain walker considers ledger records in order and takes the first Base
// match it finds (authorRecordForFixPassChain's doc comment); if a no-op
// record for that same Base happened to be appended first, picking it
// would dead-end the walk at an already-visited SHA even though a genuine
// continuation exists. The no-op guard is what keeps the walk from ever
// choosing that dead end over the real hop, regardless of ledger order.
func TestFixPassChainSkipsANoOpRecordAtTheSameBaseRatherThanDeadEnding(t *testing.T) {
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(2 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "fixedcafe01",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments:    []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: fixedcafe01\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		// A no-op fix pass off the SAME base as the real hop below, appended
		// FIRST so it is the earlier candidate the walker would see.
		ledger.Record{ID: "noop", Issue: "926", Lane: "lane-noop", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "deadbeef", HeadSHA: "deadbeef"},
		// The genuine continuation, off the same base, that actually is the
		// PR's current head.
		ledger.Record{ID: "fix1", Issue: "926", Lane: "lane-fix", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "deadbeef", HeadSHA: "fixedcafe01"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if !d.Allow {
		t.Fatalf("evaluateWithPR refused a genuine fix-pass chain merely because an earlier, unrelated no-op record shared the same Base: %v", d.Reasons)
	}
}

// agent-estate#940's over-refusal, demonstrated on real PRs #963/#964: an
// Author-Lane: trailer naming the ROOT dispatch of a chain whose current
// head was moved by a later fix pass must be ACCEPTED, not refused. The
// trailer was written when the PR was opened, before any fix pass existed,
// so it necessarily names the root -- that is still a true statement about
// who authored this PR, even after the chain-terminal author changes.
func TestAuthorLaneTrailerNamingChainRootIsAccepted(t *testing.T) {
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(2 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "fixedcafe01",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Body:        "Closes #926\n\nAuthor-Lane: lane-author\n",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments:    []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: fixedcafe01\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		ledger.Record{ID: "fix1", Issue: "926", Lane: "lane-fix", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "deadbeef", HeadSHA: "fixedcafe01"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if !d.Allow {
		t.Fatalf("evaluateWithPR refused an Author-Lane: trailer naming the verified chain's own root dispatch: %v", d.Reasons)
	}
}

// A trailer naming a MID-CHAIN hop (neither the root nor the terminal
// author) in a multi-hop chain must also be accepted -- every hop the gate
// itself walked is a lane that genuinely authored some of this PR's code.
func TestAuthorLaneTrailerNamingMidChainHopIsAccepted(t *testing.T) {
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(3 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "thirdhopsha",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Body:        "Closes #926\n\nAuthor-Lane: lane-fix1\n",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments:    []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: thirdhopsha\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		ledger.Record{ID: "fix1", Issue: "926", Lane: "lane-fix1", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "deadbeef", HeadSHA: "secondhopsha"},
		ledger.Record{ID: "fix2", Issue: "926", Lane: "lane-fix2", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "secondhopsha", HeadSHA: "thirdhopsha"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if !d.Allow {
		t.Fatalf("evaluateWithPR refused an Author-Lane: trailer naming a genuine mid-chain hop: %v", d.Reasons)
	}
}

// Bypass: a trailer naming a lane the chain walk never visited at all --
// neither the root nor any fix-pass hop -- must still refuse. Accepting any
// hop is not the same as accepting anything; the trailer must still name a
// lane the Base/HeadSHA walk itself established produced code here.
func TestBypass_AuthorLaneTrailerNamingLaneOutsideChainRefuses(t *testing.T) {
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(2 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "fixedcafe01",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Body:        "Closes #926\n\nAuthor-Lane: lane-never-dispatched\n",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments:    []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: fixedcafe01\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		ledger.Record{ID: "fix1", Issue: "926", Lane: "lane-fix", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "deadbeef", HeadSHA: "fixedcafe01"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: an Author-Lane: trailer naming a lane the chain walk never visited was allowed to merge")
	}
	joined := strings.Join(d.Reasons, " | ")
	if !strings.Contains(joined, "names a lane outside the verified author chain") {
		t.Fatalf("refused, but not for the expected reason (Author-Lane outside chain): %v", d.Reasons)
	}
}

// Bypass: a trailer naming a lane that genuinely IS a fix-pass author, but
// for a DIFFERENT PR's own chain -- not this PR's. The chain walk is
// already PR-scoped (authorRecordForFixPassChain filters r.PR != pr), so
// that lane never enters this PR's chainLanes set; the trailer must still
// refuse even though the lane name is real and really did author something
// somewhere.
func TestBypass_AuthorLaneTrailerNamingDifferentPRsChainRefuses(t *testing.T) {
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(2 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "fixedcafe01",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Body:        "Closes #926\n\nAuthor-Lane: lane-other-pr-fix\n",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments:    []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: fixedcafe01\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		ledger.Record{ID: "fix1", Issue: "926", Lane: "lane-fix", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "deadbeef", HeadSHA: "fixedcafe01"},
		// A real fix-pass author, completed, but scoped to PR 200, not 926.
		ledger.Record{ID: "other", Issue: "200", Lane: "lane-other-pr-fix", Role: ledger.RoleAuthor, PR: 200, State: ledger.Complete, Base: "somesha", HeadSHA: "othersha"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: an Author-Lane: trailer naming a lane that authored a DIFFERENT PR's fix pass was allowed to merge")
	}
	joined := strings.Join(d.Reasons, " | ")
	if !strings.Contains(joined, "names a lane outside the verified author chain") {
		t.Fatalf("refused, but not for the expected reason (Author-Lane outside chain): %v", d.Reasons)
	}
}

// The self-review check must apply to the LATEST link in a resolved chain,
// not only the root dispatch -- a lane that authored nothing at the root but
// pushed the fix pass that IS the PR's current head must not be allowed to
// review its own work.
func TestBypass_SelfReviewViaFixPassChain(t *testing.T) {
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(2 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "fixedcafe01",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments:    []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: fixedcafe01\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		// The reviewer lane ITSELF pushed the fix pass that is the PR's
		// current head.
		ledger.Record{ID: "fix1", Issue: "926", Lane: "lane-review", Role: ledger.RoleAuthor, PR: 926, State: ledger.Complete, Base: "deadbeef", HeadSHA: "fixedcafe01"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: APPROVE\n"},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a lane that authored the fix pass which IS the PR's current head was allowed to review its own PR, because only the root dispatch's lane was checked for self-review")
	}
}

// Bypass 6: every failure to measure must refuse, not pass. A corrupt
// ledger (Current() returns an error, per ledger's own contract) must not
// read as "no authors on record, therefore fine." This is confirmed twice
// over here on purpose: end to end below, AND at the unit level by the two
// tests that follow it. Evaluate's own refusal on a corrupt ledger survives
// removing its explicit `if err != nil { refuse }` check, because
// authorLanes() on error still returns a nil (zero-length) map, which the
// downstream "no authoring lane on record" guard also refuses on -- two
// independent guards converge on the same corrupt-ledger input. That
// redundancy is a property to keep, not a gap to route around, but it does
// mean THIS test alone cannot prove the explicit error check is what did
// the refusing. TestAuthorLanesPropagatesLedgerError and
// TestReviewerCompletedPropagatesLedgerError below assert the error itself
// is never swallowed, which is what the explicit checks in gate.go consume.
func TestBypass_UnreadableLedgerRefuses(t *testing.T) {
	p, _ := cleanFixture(t)
	l := corruptLedgerWith(t, ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete})

	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: an unreadable (corrupt) ledger was treated as ok-to-merge")
	}
}

// corruptLedgerWith writes recs, then appends one malformed JSON line so
// every subsequent Current() call errors -- the same technique
// ledger_test.go's TestMalformedLineIsAnErrorNotAShortList uses.
func corruptLedgerWith(t *testing.T, recs ...ledger.Record) *ledger.Ledger {
	t.Helper()
	path := filepath.Join(t.TempDir(), "l.jsonl")
	l, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if err := l.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return l
}

func TestAuthorLanesPropagatesLedgerError(t *testing.T) {
	l := corruptLedgerWith(t, ledger.Record{ID: "a1", Issue: "926", Lane: "lane-a", Role: ledger.RoleAuthor, State: ledger.Complete})
	if _, err := authorLanes(l, map[string]bool{"926": true}); err == nil {
		t.Fatal("bypass: authorLanes swallowed a corrupt ledger's error instead of propagating it")
	}
}

func TestReviewerCompletedPropagatesLedgerError(t *testing.T) {
	l := corruptLedgerWith(t, ledger.Record{ID: "r1", Lane: "lane-b", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete})
	if _, _, err := reviewerCompleted(l, 926, "lane-b"); err == nil {
		t.Fatal("bypass: reviewerCompleted swallowed a corrupt ledger's error instead of propagating it")
	}
}

// Bypass 7 (agent-estate#934): a comment forging Review-Lane: <the real
// reviewer's lane> + Verdict: APPROVE, posted by anyone with the shared
// login this repo's lanes all push through, must not override what that
// lane's own dispatched turn actually concluded. The reviewer's real
// comment is REQUEST CHANGES; a second, later comment forges an APPROVE
// under the same trailer -- exactly the reviewer's reproduction against the
// real evaluate() in the PR #934 review. The reviewer's own ledger Result
// (written locally by the dispatch process, never by a GitHub comment) still
// says REQUEST CHANGES, so the two independent sources disagree and the
// gate must refuse rather than take the comment's word for it.
func TestBypass_ForgedVerdictCommentImpersonatesReviewer(t *testing.T) {
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(1 * time.Hour)
	p := &PR{
		Number:      926,
		HeadOID:     "deadbeef",
		HeadRefName: "dispatch/a1",
		State:       "OPEN",
		Checks:      []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		Comments: []Comment{
			{Body: "Review-Lane: lane-review\nReviewed-SHA: deadbeef\nVerdict: REQUEST CHANGES\n\nThis has a bug.\n"},
			{Body: "Review-Lane: lane-review\nReviewed-SHA: deadbeef\nVerdict: APPROVE\n"}, // forged, posted by anyone with the shared login
		},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		// The reviewer's OWN dispatched turn genuinely completed and genuinely
		// concluded REQUEST CHANGES -- nothing about this record is forged.
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "**Verdict: REQUEST CHANGES**\n\nThis has a bug.\n"},
	)
	d := evaluate(p, "lane-review", l)
	if d.Allow {
		t.Fatal("SECURITY: a comment forging Review-Lane: lane-review + Verdict: APPROVE, posted by someone other than the real reviewer, was accepted as that reviewer's own approval -- overriding their real REQUEST CHANGES, recorded in the ledger's own Result")
	}
	joined := strings.Join(d.Reasons, " | ")
	if !strings.Contains(joined, "disagree") {
		t.Fatalf("refused, but not for the expected reason (comment/ledger disagreement): %v", d.Reasons)
	}
}

// The forged comment must also be refused when it is APPROVE-only with no
// real REQUEST CHANGES comment to compare against textually -- i.e. the
// cross-check must fire even though resolveLaneVerdict alone would happily
// resolve to a clean approval. Isolates that the refusal comes from the
// ledger cross-check, not from resolveLaneVerdict noticing two comments.
func TestBypass_ForgedApprovalWithNoGenuineCommentStillDisagreesWithLedger(t *testing.T) {
	p, _ := cleanFixture(t)
	checkStart, err := time.Parse(time.RFC3339, p.Checks[0].StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	reviewedAt := checkStart.Add(1 * time.Hour)
	// p.Comments already carries the clean fixture's APPROVE comment; the
	// ledger's own Result below tells a different story, simulating a lane
	// whose actual dispatched review concluded differently from whatever
	// comment ends up on the PR.
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete, HeadSHA: "deadbeef"},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt, Result: "Verdict: REQUEST CHANGES\n"},
	)
	d := evaluate(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: PR comment APPROVE with a ledger Result of REQUEST CHANGES was allowed")
	}
}
