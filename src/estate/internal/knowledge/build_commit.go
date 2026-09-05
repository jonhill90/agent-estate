package knowledge

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// defaultGitRunner shells out to the real git binary -- the one live
// implementation; every test in this package supplies a fake instead
// (Config.RunGit), the same pattern stars.go's defaultGHRunner already
// established for `gh`.
//
// A non-zero exit status of exactly 1 is translated into the sentinel
// errGitExitOne rather than passed through as a bare *exec.ExitError --
// agent-estate#1191's provenance.go needs to tell `git merge-base
// --is-ancestor`'s own "ran fine, genuinely answered no" (exit 1) apart
// from every other way git can fail to run at all (missing binary, unknown
// commit, not a repository -- all other, non-{0,1} exits), and a fake
// Config.RunGit in a test cannot easily construct a real *exec.ExitError to
// simulate that first case. Any other non-zero exit keeps its original
// error, unexamined by this function -- ResolveBuildCommit's own two
// callers (`status --porcelain`, `rev-parse HEAD`) only ever fail with
// something other than exit 1 in practice, so this reclassification does
// not change what they already do with a non-nil error (treat it as
// unknownCommit either way).
func defaultGitRunner(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, errGitExitOne
		}
		return nil, err
	}
	return out.Bytes(), nil
}

// errGitExitOne is the sentinel defaultGitRunner returns in place of git's
// own exit-1 *exec.ExitError -- see that function's doc comment. A fake
// Config.RunGit in a test returns this value directly to simulate "git ran
// and gave a genuine negative answer", without depending on constructing a
// real subprocess exit code.
var errGitExitOne = errors.New("git: exited 1 (a real negative answer, not a failure to run)")

// errGitNo reports whether err is (or wraps) errGitExitOne -- see that
// variable's own doc comment. Exported to this package only; provenance.go
// is the one caller.
func errGitNo(err error) bool {
	return errors.Is(err, errGitExitOne)
}

// unknownCommit is the one value this package ever writes for a commit it
// could not positively determine -- never a guess, never a zero-value
// empty string that a caller might mistake for "not yet set" rather than
// "checked and could not tell".
const unknownCommit = "unknown"

// ResolveBuildCommit reports the commit of the git checkout at cfg.RepoRoot,
// or unknownCommit when that cannot be positively established -- no repo
// root resolved, git itself unavailable or erroring, or (deliberately) a
// dirty working tree. A dirty tree has no single commit that describes what
// is actually on disk, so this refuses to name HEAD in that case rather
// than asserting a false precision -- agent-estate#1082's own "not a
// guess" requirement, the same rule query.go's CoverageUnknownFreshness
// already follows for github-stars. Exported so main.go can call the exact
// same resolution this package's own Generate uses, to determine the
// CURRENTLY RUNNING checkout's commit for an index-vs-binary comparison at
// query time -- one implementation, never a second copy that could drift.
func ResolveBuildCommit(cfg Config) string {
	run := cfg.RunGit
	if run == nil {
		run = defaultGitRunner
	}
	if cfg.RepoRoot == "" {
		return unknownCommit
	}
	status, err := run("-C", cfg.RepoRoot, "status", "--porcelain")
	if err != nil {
		return unknownCommit
	}
	if len(bytes.TrimSpace(status)) > 0 {
		// Dirty tree -- see doc comment above.
		return unknownCommit
	}
	out, err := run("-C", cfg.RepoRoot, "rev-parse", "HEAD")
	if err != nil {
		return unknownCommit
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return unknownCommit
	}
	return commit
}
