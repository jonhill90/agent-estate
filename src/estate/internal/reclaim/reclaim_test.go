package reclaim

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestAssessNoPIDRecorded(t *testing.T) {
	rec := ledger.Record{ID: "a", At: at("2026-09-03T10:00:00Z")}
	probe := func(pid int) (ProcessInfo, error) {
		t.Fatalf("probe should never be called when no pid is recorded")
		return ProcessInfo{}, nil
	}
	got := Assess(rec, time.Time{}, probe)
	if got.Reclaimable {
		t.Fatalf("no pid recorded must never be reclaimable, got %+v", got)
	}
}

func TestAssessDeadPIDIsReclaimable(t *testing.T) {
	rec := ledger.Record{ID: "a", PID: 4242, At: at("2026-09-03T10:00:00Z")}
	probe := func(pid int) (ProcessInfo, error) {
		if pid != 4242 {
			t.Fatalf("probe called with wrong pid %d", pid)
		}
		return ProcessInfo{Exists: false}, nil
	}
	got := Assess(rec, time.Time{}, probe)
	if !got.Reclaimable {
		t.Fatalf("a pid that is not running must be reclaimable, got %+v", got)
	}
}

func TestAssessLivePlausiblePIDIsNotReclaimable(t *testing.T) {
	recordedAt := at("2026-09-03T10:00:00Z")
	rec := ledger.Record{ID: "a", PID: 4242, At: recordedAt}
	probe := func(pid int) (ProcessInfo, error) {
		// Started a moment before the record -- Start() always returns
		// before the ledger Append() that records the pid runs.
		return ProcessInfo{Exists: true, Comm: "claude", StartedAt: recordedAt.Add(-2 * time.Second)}, nil
	}
	got := Assess(rec, time.Time{}, probe)
	if got.Reclaimable {
		t.Fatalf("a live, plausibly-continuous pid must not be reclaimed, got %+v", got)
	}
}

func TestAssessLivePIDWithUnknownCommAndStartIsNotReclaimable(t *testing.T) {
	// Neither comm nor start time could be determined -- an ambiguous live
	// pid must still be left alone. Reclaiming needs a positive
	// contradiction, not merely the absence of confirmation.
	rec := ledger.Record{ID: "a", PID: 4242, At: at("2026-09-03T10:00:00Z")}
	probe := func(pid int) (ProcessInfo, error) {
		return ProcessInfo{Exists: true}, nil
	}
	got := Assess(rec, time.Time{}, probe)
	if got.Reclaimable {
		t.Fatalf("an ambiguous live pid must not be reclaimed, got %+v", got)
	}
}

func TestAssessPIDReuseByLaterStartIsReclaimable(t *testing.T) {
	recordedAt := at("2026-09-03T10:00:00Z")
	rec := ledger.Record{ID: "a", PID: 4242, At: recordedAt}
	probe := func(pid int) (ProcessInfo, error) {
		// Started long after the pid was recorded: our process already
		// exited and something else picked up the number.
		return ProcessInfo{Exists: true, Comm: "claude", StartedAt: recordedAt.Add(5 * time.Minute)}, nil
	}
	got := Assess(rec, time.Time{}, probe)
	if !got.Reclaimable {
		t.Fatalf("a pid that started well after it was recorded must be reclaimed as reused, got %+v", got)
	}
}

func TestAssessPIDReuseByDifferentCommIsReclaimable(t *testing.T) {
	rec := ledger.Record{ID: "a", PID: 4242, At: at("2026-09-03T10:00:00Z")}
	probe := func(pid int) (ProcessInfo, error) {
		return ProcessInfo{Exists: true, Comm: "Finder"}, nil
	}
	got := Assess(rec, time.Time{}, probe)
	if !got.Reclaimable {
		t.Fatalf("a live pid now running an unrelated command must be reclaimed as reused, got %+v", got)
	}
}

func TestAssessRebootAfterRecordIsReclaimableWithoutProbing(t *testing.T) {
	rec := ledger.Record{ID: "a", PID: 4242, At: at("2026-09-03T10:00:00Z")}
	boot := at("2026-09-03T11:00:00Z") // rebooted an hour after the record
	probe := func(pid int) (ProcessInfo, error) {
		t.Fatalf("the reboot check must decide this without probing the process")
		return ProcessInfo{}, nil
	}
	got := Assess(rec, boot, probe)
	if !got.Reclaimable {
		t.Fatalf("a record older than the host's boot time must be reclaimable, got %+v", got)
	}
}

