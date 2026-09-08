// Command progress answers the only question that matters: did the app move?
//
// For a month the estate produced 236 commits against the shell supervisor and
// 13 against the app, and nobody noticed because nothing measured it. Merged
// PRs, closed issues and swept worktrees all rose steadily while the product
// stood still. This makes that ratio impossible to miss.
//
// Exit 1 when app work is a minority of the window's commits, so it can gate a
// weekly report rather than sit in a dashboard nobody opens.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Paths that constitute the product. Everything else is scaffolding: useful,
// sometimes necessary, never progress on its own.
var appPaths = []string{"src/tui", "src/estate"}

// Scaffolding worth counting separately so the ratio is visible.
//
// scripts/ added 2026-09-08 (agent-estate#1311): Jon's 2026-09-07 rule narrows
// Go-only to the APP, not tooling, so scripts/ (docs-lint, evidence,
// knowledge, the vault validator landed in #1296) is exactly this file's own
// definition of scaffolding -- "useful, sometimes necessary, never progress
// on its own" -- and was invisible to this instrument entirely before this
// fix, four commits uncounted as either app or scaffolding over the prior
// two-week window. src/langguard removed: the directory does not exist
// (deleted with the CI gate it enforced, agent-estate#1311) and was dead
// config nothing maintained -- `git log` on a nonexistent pathspec does not
// error, so this had gone unnoticed since deletion; see
// TestAppAndSupportPathsResolveOnDisk, added the same day, which would have
// caught it then. scripts/ itself turned out to be the same failure mode one
// level subtler (PR #1316 review): `git log -- scripts` also does not error
// on a pathspec that existed, was deleted, and was later reused for
// something unrelated -- it was the shell/Python supervisor until #906
// deleted it 2026-08-30, then became this unrelated docs-lint/evidence/
// knowledge directory from #1272 onward, and counting both eras as one
// inflated scaffolding by 90 of 96 hits. commitsForPath/pathRebirth bound
// every path here (not just scripts/) to its current incarnation so a
// reused name can never again silently conflate two different directories.
var supportPaths = []string{"reference", ".github", "docs", "scripts", "src/notify", "src/issuemine", "src/progress"}

// count totals commits touching any of paths since the given date, counting
// each commit once even when it touches several of them. A path name is not
// a stable directory identity across history (agent-estate#1316 review):
// git log -- path matches any commit that ever touched that string, oblivious
// to a delete-and-unrelated-recreate in between, so counting is done
// per-path via commitsForPath (which bounds each path to its current
// incarnation) and the resulting commit hashes are unioned in a set --
// batching all paths into one git-log call, as this used to do, would lose
// that per-path boundary.
func count(since string, paths []string) (int, error) {
	root, err := repoRoot()
	if err != nil {
		return 0, err
	}
	return countAt(root, since, paths)
}

// countAt is count with an explicit repo root, so it can run against a
// fixture repo in tests without touching process cwd.
func countAt(root, since string, paths []string) (int, error) {
	seen := map[string]bool{}
	for _, p := range paths {
		hashes, err := commitsForPath(root, since, p)
		if err != nil {
			return 0, err
		}
		for _, h := range hashes {
			seen[h] = true
		}
	}
	return len(seen), nil
}

