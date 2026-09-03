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
func cleanFixture(t *testing.T) (*PR, *ledger.Ledger) {
	t.Helper()
	checkStart := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	reviewedAt := checkStart.Add(1 * time.Hour)
	p := &PR{
		Number:        926,
		HeadOID:       "deadbeef",
		State:         "OPEN",
		Checks:        []Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: checkStart.Format(time.RFC3339)}},
		ClosingIssues: []closingIssue{{Number: 926}},
		Comments:      []Comment{{Body: "Review-Lane: lane-review\nReviewed-SHA: deadbeef\nVerdict: APPROVE\n"}},
	}
	l := newLedger(t,
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: reviewedAt},
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

// Bypass 1: reviewer lane also has a role=author record on the closing
// issue. Removing the self-review check must be the ONLY way this passes.
func TestBypass_SelfReview(t *testing.T) {
	p, l := cleanFixture(t)
	if err := l.Append(ledger.Record{ID: "a2", Issue: "926", Lane: "lane-review", Role: ledger.RoleAuthor, State: ledger.Complete}); err != nil {
		t.Fatal(err)
	}
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a lane that also authored work on the closing issue was allowed to review its own PR")
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
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete},
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
		ledger.Record{ID: "a1", Issue: "926", Lane: "lane-author", Role: ledger.RoleAuthor, State: ledger.Complete},
		ledger.Record{ID: "r1", Issue: "926", Lane: "lane-review", Role: ledger.RoleReviewer, PR: 926, State: ledger.Complete, At: staleAt},
	)
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a review completed before the current head's checks started was accepted as current")
	}
}

// Bypass 5: caller-supplied issue is no longer accepted at all -- Evaluate
// takes no issue argument (a signature-level guarantee: main.go's "merge"
// case no longer has one to pass), so an author cannot point the gate at an
// unrelated, cleanly-authored issue using only record shapes `estate
// dispatch` writes itself (agent-estate#926's exact reported exploit). A PR
// reporting NO closing issue must refuse rather than merge on the strength
// of some unrelated issue's clean author/reviewer pair. Removing the
// explicit "closes no issue" guard does not, by itself, open this hole --
// an empty issue set also makes authorLanes() return nothing, so the
// "authorship unknown" guard downstream still refuses. Both guards are
// asserted here (via the Reasons text) precisely because the explicit one
// is what makes the refusal reason legible ("closes no issue") rather than
// merely "cannot tell who authored this" -- an operator debugging a refused
// merge needs to know which of the two it is.
func TestBypass_UnlinkedPRRefusesEvenWithAPerfectLedgerElsewhere(t *testing.T) {
	p, l := cleanFixture(t)
	p.ClosingIssues = nil
	d := evaluateWithPR(p, "lane-review", l)
	if d.Allow {
		t.Fatal("bypass: a PR closing no issue was allowed to merge because SOME unrelated issue had a clean author/reviewer pair on record")
	}
	joined := strings.Join(d.Reasons, " | ")
	if !strings.Contains(joined, "closes no issue") {
		t.Fatalf("refused, but not for the expected reason (closes no issue): %v", d.Reasons)
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
