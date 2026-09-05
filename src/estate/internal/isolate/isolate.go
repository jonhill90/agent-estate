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
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Worktree is one dispatch's isolated checkout.
type Worktree struct {
	// Path is the directory the agent turn should run in.
	Path string
	// Branch is the branch created for it, named for the dispatch (Create),
	// or the EXISTING branch it is continuing (CreateOnBranch).
	Branch string
	// Base is the commit the worktree was created from. Anything the branch
	// points at beyond this is work the turn committed.
	Base string
	// Detached reports whether Path's HEAD is a detached checkout rather
	// than a local branch named Branch -- true only for CreateOnBranch.
	// Remove uses this to know whether there is a local branch ref left to
	// delete at teardown.
	Detached bool
	// Landed, when set, is the second way Remove can establish that this
	// worktree's commits have been collected -- see the Landed type and
	// Remove's own doc comment. nil means the question is not asked at all,
	// which leaves Remove behaving exactly as it did before this seam
	// existed: origin's own copy of the branch is then the only evidence
	// accepted.
	Landed Landed

	root string
}

// Landed answers "did this commit's work reach the repository for good?" --
// the question Remove must ask that origin's copy of the dispatch branch
// cannot answer.
//
// WHY THIS EXISTS (agent-estate#1000). remoteHasCommit (below) asks whether
// origin's own tip of the dispatch branch still contains the commit. That is
// a true "collected" signal right up until the pull request merges: this
// estate merges with `gh pr merge --squash --delete-branch`, so the branch
// origin was holding the evidence on is deleted *by the very act of
// collecting the work*. From that moment the fetch fails, remoteHasCommit
// returns "could not measure", Remove refuses, and it refuses forever --
// which is exactly how 176 worktrees accumulated until the host OOM-killed
// itself.
//
// AHEAD-NESS IS NOT THE ANSWER, and neither is any purely-git test. A squash
// merge writes a NEW commit with a new tree-parent lineage; the dispatch
// branch's own commits are never ancestors of main afterwards. Measured on
// merged PR #984: 1 commit ahead of origin/main, `merge-base --is-ancestor`
// against main says NO. Of 63 ahead-of-main worktrees on the dying host, 34
// had a merged pull request. `git cherry`'s patch-id matching does not
// rescue this either: a squash of N>1 commits has no patch-id in common with
// any of them. So the estate has to ask the forge, which is the only party
// that records "this commit is the head of pull request N, and N merged" --
// and records it durably, after the branch is gone. Verified against merged
// PR #996, whose branch origin no longer has:
//
//	gh api repos/jonhill90/agent-estate/commits/20e5adc.../pulls
//	  -> [{"n":996,"merged":"2026-09-03T19:58:39Z"}]
//
// A false "yes" here deletes the only copy of a turn's output, so every
// failure to ask is an error and never a "no" -- same fail-closed contract
// as remoteHasCommit and Committed.
type Landed func(commit string) (bool, error)

// ghTimeout bounds GHLanded's own subprocess, for the same reason
// remoteFetchTimeout bounds the fetch: a network call with no bound turns
// teardown into something that hangs rather than refuses. Same 15s as
// remoteFetchTimeout and internal/cost's execTimeout rather than a fresh
// number. A var, not a const, so a test can shrink it.
var ghTimeout = 15 * time.Second

// safeCommit refuses anything that is not a plain hexadecimal object name.
// GHLanded interpolates the commit into a REST path, so a value carrying
// "/" or ".." would address a different endpoint entirely; and a commit that
// is not a full object name cannot be what Head() returned, which is the
// only thing that should ever reach here.
func safeCommit(commit string) error {
	c := strings.TrimSpace(commit)
	if len(c) < 7 {
		return fmt.Errorf("commit %q is too short to be an object name", commit)
	}
	for _, r := range c {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return fmt.Errorf("commit %q contains %q, which is not hexadecimal", commit, r)
		}
	}
	return nil
}

