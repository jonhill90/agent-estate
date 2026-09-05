package isolate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// squashMerged builds the exact on-disk shape a landed dispatch leaves
// behind, which is the shape agent-estate#1000 is about:
//
//	the turn committed;
//	it pushed its branch to origin and a pull request was opened;
//	the pull request was merged with --squash --delete-branch, so origin
//	  no longer has the branch at all.
//
// From teardown's point of view that is indistinguishable, by git alone,
// from a branch that was never pushed: `git fetch origin <branch>` fails
// either way. Only the forge can tell the two apart, which is what the
// Landed seam is for.
//
// It returns the worktree, with no Landed seam set -- each test supplies its
// own so the direction under test is explicit.
func squashMerged(t *testing.T, id string) *Worktree {
	t.Helper()
	root := repo(t)
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", root, "remote", "add", "origin", bare).CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v: %s", err, out)
	}
	w, err := Create(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, "result.txt"), []byte("the turn's output"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "the turn's work"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = w.Path
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "-C", w.Path, "push", "-q", "origin", "HEAD:"+w.Branch).CombinedOutput(); err != nil {
		t.Fatalf("git push: %v: %s", err, out)
	}
	// The merge collects the work and deletes the branch that carried it.
	if out, err := exec.Command("git", "-C", w.Path, "push", "-q", "origin", "--delete", w.Branch).CombinedOutput(); err != nil {
		t.Fatalf("git push --delete: %v: %s", err, out)
	}
	// Prove the setup really is the ambiguous one: the branch evidence must
	// be unavailable, or the tests below would pass for the wrong reason.
	head, err := w.Head()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.remoteHasCommit(head, w.Branch); err == nil {
		t.Fatal("setup is wrong: origin still answers for the deleted branch, so these tests would not exercise the Landed path at all")
	}
	return w
}

// The direction that fixes the leak. A branch origin no longer has, whose
// work the forge says merged, is collected -- and its worktree goes.
func TestRemoveProceedsWhenTheBranchIsGoneBecauseTheWorkWasSquashMerged(t *testing.T) {
	w := squashMerged(t, "squash-landed")
	head, err := w.Head()
	if err != nil {
		t.Fatal(err)
	}
	asked := ""
	w.Landed = func(commit string) (bool, error) { asked = commit; return true, nil }

	if err := w.Remove(); err != nil {
		t.Fatalf("Remove refused work the forge says landed: %v", err)
	}
	if asked != head {
		t.Fatalf("Landed was asked about %q, not the worktree's own HEAD %q", asked, head)
	}
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Fatalf("Remove reported success but %s is still there", w.Path)
	}
}

// The other direction, and the one that must never regress. Same shape --
// origin cannot vouch for the branch -- but the forge says nothing merged.
// That is an open pull request, a closed-unmerged one, or work that was
// never collected at all, and the worktree is the only copy.
func TestRemoveStillRefusesWhenTheBranchIsGoneAndNothingMerged(t *testing.T) {
	w := squashMerged(t, "squash-not-landed")
	w.Landed = func(string) (bool, error) { return false, nil }

	err := w.Remove()
	if err == nil {
		t.Fatal("Remove destroyed committed work that neither origin nor the forge says was collected")
	}
	if _, serr := os.Stat(filepath.Join(w.Path, "result.txt")); serr != nil {
		t.Fatalf("the refused Remove destroyed the turn's output anyway: %v", serr)
	}
}

