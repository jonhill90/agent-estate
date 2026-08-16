package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jonhill90/keelson/internal/board"
)

func TestLedgerCopier_RefreshCallsBackupWithSourceAndDest(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "ledger.sqlite3")
	if err := os.WriteFile(source, []byte("stand-in"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := newLedgerCopier(source, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	var gotSource, gotDest string
	c.backup = func(src, dst string) error {
		gotSource, gotDest = src, dst
		return nil
	}

	dest, err := c.Refresh()
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if gotSource != source {
		t.Errorf("backup source = %q, want %q", gotSource, source)
	}
	if gotDest != dest || dest == "" {
		t.Errorf("backup dest = %q, Refresh() returned %q", gotDest, dest)
	}
}

// TestLedgerCopier_RefreshTwiceCallsBackupAgain proves "refresh the copy on
// r" (the brief's own phrase): a second Refresh call must re-invoke backup,
// not serve a cached result -- board's own "r" key re-invokes the whole
// Fetcher closure, which calls Refresh again.
func TestLedgerCopier_RefreshTwiceCallsBackupAgain(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "ledger.sqlite3")
	if err := os.WriteFile(source, []byte("stand-in"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := newLedgerCopier(source, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	c.backup = func(src, dst string) error {
		calls++
		return nil
	}

	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("backup called %d times across two Refresh calls, want 2", calls)
	}
}

func TestLedgerCopier_RefreshPropagatesBackupError(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "ledger.sqlite3")
	if err := os.WriteFile(source, []byte("stand-in"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := newLedgerCopier(source, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("database is locked")
	c.backup = func(src, dst string) error { return want }

	if _, err := c.Refresh(); err == nil {
		t.Error("got nil error, want one -- a failed backup must never silently produce an empty/stale copy")
	}
}

// TestLedgerCopier_ConcurrentRefreshDoesNotRace reproduces the ACTUAL root
// cause behind PR #50's second review round: rail's own task fetch
// (internal/rail's 2s refresh loop, agent-tui#26) and board's fetch both
// call Refresh() on the SAME shared copier (main.go wires one
// ledgerSource into both). Two concurrent `sqlite3 ... .backup dest`
// subprocesses racing to write the same dest file -- not contention with
// the live supervisor's own writer -- is what actually produced "database
// is locked": a standalone repro loop against the real live ledger with no
// second Refresh in flight never reproduced it, but firing Refresh from
// many goroutines at once always did before ledgerCopier.mu existed. This
// fires backup from many goroutines at once against a fake backupRunner
// that fails if it detects itself running concurrently with another call,
// proving mu actually serializes them.
func TestLedgerCopier_ConcurrentRefreshDoesNotRace(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "ledger.sqlite3")
	if err := os.WriteFile(source, []byte("stand-in"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := newLedgerCopier(source, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	inFlight := 0
	sawOverlap := false
	c.backup = func(src, dst string) error {
		mu.Lock()
		inFlight++
		if inFlight > 1 {
			sawOverlap = true
		}
		mu.Unlock()

		time.Sleep(5 * time.Millisecond) // long enough for a real race to show up

		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Refresh(); err != nil {
				t.Errorf("Refresh: %v", err)
			}
		}()
	}
	wg.Wait()

	if sawOverlap {
		t.Error("two Refresh calls ran backup concurrently -- ledgerCopier.mu did not serialize them")
	}
}

// TestExecBackupRunner_SucceedsAgainstRealSqlite3 is the baseline
// execBackupRunner test: a real sqlite3 binary, a real (non-contended)
// source database, .backup producing a real dest file. The contention case
// this function's own doc comment describes ("database is locked" under a
// live writer, verified during PR #50's second review round against the
// real live ledger) is not reproduced here -- simulating genuine lock
// contention from a single test process is its own can of worms; this test
// only guards that the PRAGMA argument added for that fix didn't break the
// ordinary, uncontended path.
func TestExecBackupRunner_SucceedsAgainstRealSqlite3(t *testing.T) {
	sqlitePath, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 binary not on PATH")
	}
	run := execBackupRunner(sqlitePath)

	dir := t.TempDir()
	source := filepath.Join(dir, "source.sqlite3")
	if out, err := exec.Command(sqlitePath, source, "CREATE TABLE t (v TEXT);").CombinedOutput(); err != nil {
		t.Fatalf("fixture setup: %v: %s", err, out)
	}
	dest := filepath.Join(dir, "dest.sqlite3")

	if err := run(source, dest); err != nil {
		t.Fatalf("execBackupRunner: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("dest was not created: %v", err)
	}
}

// TestLedgerCopier_RefreshAgainstRealWALSource is the review's own
// reproduction: a real WAL-mode sqlite3 database whose most recent row
// sits ONLY in the -wal sidecar (deliberately not checkpointed here), the
// exact condition the old plain-file-copy implementation missed --
// producing either a stale copy (row silently absent) or a "database is
// locked" error, both reproduced against PR #50's review. execBackupRunner
// (sqlite3's own ".backup") must read source and -wal together and produce
// one self-contained copy with the row included.
func TestLedgerCopier_RefreshAgainstRealWALSource(t *testing.T) {
	sqlitePath, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 binary not on PATH")
	}

	dir := t.TempDir()
	source := filepath.Join(dir, "orig.sqlite3")
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
  't1', 'issue', 'https://github.com/jonhill90/agent-tui/issues/49', '49', 'OPEN', 'created'
);
INSERT INTO tasks VALUES (
  't1', 'running', 'agent-tui:1', 1000, 2000, NULL, NULL, NULL
);
`
	// Deliberately NO wal_checkpoint -- the row above must still be sitting
	// only in orig.sqlite3-wal when Refresh() runs.
	if out, err := exec.Command(sqlitePath, source, setup).CombinedOutput(); err != nil {
		t.Fatalf("setting up WAL fixture db: %v: %s", err, out)
	}
	if _, err := os.Stat(source + "-wal"); err != nil {
		t.Skip("fixture db did not produce a -wal sidecar on this sqlite3 build")
	}

	c, err := newLedgerCopier(source, sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := c.Refresh()
	if err != nil {
		t.Fatalf("Refresh against a real WAL source: %v", err)
	}

	rows, err := board.ReadTaskRows(board.ExecRunner(sqlitePath), dest)
	if err != nil {
		t.Fatalf("ReadTaskRows against the .backup copy: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 -- .backup must include WAL-only data a plain file copy would miss", len(rows))
	}
	if rows[0].Number != "49" || rows[0].TaskStatus != "running" {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestResolveLedgerSource_ExplicitPathIsTrustedAsIs(t *testing.T) {
	src, ok, unavailable := resolveLedgerSource("/some/explicit/copy.sqlite3", "sqlite3")
	if !ok || unavailable != "" {
		t.Fatalf("got ok=%v unavailable=%q, want ok with no message", ok, unavailable)
	}
	path, err := src()
	if err != nil || path != "/some/explicit/copy.sqlite3" {
		t.Errorf("got %q, %v, want the explicit path back verbatim", path, err)
	}
}

// TestResolveLedgerSource_NoLiveLedgerIsUnavailable is the pre-agent-tui#49
// behaviour that must still hold when even the live path doesn't exist --
// boardOK stays false with a message, never a crash or a silent read of
// nothing.
func TestResolveLedgerSource_NoLiveLedgerIsUnavailable(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no ~/.local/state/agent-dotfiles-supervisor here
	_, ok, unavailable := resolveLedgerSource("", "sqlite3")
	if ok {
		t.Fatal("got ok=true, want false -- no live ledger exists in this HOME")
	}
	if unavailable == "" {
		t.Error("got empty unavailable message, want one naming the path looked for")
	}
}

// TestResolveLedgerSource_AutoDiscoversAndCopiesLiveLedger reproduces
// agent-tui#49 item 2's actual fix: f2 with NO configuration must show a
// populated board. This stands up a fake live ledger (a real, minimal
// sqlite3 database -- .backup refuses anything else) at the exact path
// resolveLedgerSource looks for and proves it gets copied via .backup, not
// opened directly.
func TestResolveLedgerSource_AutoDiscoversAndCopiesLiveLedger(t *testing.T) {
	sqlitePath, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 binary not on PATH")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	live := filepath.Join(home, ".local", "state", "agent-dotfiles-supervisor", "ledger.sqlite3")
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		t.Fatal(err)
	}
	setup := `CREATE TABLE marker (v TEXT); INSERT INTO marker VALUES ('live bytes');`
	if out, err := exec.Command(sqlitePath, live, setup).CombinedOutput(); err != nil {
		t.Fatalf("setting up fixture live db: %v: %s", err, out)
	}

	src, ok, unavailable := resolveLedgerSource("", sqlitePath)
	if !ok || unavailable != "" {
		t.Fatalf("got ok=%v unavailable=%q, want ok with no message", ok, unavailable)
	}
	path, err := src()
	if err != nil {
		t.Fatalf("src(): %v", err)
	}
	if path == live {
		t.Fatalf("got the live path itself (%s) -- must be a copy, never the live ledger", path)
	}
	out, err := exec.Command(sqlitePath, path, "SELECT v FROM marker;").Output()
	if err != nil {
		t.Fatalf("querying the copy: %v", err)
	}
	if got := string(out); got != "live bytes\n" {
		t.Errorf("got %q, want the live ledger's row copied over", got)
	}
}
