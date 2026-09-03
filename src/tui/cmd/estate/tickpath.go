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
//
// This is baked in at build time, not read fresh at run time: a binary that
// outlives the checkout it was built from (the checkout later renamed or
// deleted) computes a repoRoot that no longer exists on disk. That degrades
// to Absent ("Director: not running") the same way a wrong launch cwd used
// to -- see resolveTickLogPath's doc comment for why that's the accepted,
// but silent, tradeoff, and rebuild after any checkout rename or move.
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
//
// TWO KNOWN, ACCEPTED DEGRADATIONS, both landing on Absent ("Director: no
// tick log ... not running") rather than a confidently wrong read:
//
//   - repoRoot itself not absolute. runtime.Caller(0)'s file path is normally
//     absolute, but under `go build -trimpath` it is the module-relative
//     path instead (nothing in this repo passes -trimpath today -- this is
//     dormant, not live; see repoRootFromSource's own doc comment). Joining
//     a relative repoRoot with a relative tickLogFlag would produce a
//     relative result that os.Open then resolves against the process's
//     cwd -- reintroducing the exact cwd-dependent bug this file exists to
//     fix, one level down, and silently, contradicting "never from
//     os.Getwd()" above. Guarded below: a non-absolute repoRoot is refused
//     rather than joined, and the caller sees Absent, never a cwd-relative
//     guess.
//   - the checkout has moved. A binary keeps repoRoot baked in from its own
//     build; if the checkout it was built from is later renamed or deleted,
//     repoRoot names a directory that no longer exists. os.Open on the
//     resolved (still-absolute, still correctly-computed) path then returns
//     fs.ErrNotExist, and estatus.Read reports Absent -- the same "Director:
//     not running" text this PR's whole fix is about. This is the honest
//     choice between two bad options, but it is still silent: rebuild after
//     any checkout rename or move to clear it.
func resolveTickLogPath(repoRoot, tickLogFlag string) string {
	if filepath.IsAbs(tickLogFlag) {
		return tickLogFlag
	}
	if !filepath.IsAbs(repoRoot) {
		// repoRoot is not absolute -- see the trimpath degradation above.
		// Refuse to guess: return a path guaranteed not to exist so
		// estatus.Read reports Absent (an honest "we don't know") instead of
		// silently resolving a repoRoot-relative join against whatever the
		// process's cwd happens to be.
		return filepath.Join(string(filepath.Separator), "estate-repo-root-not-absolute", tickLogFlag)
	}
	return filepath.Join(repoRoot, tickLogFlag)
}
