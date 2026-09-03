// Package isolate gives one dispatched agent turn its own git worktree.
//
// WHY. `estate dispatch` runs `claude -p --dangerously-skip-permissions`: an
// unattended agent with full permissions and no sandbox. Until this package it
// also ran with no working directory of its own, so it inherited the caller's
// -- the shared checkout that every other piece of this estate reads from.
// That is a full-permission agent writing into the one directory with no
// owner, on a three-minute loop.
//
// The isolation here is a worktree, not a sandbox. It bounds *where the agent's
// git working tree is*, so its edits and its branch cannot land in the shared
// checkout. It does NOT stop a determined process from writing elsewhere on the
// disk -- nothing short of a real sandbox does, and claiming otherwise would be
// worse than the current state because it would be believed. What it buys is
// that the ordinary failure -- an agent editing files where it was started --
// stops touching the checkout everything else depends on.
//
// Every refusal here fails closed: if we cannot establish isolation we return
// an error, and the caller must not dispatch. A dispatch that cannot be
// isolated is exactly the case this package exists to refuse.
package isolate

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktree is one dispatch's isolated checkout.
type Worktree struct {
	// Path is the directory the agent turn should run in.
	Path string
	// Branch is the branch created for it, named for the dispatch.
	Branch string
	// Base is the commit the worktree was created from. Anything the branch
	// points at beyond this is work the turn committed.
	Base string

	root string
}

// safeID reports whether id can name a directory and a branch without
// escaping either. Anything that could traverse out of the worktree root, or
// confuse git's own refname parsing, is refused rather than sanitised --
// silently rewriting an id would make the ledger's id and the worktree's name
// disagree.
func safeID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("dispatch id is empty")
	}
	if id != filepath.Clean(id) || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return fmt.Errorf("dispatch id %q is not a single safe path element", id)
	}
	// "." survives every check above -- Clean(".") is ".", it holds no
	// separator and no "..", and "." is in the character allowlist -- and
	// filepath.Join(root, ".") is the dispatch root itself.
	//
	// This is NOT closing a reachable hole: such an id is already refused
	// downstream, because the dispatch root exists by then and the
	// already-exists check fires. It is refused here so the reason given is
	// the true one. A guard whose message names the wrong cause sends the
	// next reader to the wrong place, which is its own kind of failure.
	if strings.Trim(id, ".") == "" {
		return fmt.Errorf("dispatch id %q names a directory rather than a worktree", id)
	}
	for _, r := range id {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			return fmt.Errorf("dispatch id %q contains %q, which is not allowed in a worktree or branch name", id, r)
		}
	}
	return nil
}

func git(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Root is where dispatch worktrees are created: outside the repository, never
// inside it. Inside would mean a runaway agent is still writing under the
// shared checkout, which defeats the point.
//
// It is keyed on a digest of the FULL repository path, not its base name. Base
// names are not unique -- two checkouts called "agent-estate" in different
// parents, or two test repositories that Go named "001", would otherwise share
// one dispatch root and refuse each other's ids as already in use. That is not
// hypothetical: it was caught by this package's own tests colliding on "001".
func Root(repoRoot string) string {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		abs = repoRoot
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(os.TempDir(), "estate-dispatch", fmt.Sprintf("%s-%x", filepath.Base(abs), sum[:6]))
}

// Create makes an isolated worktree for the dispatch identified by id.
//
// It refuses -- rather than falling back to the caller's directory -- when the
// root is not a git repository, when the id cannot safely name a directory and
// a branch, or when this dispatch's worktree already exists.
func Create(repoRoot, id string) (*Worktree, error) {
	if err := safeID(id); err != nil {
		return nil, fmt.Errorf("isolate: refusing to dispatch: %w", err)
	}
	if _, err := git(repoRoot, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("isolate: refusing to dispatch: %s is not a git repository, so no worktree can be made for the turn: %w", repoRoot, err)
	}

	base := Root(repoRoot)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("isolate: refusing to dispatch: cannot create worktree root: %w", err)
	}
	path := filepath.Join(base, id)
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("isolate: refusing to dispatch: %s already exists -- another turn may be live there, and sharing it is what isolation is for", path)
	} else if !os.IsNotExist(err) {
		// Could not tell. Never treat that as free.
		return nil, fmt.Errorf("isolate: refusing to dispatch: cannot determine whether %s is in use: %w", path, err)
	}

	branch := "dispatch/" + id
	if _, err := git(repoRoot, "worktree", "add", "-b", branch, path); err != nil {
		return nil, fmt.Errorf("isolate: refusing to dispatch: %w", err)
	}
	// Remember where the branch started, so teardown can tell whether the
	// turn committed anything.
	baseOut, err := git(path, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("isolate: refusing to dispatch: cannot record the worktree's base commit: %w", err)
	}
	return &Worktree{Path: path, Branch: branch, Base: strings.TrimSpace(string(baseOut)), root: repoRoot}, nil
}

