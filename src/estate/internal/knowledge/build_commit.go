package knowledge

import (
	"bytes"
	"os/exec"
	"strings"
)

// defaultGitRunner shells out to the real git binary -- the one live
// implementation; every test in this package supplies a fake instead
// (Config.RunGit), the same pattern stars.go's defaultGHRunner already
// established for `gh`.
func defaultGitRunner(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
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
