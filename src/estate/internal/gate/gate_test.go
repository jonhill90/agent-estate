package gate

import (
	"path/filepath"
	"testing"

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

func TestUnknownAuthorRefuses(t *testing.T) {
	l := newLedger(t)
	if bad := independent(l, "42", "lane-reviewer"); len(bad) == 0 {
		t.Fatal("independent() allowed a merge with no authoring lane on record")
	}
}

func TestSelfReviewRefuses(t *testing.T) {
	l := newLedger(t, ledger.Record{ID: "x", Issue: "42", Lane: "lane-a", State: ledger.Complete})
	if bad := independent(l, "42", "lane-a"); len(bad) == 0 {
		t.Fatal("independent() allowed a lane to review its own work")
	}
}

func TestMissingReviewerLaneRefuses(t *testing.T) {
	l := newLedger(t, ledger.Record{ID: "x", Issue: "42", Lane: "lane-a", State: ledger.Complete})
	if bad := independent(l, "42", "  "); len(bad) == 0 {
		t.Fatal("independent() allowed an unnamed reviewer")
	}
}

func TestDistinctLanesPass(t *testing.T) {
	l := newLedger(t, ledger.Record{ID: "x", Issue: "42", Lane: "lane-a", State: ledger.Complete})
	if bad := independent(l, "42", "lane-b"); len(bad) != 0 {
		t.Fatalf("independent() refused genuinely independent lanes: %v", bad)
	}
}
