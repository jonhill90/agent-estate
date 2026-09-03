package verifybranch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo builds a throwaway git repo containing a tiny Go module, on two
// branches: one that compiles and one that does not.
func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")

	mod := filepath.Join(root, "src", "thing")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(mod, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module thing\n\ngo 1.24\n")
	write("thing.go", "package thing\n\nfunc Add(a, b int) int { return a + b }\n")
	write("thing_test.go", "package thing\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	git("add", "-A")
	git("commit", "-qm", "good")

	// A branch importing a package that does not exist here -- the exact
	// shape of the defect this package was written for.
	git("checkout", "-qb", "broken")
	write("thing.go", "package thing\n\nimport \"thing/internal/elsewhere\"\n\nfunc Add(a, b int) int { return elsewhere.Add(a, b) }\n")
	git("add", "-A")
	git("commit", "-qm", "imports a package from another branch")
	git("checkout", "-q", "main")
	return root
}

func TestVerifyPassesOnABranchThatBuilds(t *testing.T) {
	res, err := Verify(repo(t), "main", []string{"src/thing"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("main should pass; failed at %s\n%s", res.Failed, lastOutput(res))
	}
	if len(res.Steps) != 3 {
		t.Errorf("want build, vet and test to have run; got %d steps", len(res.Steps))
	}
	if res.Worktree != "" {
		t.Errorf("a passing verification should clean up its worktree, kept %s", res.Worktree)
	}
}

// The whole point: a branch that is broken must fail HERE, even though the
// caller's own checkout is fine.
func TestVerifyCatchesABranchThatOnlyBuildsInTheAuthorsTree(t *testing.T) {
	root := repo(t)
	// The caller's checkout is on main and builds cleanly...
	if res, err := Verify(root, "main", []string{"src/thing"}); err != nil || !res.OK() {
		t.Fatalf("precondition: main must be clean; %v %v", err, res.Failed)
	}
	// ...and the branch still has to be judged on its own contents.
	res, err := Verify(root, "broken", []string{"src/thing"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() {
		t.Fatal("a branch importing a package that does not exist on it must fail verification")
	}
	if !strings.Contains(lastOutput(res), "elsewhere") {
		t.Errorf("the failure should name the missing import; got:\n%s", lastOutput(res))
	}
	// A failed run must NOT leave a worktree behind: one per failure, with
	// nothing reaping them, is an unbounded leak. The captured output is the
	// evidence.
	if res.Worktree != "" {
		t.Errorf("a failed verification leaked a worktree at %s", res.Worktree)
	}
	out, err := exec.Command("git", "-C", root, "worktree", "list").Output()
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out), "\n"); n > 1 {
		t.Errorf("worktrees left registered after a failed run:\n%s", out)
	}
}

// A module named but absent is a real answer, never a silent pass.
func TestAbsentModuleIsAFailureNotASkip(t *testing.T) {
	res, err := Verify(repo(t), "main", []string{"src/does-not-exist"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() {
		t.Fatal("naming a module that is not on the branch must fail, not pass")
	}
	if res.Worktree != "" {
		t.Errorf("an absent-module failure leaked a worktree at %s", res.Worktree)
	}
}

// Refuse to report success for having checked nothing.
func TestNoModulesIsRefused(t *testing.T) {
	if _, err := Verify(repo(t), "main", nil); err == nil {
		t.Fatal("verifying zero modules must be an error, not a pass")
	}
}

func TestUnknownBranchIsAnError(t *testing.T) {
	if _, err := Verify(repo(t), "no-such-branch", []string{"src/thing"}); err == nil {
		t.Fatal("an unknown branch must be an error")
	}
}

func lastOutput(r Result) string {
	if len(r.Steps) == 0 {
		return ""
	}
	return r.Steps[len(r.Steps)-1].Output
}
