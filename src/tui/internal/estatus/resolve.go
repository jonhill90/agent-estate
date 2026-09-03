package estatus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Resolve turns a possibly-relative path into one that means the same thing
// wherever the app was launched from.
//
// WHY. The tick log's default is "docs/tick-log.jsonl", relative to the
// process's working directory. Launch the TUI from anywhere but the repo
// root and it resolves to nothing, so Home reported the Director as not
// running -- an instrument claiming absence when it is merely looking in the
// wrong place, which is the failure this codebase names as the one it
// produces most.
//
// It went unnoticed because every test runs from its own package directory,
// so nothing ever exercised the path a user actually takes.
//
// Order: an absolute path is returned untouched; a relative one is tried
// against the working directory first (so an explicit --flag from a shell
// still means what the shell means), then against the repository root.
// Nothing is invented: if neither exists the ORIGINAL is returned, so the
// reader reports honestly rather than being handed a path that was guessed.
func Resolve(path string) string {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	return ResolveFrom(path, exeDir)
}

// ResolveFrom is Resolve with the binary's directory supplied, so the
// no-repository case is testable.
func ResolveFrom(path, exeDir string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	root, err := repoRootAt(exeDir)
	if err != nil {
		return path
	}
	candidate := filepath.Join(root, path)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return path
}

// repoRootAt asks git from the BINARY's own directory, and nowhere else.
//
// There is deliberately no working-directory fallback. A council seat built
// the binary outside any repository, ran it inside an unrelated git repo that
// happened to contain docs/tick-log.jsonl, and was shown that repo's
// fabricated content as the Director's record.
//
// Reporting absence when blind is the bug this file fixes. Reporting SOMEONE
// ELSE'S data as yours is worse: it is a fabrication with a timestamp on it,
// and no reader could tell. If the binary is not inside a repository there is
// no "the repo" to resolve against, and guessing which one the user meant is
// the invention this package refuses to make. The caller can always name the
// path explicitly.
func repoRootAt(dir string) (string, error) {
	if dir == "" {
		return "", errNoRepo
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var errNoRepo = fmt.Errorf("estatus: the binary is not inside a repository, so a relative path cannot be resolved")