// GHLanded is the real Landed: it asks GitHub which pull requests contain
// this commit and whether any of them merged. Kept beside the seam it
// implements, the same way internal/reclaim keeps PSProbe beside Probe --
// tests drive the decision logic through a fake, and this is the one place
// that knows the question is answered by a subprocess.
//
// repo is "owner/name". Any failure -- gh missing, unauthenticated, the
// commit unknown to the forge (404, which is what an unpushed commit
// produces), output that does not parse -- is returned as an error, so
// Remove refuses rather than reading "could not ask" as "not landed" or as
// "landed". Only a definite count of merged pull requests answers the
// question.
func GHLanded(repo string) Landed {
	return func(commit string) (bool, error) {
		if err := safeCommit(commit); err != nil {
			return false, err
		}
		if strings.TrimSpace(repo) == "" {
			return false, errors.New("no repository configured, so GitHub cannot be asked whether this commit landed")
		}
		ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "gh", "api",
			"repos/"+repo+"/commits/"+strings.TrimSpace(commit)+"/pulls",
			"--jq", "[.[] | select(.merged_at != null)] | length")
		// Same process-group kill as gitTimeout: gh spawns helpers, and
		// killing only the direct child leaves CombinedOutput's pipe held
		// open by a grandchild, so the "bounded" call is not bounded.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		out, err := cmd.CombinedOutput()
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return false, fmt.Errorf("could not ask GitHub whether %s landed within %s -- giving up waiting is not an answer: %w", commit, ghTimeout, err)
			}
			return false, fmt.Errorf("could not ask GitHub whether %s landed: %w: %s", commit, err, strings.TrimSpace(string(out)))
		}
		n, perr := strconv.Atoi(strings.TrimSpace(string(out)))
		if perr != nil {
			return false, fmt.Errorf("could not read GitHub's answer about %s: %q", commit, strings.TrimSpace(string(out)))
		}
		return n > 0, nil
	}
}

