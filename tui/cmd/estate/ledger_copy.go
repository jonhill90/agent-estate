package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// ledgerSource returns a path a caller may read a ledger.sqlite3 from.
// board.go's buildBoardFetch/buildTaskFetch call this on EVERY fetch, not
// once at startup -- that is what makes "refresh the copy on r" true for
// free: board's own "r" key (model.go's refreshMsg/doFetch) already
// re-invokes the whole board.Fetcher closure, which calls this again.
type ledgerSource func() (string, error)

// staticLedgerSource is an explicit -ledger/$AGENT_TUI_LEDGER value the
// user chose themselves -- already documented as required to be a copy
// (see -ledger's flag help), so this package makes no copy of its own; the
// path is simply read as given, unchanged from the pre-agent-tui#49
// behaviour.
func staticLedgerSource(path string) ledgerSource {
	return func() (string, error) { return path, nil }
}

// backupRunner performs a consistent, online copy of a live sqlite3
// database (source) into dest. cmd/estate supplies execBackupRunner (real
// sqlite3 ".backup"); tests supply a fixture -- same seam shape as every
// other Runner in this program (board.LedgerRunner, cost.Runner, ...).
type backupRunner func(source, dest string) error

// backupBusyTimeout is how long sqlite3's own busy-handler retries before
// giving up when .backup's read lock momentarily loses a race against the
// live supervisor's writer -- the SECOND half of agent-tui#49's REOPENED
// item 2: .backup alone (below) fixed the WAL-visibility problem, but a
// real live ledger under active writes still hit "database is locked"
// outright without this, because sqlite3's default busy timeout is 0 (fail
// immediately, no retry) -- verified against the actual live ledger during
// PR #50's second review round.
const backupBusyTimeout = "5000"

// execBackupRunner shells sqlite3's own ".backup" dot-command out -- the
// real implementation, and the fix for agent-tui#49's REOPENED item 2: a
// plain file copy (os.ReadFile/`cp`) of a WAL-mode database only ever sees
// ledger.sqlite3, but a WAL-mode database's most recently committed rows
// live in ledger.sqlite3-wal, a sidecar file a byte copy of the main file
// alone never reads -- board's own sqlite3 subprocess then opens that
// stale copy and, worse, its own connection can find the main file locked
// against a concurrent writer ("database is locked", the exact failure PR
// #50's review reproduced). ".backup" is SQLite's own Online Backup API
// wrapped in a CLI dot-command: it pages through the live database's
// in-memory/WAL state safely WHILE a writer still has it open, and
// produces one self-contained destination file with no WAL/shm sidecars
// of its own -- exactly what board.ReadTaskRows (PRAGMA query_only=1, no
// -readonly -- see internal/board/ledger.go) already expects to open.
//
// The sqlite3 CLI accepts multiple SQL/dot-command arguments run in order
// against the SAME connection (`sqlite3 FILENAME SQL...` -- see `sqlite3
// -help`), so the busy_timeout PRAGMA set here genuinely applies to the
// .backup call that follows it in the same process invocation.
func execBackupRunner(sqliteBin string) backupRunner {
	return func(source, dest string) error {
		cmd := exec.Command(sqliteBin, source,
			"PRAGMA busy_timeout="+backupBusyTimeout+";",
			".backup '"+dest+"'",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s .backup: %w: %s", sqliteBin, source, err, out)
		}
		return nil
	}
}

// ledgerCopier auto-discovers and copies the LIVE supervisor ledger
// (agent-tui#49 item 2) rather than requiring a human to run their own `cp`
// first. It never opens source itself for a query -- board.ReadTaskRows
// only ever sees dest, refreshed by Refresh() below -- honoring the same
// "never the live ledger" rule -ledger's flag help states, just automated.
//
// A single copier is shared by BOTH the rail's task fetch (internal/rail's
// own 2s refresh loop, agent-tui#26) and the board's fetch (60s tick or
// [r]) -- main.go wires the same ledgerSource into buildTaskFetch and
// buildBoardFetch deliberately, one ledger read path, not two. Without mu,
// those two independent tea.Cmd goroutines can call Refresh at nearly the
// same moment, and two concurrent `sqlite3 ... .backup dest` subprocesses
// racing to write the SAME dest file is exactly what produced "database is
// locked" during PR #50's second review round -- NOT contention with the
// live supervisor's own writer (a standalone repro loop against the real
// live ledger, with no second estate-side backup running, did not
// reproduce the failure at all; a concurrent second Refresh always did).
// mu serializes Refresh calls so the second caller simply waits for the
// first backup to finish and reads the same freshly-written dest, rather
// than colliding with it.
type ledgerCopier struct {
	mu     sync.Mutex
	source string
	dest   string // one fixed temp path, reused and overwritten on every Refresh
	backup backupRunner
}

// newLedgerCopier stages dest once (a real file on disk, so board's sqlite3
// subprocess always has a stable path to open) and returns a copier ready
// for repeated Refresh calls. sqliteBin is the same -sqlite-bin binary
// board's own reads already use (main.go) -- one sqlite3, not a second one
// this package would have to keep in sync.
func newLedgerCopier(source, sqliteBin string) (*ledgerCopier, error) {
	f, err := os.CreateTemp("", "estate-ledger-*.sqlite3")
	if err != nil {
		return nil, fmt.Errorf("stage ledger copy: %w", err)
	}
	dest := f.Name()
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("stage ledger copy: %w", err)
	}
	return &ledgerCopier{source: source, dest: dest, backup: execBackupRunner(sqliteBin)}, nil
}

// Refresh re-copies source to dest via sqlite3's own ".backup" (see
// execBackupRunner's doc comment for why a plain file copy is wrong here)
// and returns dest. Called fresh on every board fetch (board.go's
// buildBoardFetch/buildTaskFetch), which is what makes "refresh the copy
// on r" true for free: board's own "r" key re-invokes the whole Fetcher
// closure, which calls this again.
func (c *ledgerCopier) Refresh() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.backup(c.source, c.dest); err != nil {
		return "", fmt.Errorf("ledger copy: %w", err)
	}
	return c.dest, nil
}
