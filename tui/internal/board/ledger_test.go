package board

import (
	"errors"
	"testing"
)

func fakeRunner(t *testing.T, wantArgs []string, out string, err error) LedgerRunner {
	return func(args []string) ([]byte, error) {
		if len(args) != len(wantArgs) {
			t.Fatalf("args = %v, want %v", args, wantArgs)
		}
		for i := range args {
			if args[i] != wantArgs[i] {
				t.Fatalf("args[%d] = %q, want %q", i, args[i], wantArgs[i])
			}
		}
		return []byte(out), err
	}
}

func TestReadTaskRowsSetsQueryOnly(t *testing.T) {
	run := fakeRunner(t, []string{"-json", "/tmp/copy.sqlite3", "PRAGMA query_only=1;\n" + tasksQuery}, `[]`, nil)
	rows, err := ReadTaskRows(run, "/tmp/copy.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

func TestReadTaskRowsDecodesAndParsesSourceURL(t *testing.T) {
	out := `[{
		"task_id": "at6-task-board",
		"source_kind": "issue",
		"source_url": "https://github.com/jonhill90/agent-tui/issues/6",
		"source_ref": "6",
		"source_state": "OPEN",
		"source_status": "created",
		"task_status": "running",
		"lane": "agent-tui:2",
		"created_at": 1000,
		"updated_at": 2000,
		"delivered_at": 1500,
		"accepted_at": null,
		"completed_at": null
	}]`
	run := fakeRunner(t, []string{"-json", "x.sqlite3", "PRAGMA query_only=1;\n" + tasksQuery}, out, nil)
	rows, err := ReadTaskRows(run, "x.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.Repo.Owner != "jonhill90" || r.Repo.Name != "agent-tui" || r.Number != "6" {
		t.Errorf("parsed repo/number = %+v/%s", r.Repo, r.Number)
	}
	if r.Lane != "agent-tui:2" || r.TaskStatus != "running" {
		t.Errorf("lane/status = %q/%q", r.Lane, r.TaskStatus)
	}
	if r.DeliveredAt == nil || *r.DeliveredAt != 1500 {
		t.Errorf("delivered_at = %v", r.DeliveredAt)
	}
	if r.AcceptedAt != nil || r.CompletedAt != nil {
		t.Errorf("expected nil accepted_at/completed_at, got %v/%v", r.AcceptedAt, r.CompletedAt)
	}
}

func TestReadTaskRowsLeavesUnresolvableURLUnparsed(t *testing.T) {
	// The pre-agent-tui#127 fallback shape agent-dotfiles used before it had a real
	// GitHub URL: "issue:<n>@<dir>". sourceURLRE must not match it, and
	// Derive (card_test.go) must skip a row like this rather than crash.
	out := `[{"task_id":"x","source_kind":"issue","source_url":"issue:241@tmp.bax5AOE3RP","source_ref":"241","source_state":"UNKNOWN","source_status":"created","task_status":"complete","lane":"","created_at":1,"updated_at":1,"delivered_at":null,"accepted_at":null,"completed_at":null}]`
	run := fakeRunner(t, []string{"-json", "x.sqlite3", "PRAGMA query_only=1;\n" + tasksQuery}, out, nil)
	rows, err := ReadTaskRows(run, "x.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Repo.Owner != "" {
		t.Errorf("expected unresolved repo, got %+v", rows[0].Repo)
	}
}

func TestReadTaskRowsPropagatesRunnerError(t *testing.T) {
	run := fakeRunner(t, []string{"-json", "x.sqlite3", "PRAGMA query_only=1;\n" + tasksQuery}, "", errors.New("boom"))
	if _, err := ReadTaskRows(run, "x.sqlite3"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestReadLaneSessionsSetsQueryOnly(t *testing.T) {
	run := fakeRunner(t, []string{"-json", "/tmp/copy.sqlite3", "PRAGMA query_only=1;\n" + laneSessionsQuery}, `[]`, nil)
	rows, err := ReadLaneSessions(run, "/tmp/copy.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

// TestReadLaneSessionsDecodes uses the exact shape a live query returns
// (agent-supervisor:4|claude|014b3e7e-7944-4e5a-be0c-dddce37edbb0, read
// from this box's own ledger.sqlite3 copy, 2026-08-22).
func TestReadLaneSessionsDecodes(t *testing.T) {
	out := `[{"lane":"agent-supervisor:4","harness_session_id":"014b3e7e-7944-4e5a-be0c-dddce37edbb0"}]`
	run := fakeRunner(t, []string{"-json", "x.sqlite3", "PRAGMA query_only=1;\n" + laneSessionsQuery}, out, nil)
	rows, err := ReadLaneSessions(run, "x.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Lane != "agent-supervisor:4" || rows[0].HarnessSessionID != "014b3e7e-7944-4e5a-be0c-dddce37edbb0" {
		t.Fatalf("got %+v", rows)
	}
}

func TestReadLaneSessionsPropagatesRunnerError(t *testing.T) {
	run := fakeRunner(t, []string{"-json", "x.sqlite3", "PRAGMA query_only=1;\n" + laneSessionsQuery}, "", errors.New("boom"))
	if _, err := ReadLaneSessions(run, "x.sqlite3"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDiscoverReposDedupesAndSkipsUnresolved(t *testing.T) {
	rows := []TaskRow{
		{Repo: Repo{Owner: "jonhill90", Name: "agent-tui"}},
		{Repo: Repo{Owner: "jonhill90", Name: "agent-tui"}},
		{Repo: Repo{Owner: "jonhill90", Name: "skills"}},
		{Repo: Repo{}}, // unresolved -- must be skipped
	}
	got := DiscoverRepos(rows)
	if len(got) != 2 {
		t.Fatalf("got %+v, want 2 repos", got)
	}
}
