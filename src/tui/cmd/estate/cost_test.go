package main

import (
	"errors"
	"testing"
	"time"
)

// TestBuildCostFetch_QuotaSurvivesCcusageFailure reproduces PR agent-tui#50's
// reopened review finding verbatim: a real machine had a working quota.sh
// but no working ccusage, and f3 showed "cost: unknown (ccusage
// unreadable)" with no quota line at all. buildCostFetch must fetch
// quota.sh independently of ccusage and carry it through even when the
// ccusage run itself fails outright.
func TestBuildCostFetch_QuotaSurvivesCcusageFailure(t *testing.T) {
	brokenCcusage := func(args []string) ([]byte, error) {
		return nil, errors.New("exec: \"ccusage\": executable file not found in $PATH")
	}
	quotaRun := func(args []string) ([]byte, int, error) {
		return []byte("  claude   session=18% used  weekly=9% used  resets in 2h\n"), 0, nil
	}

	fetch := buildCostFetchFromRunner(brokenCcusage, 0, quotaRun, time.Now)
	snap, err := fetch()
	if err != nil {
		// buildCostFetch must never propagate a plain ccusage failure as a
		// Go error -- cost.Model.Update folds ANY non-nil error into
		// cost.Unknown() (model.go's own blindness-test discipline),
		// which would silently drop the quota data this test is checking.
		t.Fatalf("fetch() returned an error, want nil (a ccusage failure must not discard quota data): %v", err)
	}
	if snap.Known {
		t.Error("snap.Known = true, want false -- ccusage itself is still unreadable")
	}
	q, ok := snap.Quotas["claude"]
	if !ok || !q.Known {
		t.Fatalf("snap.Quotas[claude] = %+v, want Known -- quota.sh succeeded independently of ccusage", q)
	}
	if q.SessionPercent.Value != 18 || q.WeeklyPercent.Value != 9 {
		t.Errorf("quota = %+v, want session 18%%, weekly 9%%", q)
	}
}

// TestBuildCostFetch_NoQuotaRunnerLeavesQuotasNil covers the "quota.sh
// could not be resolved at all" path (main.go's resolvedQuotaBin stays
// empty) -- quotaRun is nil, and buildCostFetch must never panic dialing a
// nil func, nor fabricate quota data that was never sourced.
func TestBuildCostFetch_NoQuotaRunnerLeavesQuotasNil(t *testing.T) {
	brokenCcusage := func(args []string) ([]byte, error) {
		return nil, errors.New("exec: \"ccusage\": executable file not found in $PATH")
	}
	fetch := buildCostFetchFromRunner(brokenCcusage, 0, nil, time.Now)
	snap, err := fetch()
	if err != nil {
		t.Fatalf("fetch(): %v", err)
	}
	if snap.Known {
		t.Error("snap.Known = true, want false")
	}
	if len(snap.Quotas) != 0 {
		t.Errorf("snap.Quotas = %+v, want empty -- no quota.sh was ever configured", snap.Quotas)
	}
}