func TestAssessRebootBeforeRecordDoesNotShortCircuit(t *testing.T) {
	rec := ledger.Record{ID: "a", PID: 4242, At: at("2026-09-03T10:00:00Z")}
	boot := at("2026-09-01T00:00:00Z") // booted well before this turn was dispatched
	called := false
	probe := func(pid int) (ProcessInfo, error) {
		called = true
		return ProcessInfo{Exists: true, Comm: "claude"}, nil
	}
	got := Assess(rec, boot, probe)
	if !called {
		t.Fatalf("a record newer than boot must fall through to the process probe")
	}
	if got.Reclaimable {
		t.Fatalf("live process after a prior boot must not be reclaimed, got %+v", got)
	}
}

func TestAssessProbeErrorIsNotReclaimable(t *testing.T) {
	rec := ledger.Record{ID: "a", PID: 4242, At: at("2026-09-03T10:00:00Z")}
	probe := func(pid int) (ProcessInfo, error) {
		return ProcessInfo{}, errors.New("permission denied")
	}
	got := Assess(rec, time.Time{}, probe)
	if got.Reclaimable {
		t.Fatalf("a probe failure must never be reclaimed -- blindness is not evidence, got %+v", got)
	}
	if got.Reason == "" {
		t.Fatalf("a probe failure must still be reported with a reason")
	}
}

func TestReportDoesNotMutateLedger(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ledger.Record{ID: "dead", PID: 111, State: ledger.Dispatched, At: at("2026-09-03T10:00:00Z")}); err != nil {
		t.Fatal(err)
	}
	before, err := l.Current()
	if err != nil {
		t.Fatal(err)
	}

	inflight, err := l.InFlight()
	if err != nil {
		t.Fatal(err)
	}
	probe := func(pid int) (ProcessInfo, error) { return ProcessInfo{Exists: false}, nil }
	assessments := Report(inflight, time.Time{}, probe)
	if len(assessments) != 1 || !assessments[0].Reclaimable {
		t.Fatalf("expected one reclaimable assessment, got %+v", assessments)
	}

	after, err := l.Current()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || after[0].State != before[0].State {
		t.Fatalf("Report must never write to the ledger: before=%+v after=%+v", before, after)
	}
}

func TestApplyFreesOnlyReclaimableSlots(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	records := []ledger.Record{
		{ID: "dead", PID: 111, State: ledger.Dispatched, At: at("2026-09-03T10:00:00Z")},
		{ID: "live", PID: 222, State: ledger.Dispatched, At: at("2026-09-03T10:00:00Z")},
		{ID: "no-pid", State: ledger.Dispatched, At: at("2026-09-03T10:00:00Z")},
	}
	for _, r := range records {
		if err := l.Append(r); err != nil {
			t.Fatal(err)
		}
	}

	probe := func(pid int) (ProcessInfo, error) {
		if pid == 111 {
			return ProcessInfo{Exists: false}, nil
		}
		return ProcessInfo{Exists: true, Comm: "claude", StartedAt: at("2026-09-03T09:59:59Z")}, nil
	}
	assessments := Report(records, time.Time{}, probe)
	n, err := Apply(l, assessments)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("Apply reclaimed %d slots, want 1 (only the dead pid)", n)
	}

	inflight, err := l.InFlight()
	if err != nil {
		t.Fatal(err)
	}
	if len(inflight) != 2 {
		t.Fatalf("expected 2 still in flight (live pid, no-pid record), got %d: %+v", len(inflight), inflight)
	}
	for _, r := range inflight {
		if r.ID == "dead" {
			t.Fatalf("the dead turn should have been reclaimed out of InFlight, still present: %+v", r)
		}
	}

	cur, err := l.Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range cur {
		if r.ID == "dead" {
			if r.State != ledger.Failed {
				t.Fatalf("reclaimed record must be terminal (Failed), got %s", r.State)
			}
			if r.Note == "" {
				t.Fatalf("reclaimed record must carry a reason in its note")
			}
		}
	}
}

func TestApplyIsIdempotentOnAlreadyTerminalAssessments(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := Apply(l, []Assessment{{Record: ledger.Record{ID: "a"}, Reclaimable: false, Reason: "alive"}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("Apply must not write anything for a non-reclaimable assessment, wrote %d", n)
	}
	cur, err := l.Current()
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 0 {
		t.Fatalf("expected an untouched ledger, got %+v", cur)
	}
}