// "Could not ask" is not "no", and it is not "yes" either. Both sources
// failing must refuse, and the message must carry BOTH failures -- a
// teardown refusal that names only half of why it could not decide sends
// the next reader to the wrong place.
func TestRemoveRefusesWhenNeitherOriginNorTheForgeCouldBeAsked(t *testing.T) {
	w := squashMerged(t, "squash-unmeasurable")
	w.Landed = func(string) (bool, error) { return false, errors.New("gh: not authenticated") }

	err := w.Remove()
	if err == nil {
		t.Fatal("Remove proceeded on evidence it never obtained")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("the refusal does not name the forge failure that caused it: %v", err)
	}
	if !strings.Contains(err.Error(), "fetch") {
		t.Fatalf("the refusal does not name the branch failure that caused it: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(w.Path, "result.txt")); serr != nil {
		t.Fatalf("the refused Remove destroyed the turn's output anyway: %v", serr)
	}
}

// A DEFINITE "no" from origin -- the branch is right there and simply does
// not contain these commits -- is its own path, distinct from "the branch is
// gone" and from "could not ask". It is the shape of a turn that committed
// and then died before pushing, and its worktree is the only copy of that
// work in existence.
//
// This test exists because a mutation that deleted the !collected refusal
// outright left the whole package green: every other case in the package
// refuses via an ERROR (a failed fetch), so nothing exercised the branch
// where origin answers clearly and the answer is no. Both the pre-#1000
// behaviour (no seam) and the post-#1000 one (a seam that says it did not
// land) must refuse here.
func TestRemoveRefusesWhenOriginPlainlyDoesNotHaveTheCommits(t *testing.T) {
	for _, c := range []struct {
		name   string
		landed Landed
	}{
		{"with no forge seam configured", nil},
		{"with the forge saying nothing merged", func(string) (bool, error) { return false, nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := repo(t)
			bare := t.TempDir()
			if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
				t.Fatalf("git init --bare: %v: %s", err, out)
			}
			if out, err := exec.Command("git", "-C", root, "remote", "add", "origin", bare).CombinedOutput(); err != nil {
				t.Fatalf("git remote add: %v: %s", err, out)
			}
			id := "origin-behind-" + strings.ReplaceAll(c.name, " ", "-")
			w, err := Create(root, id)
			if err != nil {
				t.Fatal(err)
			}
			// One commit that origin gets...
			if err := os.WriteFile(filepath.Join(w.Path, "pushed.txt"), []byte("collected"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "pushed"}, {"push", "-q", "origin", "HEAD:" + w.Branch}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = w.Path
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
			}
			// ...and one it never does. This is the only copy.
			only := filepath.Join(w.Path, "never-pushed.txt")
			if err := os.WriteFile(only, []byte("the work that would be lost"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "not pushed"}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = w.Path
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
			}
			// Confirm the setup really produces a definite no rather than an
			// error, or this test would prove the same thing the others do.
			head, err := w.Head()
			if err != nil {
				t.Fatal(err)
			}
			has, herr := w.remoteHasCommit(head, w.Branch)
			if herr != nil || has {
				t.Fatalf("setup is wrong: wanted a definite no from origin, got has=%v err=%v", has, herr)
			}

			w.Landed = c.landed
			if err := w.Remove(); err == nil {
				t.Fatal("Remove destroyed a commit origin plainly does not have")
			}
			if _, err := os.Stat(only); err != nil {
				t.Fatalf("the refused Remove destroyed the unpushed work anyway: %v", err)
			}
		})
	}
}

// The seam may only ever WIDEN what Remove accepts. It is consulted after
// the branch evidence has failed, never before it, so it can never turn a
// removal into a refusal and can never be the reason a removal happened
// when origin already answered.
func TestTheForgeIsNotAskedWhenOriginAlreadyVouchesForTheCommits(t *testing.T) {
	root := repo(t)
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", root, "remote", "add", "origin", bare).CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v: %s", err, out)
	}
	w, err := Create(root, "origin-vouches")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, "result.txt"), []byte("out"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "work"}, {"push", "-q", "origin", "HEAD:" + w.Branch}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = w.Path
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	asked := false
	w.Landed = func(string) (bool, error) { asked = true; return false, errors.New("must not be reached") }

	if err := w.Remove(); err != nil {
		t.Fatalf("Remove refused work origin plainly has: %v", err)
	}
	if asked {
		t.Fatal("the forge was consulted even though origin's own branch already vouched for the commits -- the seam must only ever be a second chance, never a veto")
	}
}

// A worktree with no seam configured must behave EXACTLY as it did before
// the seam existed: origin's branch is the only evidence accepted, and its
// absence refuses. This is what keeps every caller that has not opted in --
// and every existing test in this package -- meaning what it meant.
func TestWithNoForgeSeamRemoveIsUnchanged(t *testing.T) {
	w := squashMerged(t, "no-seam")
	if w.Landed != nil {
		t.Fatal("setup is wrong: this test is about the seam being absent")
	}
	if err := w.Remove(); err == nil {
		t.Fatal("with no forge seam, a branch origin cannot vouch for must still refuse")
	}
}

// safeCommit is what stops a recorded or malformed "commit" from being
// interpolated into a REST path that addresses something else entirely.
func TestSafeCommitRefusesAnythingThatIsNotAnObjectName(t *testing.T) {
	for _, bad := range []string{
		"",
		"abc",
		"../../repos/someone/else/commits/deadbeefdeadbeef",
		"deadbeef/../../x",
		"deadbeef;rm -rf /",
		"refs/heads/main",
	} {
		if err := safeCommit(bad); err == nil {
			t.Fatalf("safeCommit accepted %q", bad)
		}
	}
	if err := safeCommit("20e5adc2c762f2ff0c5ec04f83a864a26e1332d3"); err != nil {
		t.Fatalf("safeCommit rejected a real object name: %v", err)
	}
}

// GHLanded must refuse rather than answer when it has nothing to ask with.
// The subprocess is never reached in either case, so this runs anywhere.
func TestGHLandedRefusesWithoutAValidQuestionToAsk(t *testing.T) {
	if _, err := GHLanded("owner/name")("not-a-sha"); err == nil {
		t.Fatal("GHLanded accepted a commit that is not an object name")
	}
	if _, err := GHLanded("  ")("20e5adc2c762f2ff0c5ec04f83a864a26e1332d3"); err == nil {
		t.Fatal("GHLanded answered without knowing which repository to ask about")
	}
}
