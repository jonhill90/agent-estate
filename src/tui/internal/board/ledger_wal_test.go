package board

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestReadTaskRowsAgainstRealWALCopy is the test the review demanded: it
// drives ReadTaskRows through a REAL sqlite3 binary against a REAL
// WAL-mode database file, reproducing the exact failure mode the review
// found -- `-readonly` against a plain `cp` of a WAL ledger (no `-wal`/
// `-shm` sidecars) errors with "unable to open database file (14)" before
// a single row is read. The fixture-backed tests in ledger_test.go only
// assert on the argv this package builds; they cannot fail on this because
// they never invoke sqlite3 at all. This test must fail red if `-readonly`
// comes back (mutation-check 1 in the PR reply).
func TestReadTaskRowsAgainstRealWALCopy(t *testing.T) {
	sqlitePath, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 binary not on PATH")
	}

	dir := t.TempDir()
	orig := filepath.Join(dir, "orig.sqlite3")

	setup := `
PRAGMA journal_mode=WAL;
CREATE TABLE source_tasks (
  id TEXT PRIMARY KEY,
  source_kind TEXT,
  source_url TEXT,
  source_ref TEXT,
  source_state TEXT,
  status TEXT
);
CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  status TEXT,
  lane TEXT,
  created_at INTEGER,
  updated_at INTEGER,
  delivered_at INTEGER,
  accepted_at INTEGER,
  completed_at INTEGER
);
INSERT INTO source_tasks VALUES (
  't1', 'issue', 'https://github.com/jonhill90/agent-tui/issues/6', '6', 'OPEN', 'created'
);
INSERT INTO tasks VALUES (
  't1', 'running', 'agent-tui:2', 1000, 2000, NULL, NULL, NULL
);
PRAGMA wal_checkpoint(TRUNCATE);
`
	if out, err := exec.Command(sqlitePath, orig, setup).CombinedOutput(); err != nil {
		t.Fatalf("setting up WAL fixture db: %v: %s", err, out)
	}
	if journalMode(t, sqlitePath, orig) != "wal" {
		t.Fatalf("fixture db did not come up in WAL mode")
	}

	// The exact reproduction from the review: a plain `cp`, no -wal/-shm
	// sidecars, of a WAL-mode database file.
	copyPath := filepath.Join(dir, "fresh-copy.sqlite3")
	origBytes, err := os.ReadFile(orig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, origBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(copyPath + "-wal"); err == nil {
		t.Fatal("test setup produced a -wal sidecar; this must reproduce a sidecar-less plain copy")
	}
	if _, err := os.Stat(copyPath + "-shm"); err == nil {
		t.Fatal("test setup produced a -shm sidecar; this must reproduce a sidecar-less plain copy")
	}

	run := ExecRunner(sqlitePath)
	rows, err := ReadTaskRows(run, copyPath)
	if err != nil {
		t.Fatalf("ReadTaskRows against a real WAL copy: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Repo.Owner != "jonhill90" || rows[0].Repo.Name != "agent-tui" || rows[0].Number != "6" {
		t.Errorf("parsed repo/number = %+v/%s", rows[0].Repo, rows[0].Number)
	}
	if rows[0].Lane != "agent-tui:2" || rows[0].TaskStatus != "running" {
		t.Errorf("lane/status = %q/%q", rows[0].Lane, rows[0].TaskStatus)
	}

	// The write guard this test exists to protect: even against a real
	// sqlite3 binary, ReadTaskRows must never leave the copy modified in a
	// way that would matter if this were the live ledger -- query_only
	// must have actually been in force. If it were not, the CLI would be
	// free to accept writes; we can't issue one through this API (it only
	// ever runs SELECT), so the guarantee under test is that the pragma
	// was sent ahead of the query at all, which the argv-level fixture
	// tests in ledger_test.go pin down precisely.
}

// journalMode shells `PRAGMA journal_mode;` directly (not through
// ReadTaskRows) to confirm the fixture db actually is WAL-mode before the
// real assertion runs -- if setup silently fell back to a different
// journal mode this test would pass for the wrong reason.
func journalMode(t *testing.T, sqlitePath, dbPath string) string {
	t.Helper()
	out, err := exec.Command(sqlitePath, dbPath, "PRAGMA journal_mode;").Output()
	if err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	mode := string(out)
	for len(mode) > 0 && (mode[len(mode)-1] == '\n' || mode[len(mode)-1] == '\r') {
		mode = mode[:len(mode)-1]
	}
	return mode
}
