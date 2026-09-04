package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// defaultCorpusPath is where the operator's own corpus lives --
// src/estate/internal/corpus.Path()'s own default, verified against that
// package 2026-09-04 (agent-estate#1088). Unlike defaultLedgerLivePath
// (board.go), this is not the supervisor's install layout: the corpus is
// deliberately NOT under ~/.local/state ("knowledge, not scratch space the
// harness reuses" -- internal/corpus's own package doc comment). An unset
// $HOME makes this undiscoverable, same treatment as defaultLedgerLivePath:
// callers read "" as "nothing found," not an error.
func defaultCorpusPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "corpus", "ledger.sqlite3")
}

// corpusReadOnlyURI wraps path in the file:...?mode=ro URI form sqlite3's
// CLI accepts in place of a bare path. src/estate/internal/corpus.Hard's own
// doc comment records why this form, not `-readonly`: "the bare -readonly
// flag has been observed failing with 'unable to open database file (14)'
// under WAL contention while file:...?mode=ro succeeded on the same file
// seconds later" -- the SAME growing, actively-written database this
// function points the library pane's second Source at, so the same failure
// mode applies here. immutable=1 tells SQLite this connection alone may
// assume the file will not change for its own lifetime (a single `sqlite3
// -json ... SELECT ...` subprocess, not a long-lived handle) -- it relaxes
// locking without requiring a .backup copy the way ledgerCopier's shared-
// ledger path needs (ledger_copy.go's own doc comment explains why THAT
// database needs a copy: board's reads there use a plain path plus `PRAGMA
// query_only=1`, not this URI form, and predate this file).
func corpusReadOnlyURI(path string) string {
	return "file:" + path + "?mode=ro&immutable=1"
}

// resolveCorpusSource turns -corpus-ledger/$AGENT_TUI_CORPUS_LEDGER
// (explicit, possibly empty) into a ledgerSource for the operator's own
// corpus, mirroring resolveLedgerSource's shape (board.go) but for a
// database this file never copies -- see corpusReadOnlyURI's doc comment
// for why mode=ro+immutable is sufficient here without ledgerCopier's
// .backup step. An explicit path is trusted as given (wrapped in the same
// read-only URI); an empty explicit path falls through to
// defaultCorpusPath's auto-discovery. Returns ok == false, with a message
// naming the path looked for, only when nothing could be found -- the
// library pane's operator Source then renders itself "not configured"
// (library.unconfiguredMessage) rather than erroring the whole program,
// unlike -board's boardOK check: no flag makes the operator corpus
// mandatory to start.
func resolveCorpusSource(explicit string) (ledger ledgerSource, ok bool, unavailable string) {
	path := explicit
	if path == "" {
		path = defaultCorpusPath()
	}
	if path == "" {
		return nil, false, "no -corpus-ledger (or $AGENT_TUI_CORPUS_LEDGER) configured, and $HOME is unset so the operator's corpus could not be discovered"
	}
	if _, err := os.Stat(path); err != nil {
		return nil, false, fmt.Sprintf("no operator corpus found at %s -- point -corpus-ledger at it explicitly", path)
	}
	return staticLedgerSource(corpusReadOnlyURI(path)), true, ""
}
