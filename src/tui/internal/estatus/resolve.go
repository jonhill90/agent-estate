package estatus

import (
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
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	root, err := repoRoot()
	if err != nil {
		return path
	}
	candidate := filepath.Join(root, path)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return path
}

// repoRoot asks git, from the executable's own directory rather than the
// caller's -- the binary lives in the tree it reports on, the shell does not.
func repoRoot() (string, error) {
	dir := ""
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		// Fall back to the working directory: a binary copied out of the
		// tree still deserves a chance to find the repo it was pointed at.
		out, err = exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return "", err
		}
	}
	return strings.TrimSpace(string(out)), nil
}
