package ledger

import (
	"os"
	"path/filepath"
	"testing"
)

// schema mirrors the live ledger's constraints that actually bit us:
// tasks.lane REFERENCES lanes(lane), and the status CHECK enum.
const schema = `
CREATE TABLE lanes (lane TEXT PRIMARY KEY, pane_id TEXT NOT NULL, nonce TEXT NOT NULL,
 harness TEXT NOT NULL CHECK (harness IN ('codex','claude','copilot','copilot-acp','pi')),
 repo TEXT NOT NULL, server_id TEXT NOT NULL, session_id TEXT NOT NULL, command TEXT NOT NULL,
 harness_session_id TEXT DEFAULT '', harness_project_dir TEXT DEFAULT '',
 transport TEXT NOT NULL DEFAULT 'send-keys' CHECK (transport IN ('send-keys','acp','pi-rpc','claude-print')),
 updated_at INTEGER NOT NULL);
CREATE TABLE tasks (id TEXT PRIMARY KEY, lane TEXT NOT NULL REFERENCES lanes(lane),
 pane_nonce TEXT NOT NULL, summary TEXT NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('created','delivery_pending','delivered','accepted','running','complete','failed','cancelled')),
 result_path TEXT, result_sha256 TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 delivery_attempted_at INTEGER, delivered_at INTEGER, accepted_at INTEGER, completed_at INTEGER,
 worktree_path TEXT NOT NULL DEFAULT '');`

func newDB(t *testing.T) *DB {
	t.Helper()
	p := filepath.Join(t.TempDir(), "l.sqlite3")
	l, err := Open(p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := l.db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { l.Close(); os.Remove(p) })
	return l
}

// The bug the first REAL dispatch hit: tasks.lane is a foreign key and the
// daemon's lane was never registered. This pins the fix.
func TestCreateRequiresLane(t *testing.T) {
	l := newDB(t)
	if err := l.Create(Task{ID: "t1", Lane: "daemon"}); err == nil {
		t.Fatal("want FK failure creating a task for an unregistered lane, got nil")
	}
	if err := l.EnsureLane("daemon", "/tmp", "claude"); err != nil {
		t.Fatalf("EnsureLane: %v", err)
	}
	if err := l.Create(Task{ID: "t1", Lane: "daemon"}); err != nil {
		t.Fatalf("Create after EnsureLane: %v", err)
	}
}

func TestEnsureLaneIsIdempotent(t *testing.T) {
	l := newDB(t)
	for i := 0; i < 3; i++ {
		if err := l.EnsureLane("daemon", "/tmp", "claude"); err != nil {
			t.Fatalf("EnsureLane #%d: %v", i, err)
		}
	}
}

// The bug found by running a real Codex dispatch end-to-end, not by
// inspection: EnsureLane used to hardcode harness='claude' regardless of
// which adapter actually ran. This pins the fix -- a lane dispatched with
// harness="codex" must record 'codex', not 'claude', in lanes.harness.
func TestEnsureLaneRecordsTheHarnessThatRan(t *testing.T) {
	l := newDB(t)
	if err := l.EnsureLane("codexlane", "/tmp", "codex"); err != nil {
		t.Fatalf("EnsureLane: %v", err)
	}
	var got string
	if err := l.db.QueryRow(`SELECT harness FROM lanes WHERE lane = ?`, "codexlane").Scan(&got); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got != "codex" {
		t.Fatalf("lanes.harness = %q, want %q", got, "codex")
	}
}

// An existing lane re-dispatched with a DIFFERENT harness must have its
// harness column updated, not left stale from the first dispatch --
// EnsureLane's ON CONFLICT clause has to include harness in its UPDATE SET,
// not just updated_at.
func TestEnsureLaneUpdatesHarnessOnReDispatch(t *testing.T) {
	l := newDB(t)
	if err := l.EnsureLane("switcher", "/tmp", "claude"); err != nil {
		t.Fatalf("first EnsureLane: %v", err)
	}
	if err := l.EnsureLane("switcher", "/tmp", "codex"); err != nil {
		t.Fatalf("second EnsureLane: %v", err)
	}
	var got string
	if err := l.db.QueryRow(`SELECT harness FROM lanes WHERE lane = ?`, "switcher").Scan(&got); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got != "codex" {
		t.Fatalf("lanes.harness = %q after re-dispatch, want %q", got, "codex")
	}
}

// An empty harness argument (an older caller, or a Job that never set one)
// must fall back to 'claude', the pre-existing default -- not an empty
// string the CHECK constraint would reject outright.
func TestEnsureLaneDefaultsEmptyHarnessToClaude(t *testing.T) {
	l := newDB(t)
	if err := l.EnsureLane("defaulted", "/tmp", ""); err != nil {
		t.Fatalf("EnsureLane: %v", err)
	}
	var got string
	if err := l.db.QueryRow(`SELECT harness FROM lanes WHERE lane = ?`, "defaulted").Scan(&got); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got != "claude" {
		t.Fatalf("lanes.harness = %q, want default %q", got, "claude")
	}
}

// agent-supervisor#488: a reconciler stamped `failed` on a task whose process
// was still running, over work that had already resolved. A terminal state
// must be write-once. Mutation check: delete the guard in Finish and this
// test goes red.
func TestFinishRefusesToRestampTerminal(t *testing.T) {
	l := newDB(t)
	if err := l.EnsureLane("daemon", "/tmp", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := l.Create(Task{ID: "t1", Lane: "daemon"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Finish("t1", true); err != nil {
		t.Fatalf("first Finish: %v", err)
	}
	err := l.Finish("t1", false)
	if err == nil {
		t.Fatal("want refusal restamping a terminal task, got nil -- #488 regression")
	}
	got, _ := l.Task("t1")
	if got.Status != StatusComplete {
		t.Fatalf("status must survive the refused restamp, got %s", got.Status)
	}
}

func TestTaskNotFoundIsAnError(t *testing.T) {
	l := newDB(t)
	if _, err := l.Task("nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
