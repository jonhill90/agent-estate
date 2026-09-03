// Package verifybranch builds and tests a branch in a throwaway worktree,
// rather than in whatever checkout the caller happens to have open.
//
// WHY. Four times in one session, work was pushed that was correct in the
// author's working tree and wrong on the branch: three documentation claims
// naming files and commands that existed only elsewhere, then a main.go
// importing two packages that live on other branches. Reviewers caught the
// first three by hand; CI caught the fourth.
//
// The root cause is not carelessness that more care would fix. A working tree
// accumulates state from every branch worked on, and every check run against
// it answers a question about THAT TREE, not about the branch. The only
// reliable fix is to check somewhere the accumulated state does not exist.
//
// This is deliberately not a git wrapper: it does one thing, in a directory it
// creates, and it reports exactly what it ran and what came back.
package verifybranch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Step is one command that ran, and what it said.
type Step struct {
	Module string
	Cmd    string
	Output string
	Err    error
}

// Result is what a verification found.
type Result struct {
	Branch string
	// Worktree is where it ran. It is kept on failure so the caller can look,
	// and removed when everything passed.
	Worktree string
	Steps    []Step
	// Failed names the first step that failed; empty when all passed.
	Failed string
}

// OK reports whether every step passed.
func (r Result) OK() bool { return r.Failed == "" }

// Verify materialises branch in a fresh worktree and runs build, vet and test
// for each named module there.
//
// It never runs in repoRoot. A check that runs in the shared checkout is
// exactly the check that keeps passing while the branch is broken.
func Verify(repoRoot, branch string, modules []string) (Result, error) {
	res := Result{Branch: branch}
	if strings.TrimSpace(branch) == "" {
		return res, fmt.Errorf("verifybranch: no branch named")
	}
	if len(modules) == 0 {
		return res, fmt.Errorf("verifybranch: no modules to check -- refusing to report success for having looked at nothing")
	}
	base, err := os.MkdirTemp("", "estate-verify-")
	if err != nil {
		return res, fmt.Errorf("verifybranch: cannot make a worktree: %w", err)
	}
	dir := filepath.Join(base, "wt")
	res.Worktree = dir

	if out, err := run(repoRoot, "git", "worktree", "add", "--detach", dir, branch); err != nil {
		os.RemoveAll(base)
		return res, fmt.Errorf("verifybranch: cannot check out %s: %w: %s", branch, err, strings.TrimSpace(out))
	}

	for _, mod := range modules {
		modDir := filepath.Join(dir, mod)
		if _, err := os.Stat(modDir); err != nil {
			// A module named but absent on this branch is a real answer, not
			// something to quietly skip.
			res.Steps = append(res.Steps, Step{Module: mod, Cmd: "stat", Output: err.Error(), Err: err})
			res.Failed = mod + " is not present on this branch"
			cleanup(repoRoot, dir, base)
			res.Worktree = ""
			return res, nil
		}
		for _, c := range [][]string{
			{"go", "build", "./..."},
			{"go", "vet", "./..."},
			{"go", "test", "./...", "-count=1"},
		} {
			out, err := run(modDir, c[0], c[1:]...)
			res.Steps = append(res.Steps, Step{Module: mod, Cmd: strings.Join(c, " "), Output: out, Err: err})
			if err != nil {
				res.Failed = mod + ": " + strings.Join(c, " ")
				// The failing OUTPUT is the evidence and is captured above.
				// Keeping the tree as well leaked one worktree per failed
				// run, registered in .git/worktrees with nothing reaping it.
				// An unbounded leak is worse than losing a directory whose
				// contents are just the branch.
				cleanup(repoRoot, dir, base)
				res.Worktree = ""
				return res, nil
			}
		}
	}

	cleanup(repoRoot, dir, base)
	res.Worktree = ""
	return res, nil
}

// cleanup removes the worktree and prunes registrations left behind by any
// earlier run that died before cleaning up after itself.
func cleanup(repoRoot, dir, base string) {
	run(repoRoot, "git", "worktree", "remove", "--force", dir)
	os.RemoveAll(base)
	run(repoRoot, "git", "worktree", "prune")
}

func run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