// commitsForPath returns the commit hashes, since the given date, that touch
// path -- but only from path's current incarnation onward. scripts/ was the
// shell/Python supervisor until #906 deleted it 2026-08-30, then became an
// unrelated docs-lint/evidence/knowledge directory from #1272 onward: naive
// `git log -- scripts` conflated both eras, inflating the reviewed PR's
// scaffolding count by 90 of 96 hits (67% of the reported total) with
// commits belonging to a directory that no longer exists. pathRebirth finds
// the most recent point path came back into existence and this excludes
// everything before it.
func commitsForPath(root, since, path string) ([]string, error) {
	boundary, err := pathRebirth(root, path)
	if err != nil {
		return nil, err
	}
	args := []string{"log", "--since=" + since, "--format=%H"}
	if boundary != "" {
		args = append(args, boundary+"..HEAD")
	}
	args = append(args, "--", path)
	out, err := runGit(root, args...)
	if err != nil {
		return nil, fmt.Errorf("git log %v: %w", args, err)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// pathRebirth returns the commit boundary such that "boundary..HEAD -- path"
// includes only path's current incarnation: everything from the most recent
// time it came back into existence onward (its most recent "birth", if it
// has only ever existed once). Returns "" when there is no earlier
// incarnation to exclude -- no history at all, or the birth commit is the
// repo's very first commit and so has no parent to exclude.
func pathRebirth(root, path string) (string, error) {
	out, err := runGit(root, "log", "--reverse", "--format=%H", "--", path)
	if err != nil {
		return "", fmt.Errorf("git log --reverse -- %s: %w", path, err)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return "", nil
	}
	var lastBirth string
	for _, c := range strings.Split(s, "\n") {
		existed, err := existsAtParent(root, c, path)
		if err != nil {
			return "", err
		}
		if !existed {
			lastBirth = c
		}
	}
	if lastBirth == "" {
		return "", nil
	}
	parent, err := runGit(root, "rev-parse", lastBirth+"^")
	if err != nil {
		// lastBirth has no parent -- it's the repo's first commit, so
		// path's whole history is one incarnation and needs no boundary.
		return "", nil
	}
	return strings.TrimSpace(parent), nil
}

// existsAtParent reports whether path existed in commit's first parent.
// A commit with no parent (the repo's very first commit) reports false:
// nothing can have existed "before" the repository began.
func existsAtParent(root, commit, path string) (bool, error) {
	parent, err := runGit(root, "rev-parse", commit+"^")
	if err != nil {
		return false, nil
	}
	if _, err := runGit(root, "cat-file", "-e", strings.TrimSpace(parent)+":"+path); err != nil {
		return false, nil
	}
	return true, nil
}

// runGit runs git with an explicit working directory rather than the
// process's own cwd, so callers work the same from main() (invoked from
// repo root) and from tests against a fixture repo elsewhere on disk.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// repoRoot resolves the repository root regardless of the caller's own
// working directory -- `go test` always runs with cwd set to the package's
// own source directory (src/progress/), never the invoker's cwd, so a git
// pathspec relative to "wherever this process happens to be" silently
// resolves against the wrong base there. git itself walks upward to find it
// from any subdirectory, so this works from either context.
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitTrackedDirs lists the immediate tracked directories under root (repo-root
// relative paths, e.g. "src/estate"), or the repo's own top-level directories
// when root == "". Reads the tree actually checked out (HEAD), not a remote
// ref that may not exist in every clone -- a fresh worktree at a detached
// commit has no origin/main to compare against. Runs with cwd pinned to the
// repo root it resolves itself, so this gives the same answer called from
// main() (invoked from repo root, per this command's own usage) or from a
// test (invoked with cwd forced to this package's own directory).
func gitTrackedDirs(root string) ([]string, error) {
	top, err := repoRoot()
	if err != nil {
		return nil, err
	}
	args := []string{"ls-tree", "-d", "--name-only", "HEAD"}
	if root != "" {
		args = append(args, "--", root+"/")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = top
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree %q: %w", root, err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

// unclassified reports which entries in universe are named in neither
// classified list -- set subtraction, nothing more, kept pure and separate
// from the git calls above so it is testable without a real repo.
func unclassified(universe []string, classified ...[]string) []string {
	known := map[string]bool{}
	for _, list := range classified {
		for _, p := range list {
			known[p] = true
		}
	}
	var out []string
	for _, p := range universe {
		if !known[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// classifiableUniverse is every path this program COULD classify: every
// top-level tracked directory except "src" itself (src's own immediate
// children are individually classified below instead -- appPaths/
// supportPaths already mix "src/tui" alongside bare "docs", so the universe
// this check enumerates has to mirror that same mixed depth or it would
// falsely report every src/* child as unclassified). Root-level FILES
// (AGENTS.md, go.work, ledger.lock, ...) are deliberately excluded: neither
// existing list has ever classified a file, only directories, and folding
// loose root files into an app-vs-scaffolding ratio is a different, wider
// question than the one agent-estate#1311 asked -- narrowing this to
// directories keeps the fix to what drifted, not what could theoretically
// also be counted.
func classifiableUniverse() ([]string, error) {
	top, err := gitTrackedDirs("")
	if err != nil {
		return nil, err
	}
	var universe []string
	for _, d := range top {
		if d == "src" {
			continue
		}
		universe = append(universe, d)
	}
	srcChildren, err := gitTrackedDirs("src")
	if err != nil {
		return nil, err
	}
	universe = append(universe, srcChildren...)
	return universe, nil
}

func main() {
	since := "2 weeks ago"
	if len(os.Args) > 1 {
		since = strings.Join(os.Args[1:], " ")
	}

	app, err := count(since, appPaths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "progress: could not measure app commits:", err)
		os.Exit(2) // could not measure is not the same as progress
	}
	sup, err := count(since, supportPaths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "progress: could not measure support commits:", err)
		os.Exit(2)
	}

	// Unclassified paths are reported, never silently dropped -- an
	// instrument that cannot see a thing looks exactly like the thing being
	// absent (corpus it-d43a08d739bf32a8). A tree-listing failure here is
	// itself "could not measure": refusing beats printing a ratio that
	// silently omits whatever this call would have found.
	universe, err := classifiableUniverse()
	if err != nil {
		fmt.Fprintln(os.Stderr, "progress: could not enumerate the tree to check for unclassified paths:", err)
		os.Exit(2)
	}
	unc := unclassified(universe, appPaths, supportPaths)
	uncCount := 0
	if len(unc) > 0 {
		uncCount, err = count(since, unc)
		if err != nil {
			fmt.Fprintln(os.Stderr, "progress: could not measure unclassified commits:", err)
			os.Exit(2)
		}
	}

	total := app + sup
	fmt.Printf("since %s\n", since)
	fmt.Printf("  app        %3d  (%s)\n", app, strings.Join(appPaths, ", "))
	fmt.Printf("  scaffolding%3d\n", sup)
	if uncCount > 0 {
		fmt.Printf("  unclassified%2d  (%s) -- neither app nor scaffolding; add to one list or leave visible here\n", uncCount, strings.Join(unc, ", "))
	}
	if total == 0 && uncCount == 0 {
		fmt.Println("\nno commits in this window -- nothing shipped, and that is the finding")
		os.Exit(1)
	}
	if total == 0 {
		fmt.Println("\nno app or scaffolding commits in this window, but unclassified paths had activity -- see above; the ratio below cannot be trusted until they are classified")
		os.Exit(2)
	}
	pct := float64(app) / float64(total) * 100
	fmt.Printf("  app share  %.0f%%\n", pct)

	if app == 0 {
		fmt.Println("\nZERO app commits. Everything in this window was scaffolding.")
		os.Exit(1)
	}
	if pct < 50 {
		ratio := strconv.FormatFloat(float64(sup)/float64(app), 'f', 1, 64)
		fmt.Printf("\n%s scaffolding commits per app commit. The product is not the thing being worked on.\n", ratio)
		os.Exit(1)
	}
	fmt.Println("\napp work is the majority of this window")
}