// Dirty reports whether the worktree holds uncommitted changes -- output a
// dispatch produced that nothing has collected yet.
//
// --ignored is load-bearing, not defensive. Without it `git status
// --porcelain` omits gitignored paths, so a turn whose only output was a
// *.log, a build artifact, or anything else the repo ignores reported CLEAN
// and Remove deleted it. An independent review found this; the guarantee is
// about whether a human can still recover the turn's output, and a file's
// gitignore status says nothing about whether it was worth keeping.
//
// The cost is real and chosen deliberately: a worktree containing only
// incidental ignored junk (a .DS_Store, a stray build directory) now refuses
// teardown and leaks until something collects it. That direction is
// recoverable and visible. The other direction destroys work and is not.
func (w *Worktree) Dirty() (bool, error) {
	out, err := git(w.Path, "status", "--porcelain", "--ignored")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// Committed reports whether the branch has moved beyond the commit it was
// created at -- that is, whether the turn committed anything.
func (w *Worktree) Committed() (bool, error) {
	if w.Base == "" {
		// We never learned where it started, so we cannot tell what is new.
		// That is "could not measure", and it must not read as "nothing".
		return false, fmt.Errorf("isolate: no base commit recorded for %s", w.Path)
	}
	head, err := git(w.Path, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(head)) != w.Base, nil
}

// Remove tears the worktree and its branch down.
//
// It refuses when the worktree holds uncommitted changes OR when the turn
// committed anything. A council seat found the second case: an agent that
// committed its work left a CLEAN git status, so the uncommitted check saw
// nothing to collect, and `git branch -D` then deleted the only ref to those
// commits. The agent did the tidy thing and lost more for it.
//
// Both refusals are the same rule: a dispatch's uncollected output looks
// exactly like an empty worktree from outside, and deleting it is
// unrecoverable while reporting it is not.
func (w *Worktree) Remove() error {
	committed, cerr := w.Committed()
	if cerr != nil {
		return fmt.Errorf("isolate: cannot tell whether %s holds committed work, so refusing to remove it: %w", w.Path, cerr)
	}
	if committed {
		return fmt.Errorf("isolate: %s has commits on %s that nothing else references; refusing to remove it -- collect them first", w.Path, w.Branch)
	}
	dirty, err := w.Dirty()
	if err != nil {
		return fmt.Errorf("isolate: cannot tell whether %s holds uncommitted work, so refusing to remove it: %w", w.Path, err)
	}
	if dirty {
		return fmt.Errorf("isolate: %s holds uncommitted work; refusing to remove it -- collect or commit it first", w.Path)
	}
	if _, err := git(w.root, "worktree", "remove", w.Path); err != nil {
		return err
	}
	if _, err := git(w.root, "branch", "-D", w.Branch); err != nil {
		return err
	}
	return nil
}
