package main

import (
	"path/filepath"
	"runtime"
)

// tickLogSourceFile is this file's own absolute path, recorded by the Go
// toolchain at compile time via runtime.Caller. It anchors repo-root
// discovery to a location baked into the binary, never to the process's
// working directory -- see resolveTickLogPath's doc comment for why that
// distinction is the whole fix.
var tickLogSourceFile = func() string {
	_, file, _, _ := runtime.Caller(0)
	return file
}()

// repoRootFromSource walks up a fixed, known number of directories from a
// source file that lives at <repoRoot>/src/tui/cmd/estate/<file>.go to
// <repoRoot> itself: <file>.go -> estate (its own dir) -> cmd -> tui -> src
// -> root, five filepath.Dir calls. It is arithmetic on a compile-time-known
// layout, not a search -- see resolveTickLogPath for why a search (walking
// up from cwd, or hunting for a marker file) is the wrong tool for this
// particular path.
func repoRootFromSource(sourceFile string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(sourceFile)))))
}

// resolveTickLogPath resolves the Director's tick log (docs/tick-log.jsonl,
// relative to the repository root) against repoRoot, never against the
// process's current working directory.
//
// THE DEFECT THIS FIXES: launch the TUI from anywhere other than the repo
// root and Home reported the Director as "not running" even when it was --
// the tick log's default path, "docs/tick-log.jsonl", is relative, and
// os.Open resolves a relative path against the process's cwd. Started
// elsewhere, that finds nothing; estatus.Read then (correctly, given what
// it was told) reports Absent, and Home renders that as "the Director is
// not running." A log we could not find is not a Director that is not
// running -- see estatus's own package doc comment ("an instrument that
// cannot see a thing looks exactly like the thing being absent").
//
// WHY NOT "walk up from cwd looking for a marker", the other obvious fix:
// a process started inside some OTHER git checkout that also happens to
// carry a docs/tick-log.jsonl (or whatever marker such a walk matched)
// would silently resolve to that repo's file and report ITS Director as
// this one's. Wrong data is worse than none. repoRoot here instead comes
// from repoRootFromSource(tickLogSourceFile) -- this file's own path,
// fixed at compile time -- which is what "discovered from a path the
// caller supplies" means in practice: the caller is this binary, and what
// it supplies is its own build-time location, never an ambient cwd that
// can point anywhere.
//
// An absolute tickLogFlag (an explicit -estate-tick-log) is honoured
// exactly as given; only the default relative path is resolved against
// repoRoot.
func resolveTickLogPath(repoRoot, tickLogFlag string) string {
	if filepath.IsAbs(tickLogFlag) {
		return tickLogFlag
	}
	return filepath.Join(repoRoot, tickLogFlag)
}