// landed asks the Landed seam, if one is configured. With none configured it
// reports (false, nil) -- "no further evidence", never an error -- so a
// caller that never set the seam gets precisely the behaviour Remove had
// before it existed.
func (w *Worktree) landed(commit string) (bool, error) {
	if w.Landed == nil {
		return false, nil
	}
	return w.Landed(commit)
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

// remoteFetchTimeout bounds remoteHasCommit's own fetch (below). A var, not a
// const, so a test can shrink it rather than wait out the real duration.
//
// 15s matches the convention src/tui's own subprocess seams already use for
// a bounded network call (internal/cost's execTimeout, #994's
// pressureRunner) -- reused rather than invented fresh. This is a fetch of
// ONE branch, not a clone: 15s is generous for that over any link that is
// actually working, and short enough that a black-holed remote (one that
// drops packets rather than refusing the connection -- a stale VPN route, a
// half-dead proxy, a firewall with a DROP rule) no longer leaves Remove
// bounded only by the OS TCP connect timeout, commonly 60-120s or more.
var remoteFetchTimeout = 15 * time.Second

// gitTimeout runs git the same way git (above) does, except the process is
// killed if it outlives ctx. Used only where an unbounded git subprocess
// would turn a fast, local operation into one bounded by nothing but network
// state -- see remoteHasCommit.
//
// exec.CommandContext's DEFAULT cancellation is not enough here: it kills
// only the direct child, and `git fetch` over http hands the connection to a
// `git-remote-http` grandchild that inherits git's stdout/stderr pipes. Kill
// git alone and that grandchild survives, still holding the write end of
// CombinedOutput's pipe open -- so Wait() keeps blocking for EOF that never
// comes, and the "bounded" fetch hangs exactly as long as the unbounded one
// did. Confirmed live against a black hole listener before this was added:
// git alone, PID visible in `ps`, ran on with no parent. Putting the process
// in its own group and killing the whole group on cancellation is what
// actually bounds it.
func gitTimeout(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
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

// IsDispatchWorktree reports whether path lies inside a worktree this
// package created (Create or CreateOnBranch, above) -- i.e. somewhere under
// TMPDIR/estate-dispatch/<repo-name>-<repo-hash>/<dispatch-id>, possibly a
// subdirectory of it such as <worktree>/src/estate. If so it returns the
// dispatch id, the worktree's own top-level directory name under Root.
//
// This is the read side of the same layout Root/Create/CreateOnBranch write:
// no separate marker file, no env var of its own -- just the path shape this
// package already commits to. Anything that needs to know "am I running
// inside one specific dispatch's isolated checkout, and which one" (e.g.
// agent-estate#1048's per-turn knowledge index path) asks this rather than
// re-deriving the layout.
//
// A path outside TMPDIR/estate-dispatch, or one that names the dispatch root
// itself with no id segment beneath it, returns ("", false) -- the caller's
// own default path, never a guess.
func IsDispatchWorktree(path string) (id string, ok bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	// os.Getwd() on macOS resolves the /var -> /private/var symlink;
	// os.TempDir() does not. Compared unresolved, a worktree's own working
	// directory would never match its literal path under os.TempDir() at
	// all -- confirmed live, this was caught by this package's own test
	// failing against a real os.Chdir before EvalSymlinks was added.
	// Resolving both sides the same way is what makes the comparison below
	// mean what it says.
	dispatchRoot := resolveSymlinksOrSelf(filepath.Join(os.TempDir(), "estate-dispatch"))
	rel, err := filepath.Rel(dispatchRoot, resolveSymlinksOrSelf(abs))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	// parts[0] is the per-repo root's own <name>-<hash> directory; parts[1]
	// is the dispatch id (Create/CreateOnBranch's filepath.Join(base, id)).
	// Both must be present and non-empty -- a bare TMPDIR/estate-dispatch or
	// TMPDIR/estate-dispatch/<repo> is not, itself, one turn's worktree.
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

// resolveSymlinksOrSelf returns p with symlinks resolved, or p unchanged if
// it cannot be resolved (e.g. it does not exist yet) -- "could not resolve"
// falls back to the literal path rather than failing the caller outright.
func resolveSymlinksOrSelf(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// safeBranch reports whether branch can safely name a git ref to fetch and
// check out. Not a general refname validator -- git itself rejects a truly
// malformed ref -- but it refuses the two shapes that would otherwise reach
// exec.Command args in a way that could be misread: empty/whitespace-only,
// and anything starting with "-" (which git or a later `git push` could take
// as a flag rather than a ref).
func safeBranch(branch string) error {
	b := strings.TrimSpace(branch)
	if b == "" {
		return errors.New("branch is empty")
	}
	if strings.HasPrefix(b, "-") {
		return fmt.Errorf("branch %q starts with '-', which could be read as a flag rather than a ref name", b)
	}
	return nil
}

// CreateOnBranch gives a dispatch an isolated worktree checked out on an
// EXISTING branch, fetched fresh from origin -- what a FIX PASS needs
// (agent-estate#940's "does not survive a fix pass" follow-up), as opposed
// to Create's always-a-brand-new-branch shape, which is right for a first
// dispatch against an issue but wrong for a turn that must continue a pull
// request's own branch instead of starting a new lineage beside it.
//
// The checkout is DETACHED on purpose, not a local branch named `branch`.
// The ORIGINAL dispatch that opened the pull request may still have its own
// worktree and local branch of that exact name sitting on disk --
// Worktree.Remove refuses to tear down a worktree that holds committed,
// uncollected work (see Remove's own doc comment), so that first worktree
// is not guaranteed to be gone by the time a fix pass runs. git refuses to
// check the same branch out in two worktrees at once; detaching sidesteps
// that collision entirely; a caller pushes with `git push origin
// HEAD:<branch>` rather than a plain `git push`, which works the same from
// a detached HEAD.
//
// It refuses under the same conditions Create does (bad id, not a git
// repository, worktree already exists), plus a bad branch argument, plus
// one more: origin must actually have the branch -- a failed fetch is
// "could not confirm the tip to continue from," not "assume it is there."
func CreateOnBranch(repoRoot, id, branch string) (*Worktree, error) {
	if err := safeID(id); err != nil {
		return nil, fmt.Errorf("isolate: refusing to dispatch: %w", err)
	}
	if err := safeBranch(branch); err != nil {
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
		return nil, fmt.Errorf("isolate: refusing to dispatch: cannot determine whether %s is in use: %w", path, err)
	}

	// The real, current tip -- never a possibly-stale local ref, and never
	// simply assumed present.
	if _, err := git(repoRoot, "fetch", "origin", branch); err != nil {
		return nil, fmt.Errorf("isolate: refusing to dispatch: cannot fetch %q from origin, so there is no confirmed tip to continue from: %w", branch, err)
	}
	if _, err := git(repoRoot, "worktree", "add", "--detach", path, "origin/"+branch); err != nil {
		return nil, fmt.Errorf("isolate: refusing to dispatch: %w", err)
	}
	baseOut, err := git(path, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("isolate: refusing to dispatch: cannot record the worktree's base commit: %w", err)
	}
	return &Worktree{Path: path, Branch: branch, Base: strings.TrimSpace(string(baseOut)), root: repoRoot, Detached: true}, nil
}

// knownDetritus lists worktree-relative paths -- exactly as `git status
// --porcelain --ignored` reports them, including the trailing "/" git prints
// for a directory -- that are known to be regenerable build byproducts
// rather than a dispatch turn's own output. Dirty does not count an ignored
// path matching one of these toward "uncollected work"; every other ignored
// path still refuses teardown, and so does anything that is not merely
// ignored (untracked, modified, staged).
//
// This is an ALLOWLIST of specific, seen material -- NOT "ignore everything
// --ignored reports." A lane's built binary, a local config file, or a
// captured frame can be gitignored and still be the only copy of that
// output in existence; those must keep refusing teardown. Add an entry only
// after it has actually been seen leaking worktrees, not speculatively.
var knownDetritus = []string{
	// reference/ is deleted, unmaintained shell/Python kept only so it can be
	// READ, never run -- see this repo's own CLAUDE.md. Every dispatch's git
	// worktree still ends up running the Python interpreter over it somewhere
	// in its toolchain, which leaves compiled bytecode behind: 100%
	// regenerable, and it holds nothing any turn produced. Measured against
	// 138 live dispatch worktrees: agent-estate#983.
	"reference/scripts/supervisor/__pycache__/",
}

// isKnownDetritus reports whether path -- as printed after the "!! " prefix
// of a `git status --porcelain --ignored` line -- names known detritus
// rather than a turn's own output.
func isKnownDetritus(path string) bool {
	for _, d := range knownDetritus {
		if path == d {
			return true
		}
	}
	return false
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
//
// The one exception is knownDetritus above: an ignored path matched there is
// excluded from this count because it has been confirmed, not assumed, to be
// regenerable rather than a turn's output (agent-estate#983 -- the refusal
// above fired on 124 of 138 live dispatch worktrees whose only ignored
// content was exactly one such path, and the message it printed was false in
// every one of them). Anything ignored that is NOT on the list still counts
// as dirty, same as before this exception existed.
func (w *Worktree) Dirty() (bool, error) {
	out, err := git(w.Path, "status", "--porcelain", "--ignored")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		path, ok := strings.CutPrefix(line, "!! ")
		if !ok {
			// Not an ignored-only entry -- untracked, modified, staged, or
			// anything else git status reports. Always real.
			return true, nil
		}
		if !isKnownDetritus(path) {
			return true, nil
		}
	}
	return false, nil
}

// Head returns the worktree's current HEAD commit -- the estate's own,
// direct git observation of what the dispatched turn's worktree actually
// points at, read after the turn's subprocess has exited. This is NOT
// anything the subprocess said about itself; it is the same primitive
// Committed uses to answer a yes/no question, exposed here so a caller (see
// main.go's dispatch case) can record WHICH commit, not merely whether one
// exists. That recorded commit is what internal/gate's structural join
// binds a PR's own headRefOid against -- see agent-estate#940's follow-up
// review, which found that a branch NAME alone (this package's own
// dispatch/<id> convention) is not evidence of identity because anyone with
// push access can rename a branch and push different content under it. A
// SHA the estate itself observed inside this specific worktree is.
func (w *Worktree) Head() (string, error) {
	out, err := git(w.Path, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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

// remoteHasCommit reports whether commit is reachable from origin's own,
// freshly-fetched tip of branch -- a LIVE `git fetch` + `merge-base
// --is-ancestor`, not a cached local remote-tracking ref.
//
// agent-estate#985: Committed (above) only asks "did HEAD move past Base?".
// It does not consult any ref other than this worktree's own branch, so the
// refusal it fed into Remove asserted "nothing else references these
// commits" without ever checking whether anything did. This is the check
// that makes the claim true: origin's OWN copy of the branch, read live.
//
// A cached remote-tracking ref (refs/remotes/origin/<branch>) was
// considered and rejected. It is only as fresh as this worktree's last
// fetch, and staleness here is dangerous in the direction that matters: a
// tracking ref can say a commit is safely on origin when origin has since
// been force-pushed or the branch deleted, which would wave through a
// deletion of the only remaining copy. A live fetch costs a network round
// trip on every teardown of committed work; that cost is accepted because
// the alternative can be wrong in the direction this package exists to
// prevent.
//
// Any failure to consult origin -- no remote configured, the network
// unreachable, the fetch otherwise failing -- is "could not measure", and
// is returned as an error so the caller refuses rather than guesses. That
// matches Committed's own base-less path and estate pressure's exit-2
// convention: refuse when you cannot tell, never read "could not check" as
// "clean".
//
// The fetch itself is bounded by remoteFetchTimeout (above). Before this
// bound existed, a remote that black-holed packets instead of refusing the
// connection left this call -- and therefore Remove, and therefore every
// teardown of committed work -- hung on nothing but the OS TCP connect
// timeout. A timeout is itself a "could not measure": the message below
// says so explicitly, rather than reading like an ordinary fetch failure,
// so a caller cannot mistake "gave up waiting" for "asked and got no".
func (w *Worktree) remoteHasCommit(commit, branch string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteFetchTimeout)
	defer cancel()
	if _, err := gitTimeout(ctx, w.Path, "fetch", "-q", "origin", branch); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return false, fmt.Errorf("could not confirm %q on origin within %s -- the fetch did not complete in time, which is not the same as origin not having it: %w", branch, remoteFetchTimeout, err)
		}
		return false, fmt.Errorf("cannot fetch %q from origin to confirm its commits are referenced there: %w", branch, err)
	}
	if _, err := git(w.Path, "merge-base", "--is-ancestor", commit, "FETCH_HEAD"); err != nil {
		var exitErr *exec.ExitError
		// --is-ancestor's own documented convention: exit 1 means a definite
		// "no", nothing else. Any other exit (bad revision, corrupt object,
		// etc.) is "could not tell", which must not be read as "no".
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("cannot determine whether %s is an ancestor of origin/%s: %w", commit, branch, err)
	}
	return true, nil
}

// Remove tears the worktree and its branch down.
//
// It refuses when the worktree holds uncommitted changes OR when the turn
// committed anything that origin does not also have. A council seat found
// the committed case: an agent that committed its work left a CLEAN git
// status, so the uncommitted check saw nothing to collect, and
// `git branch -D` then deleted the only ref to those commits. The agent did
// the tidy thing and lost more for it.
//
// agent-estate#985 found the committed check asserted more than it verified:
// it only asked whether HEAD had moved past Base, never whether anything
// else actually referenced the result, so it refused a worktree whose
// commits were already pushed and the head of an open pull request -- the
// success path. remoteHasCommit (above) is what makes "collected" mean what
// the refusal message claims: reachable from origin's own tip of the
// branch, confirmed live. A worktree whose commits origin does not have is
// still refused exactly as before.
//
// agent-estate#1000 found the reverse failure in the same check: origin's
// copy of the dispatch branch is deleted BY the merge that collects the
// work (`gh pr merge --squash --delete-branch`), so from the moment a turn
// succeeds the fetch fails, the refusal fires, and it fires forever. That
// is not conservatism, it is a leak -- 176 worktrees, then an OOM. The
// Landed seam is the second, independent way to establish collection, and
// it is consulted ONLY after the branch evidence has failed to establish
// it. It can turn a refusal into a removal; it can never turn a removal
// into a refusal, and it is never asked to overrule a positive.
//
// All these refusals are the same rule: a dispatch's uncollected output
// looks exactly like an empty worktree from outside, and deleting it is
// unrecoverable while reporting it is not.
func (w *Worktree) Remove() error {
	committed, cerr := w.Committed()
	if cerr != nil {
		return fmt.Errorf("isolate: cannot tell whether %s holds committed work, so refusing to remove it: %w", w.Path, cerr)
	}
	if committed {
		head, herr := w.Head()
		if herr != nil {
			return fmt.Errorf("isolate: cannot read %s's HEAD to check whether its commits are referenced elsewhere, so refusing to remove it: %w", w.Path, herr)
		}
		collected, rerr := w.remoteHasCommit(head, w.Branch)
		if !collected {
			// The branch could not vouch for the commits -- either origin
			// says no, or we could not ask it. Both are the shape a
			// successful squash merge leaves behind, so ask the forge
			// whether the work landed before refusing. A failure to ask is
			// carried, not swallowed: if neither source could establish
			// collection, we still refuse below.
			landed, lerr := w.landed(head)
			switch {
			case lerr == nil && landed:
				collected, rerr = true, nil
			case lerr != nil && rerr == nil:
				rerr = lerr
			case lerr != nil && rerr != nil:
				rerr = fmt.Errorf("%w; and %v", rerr, lerr)
			}
		}
		if rerr != nil {
			return fmt.Errorf("isolate: cannot confirm %s's commits on %s are referenced elsewhere; refusing to remove it -- collect them first: %w", w.Path, w.Branch, rerr)
		}
		if !collected {
			return fmt.Errorf("isolate: %s has commits on %s that nothing else references; refusing to remove it -- collect them first", w.Path, w.Branch)
		}
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
	// A CreateOnBranch worktree never had a local branch of its own -- it
	// checked out origin/<branch> detached, on purpose, so a fix-pass
	// worktree can never collide with the ORIGINAL dispatch's own,
	// still-live local branch of the same name (see CreateOnBranch's doc
	// comment). There is nothing to delete here for it.
	if w.Detached {
		return nil
	}
	if _, err := git(w.root, "branch", "-D", w.Branch); err != nil {
		return err
	}
	return nil
}

// Reattach rebuilds a Worktree value for a worktree that some OTHER process
// created, from the facts that process durably recorded about it, so the
// identical Remove refusals can be applied to it later.
//
// WHY IT HAS TO EXIST (agent-estate#1000). Create returns a Worktree that
// lives in the dispatching process's memory, and Remove is a method on it.
// Signals skip defers: a dispatch that is OOM-killed or SIGKILLed -- which
// is exactly how this host died -- runs no teardown at all, and the value
// that knew how to tear its worktree down dies with it. A design where only
// the dying process can tidy up is the same defect with a longer list. This
// is the third-party path: a LATER process reads the ledger's own record of
// path, branch and base and applies the same guards to a corpse's worktree.
//
// It refuses rather than guessing whenever a fact needed to judge safety is
// missing:
//
//   - base == "" would make Committed unable to tell what the turn
//     committed. Committed already fails closed on that, but refusing here
//     names the true cause instead of surfacing it three calls later.
//   - path must sit DIRECTLY under this repository's own dispatch Root. A
//     recorded path is a string from a file, and the operation it feeds is
//     `git worktree remove`; confining it to the one directory this package
//     creates worktrees in is what stops a corrupted or hand-edited record
//     naming something else on the disk.
//   - path must exist and be a git worktree.
//
// Detached is OBSERVED from the worktree itself rather than read from the
// record: git is the authority on whether HEAD is a branch or a detached
// checkout, and Remove uses the answer to decide whether a local branch ref
// remains to delete. A recorded flag could be stale; `symbolic-ref` cannot.
func Reattach(repoRoot, path, branch, base string) (*Worktree, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("isolate: no worktree path recorded, so there is nothing to reattach to")
	}
	if err := safeBranch(branch); err != nil {
		return nil, fmt.Errorf("isolate: refusing to reattach: %w", err)
	}
	if strings.TrimSpace(base) == "" {
		return nil, fmt.Errorf("isolate: refusing to reattach to %s: no base commit was recorded for it, so there is no way to tell what the turn committed", path)
	}
	root := Root(repoRoot)
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.ContainsRune(rel, filepath.Separator) {
		return nil, fmt.Errorf("isolate: refusing to reattach to %s: it is not a worktree directly under this repository's dispatch root %s", path, root)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("isolate: refusing to reattach to %s: %w", path, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("isolate: refusing to reattach to %s: it is not a directory", path)
	}
	if _, err := git(path, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("isolate: refusing to reattach to %s: it is not a git worktree: %w", path, err)
	}
	// `symbolic-ref -q HEAD` exits non-zero, quietly, exactly when HEAD is
	// detached -- and the rev-parse above has already established that this
	// is a git worktree, so a failure here cannot mean "not a repository".
	detached := false
	if _, err := git(path, "symbolic-ref", "-q", "HEAD"); err != nil {
		detached = true
	}
	return &Worktree{Path: path, Branch: branch, Base: base, Detached: detached, root: repoRoot}, nil
}
