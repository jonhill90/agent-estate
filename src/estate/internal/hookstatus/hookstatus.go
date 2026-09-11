// Package hookstatus answers, for one deployed hook checkout, the question
// agent-dotfiles#356 named: "nothing reports it." A PreToolUse guard that
// silently runs an old version looks exactly like a guard that works --
// #353 (main-branch-guard's Case A/B) and #357 (keychain-write-guard,
// merged upstream, never deployed at all) are both that shape. This
// package is DETECTION ONLY: it reads the checkout and origin/main,
// classifies every file under hooks/, and reports. It never fetches, pulls,
// tidies, writes to, or otherwise mutates the checkout it inspects --
// deployment policy is a decision for a human, not this package (see the
// package's own callers in main.go, which refuse a --fix flag on purpose).
package hookstatus

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// State classifies one file under hooks/ against the checkout's own HEAD
// and against origin/main. Comparisons are by git blob SHA1 -- the same
// notion of "identical content" git itself uses, so two files that differ
// only in a way git would consider identical (there is no such way; blob
// hashing is exact) never disagree with `git diff` or `git status`.
type State string

const (
	// Current means the deployed file's content is byte-identical to
	// origin/main's -- the goal state, regardless of whether HEAD's own
	// tree happens to carry the same blob (a file hand-copied from
	// origin/main without a commit, like command_guard.py during the
	// three-file partial deploy, is Current, not Drifted).
	Current State = "current"
	// Stale means the deployed file matches HEAD exactly (nobody has
	// touched it locally) but HEAD's own blob differs from origin/main's --
	// an ordinary, honest "the checkout is old" gap.
	Stale State = "stale"
	// Drifted means the deployed file matches NEITHER HEAD nor
	// origin/main -- someone edited it by hand in the live checkout and it
	// was never committed or reconciled. ledger-write-guard.sh was found in
	// this state (agent-estate#1339's sibling investigation).
	Drifted State = "drifted"
	// Absent means origin/main has this file and the deployed checkout does
	// not -- #357's shape (keychain-write-guard.sh), which a diff of only
	// the files that already exist locally would never surface.
	Absent State = "absent"
	// LocalOnly means the deployed checkout has this file and origin/main
	// does not. Informational -- not itself a defect -- but worth surfacing
	// rather than silently dropping from the report.
	LocalOnly State = "local-only"
)

// File is one path's full picture.
type File struct {
	// Path is relative to the checkout root, e.g. "hooks/ledger-write-guard.sh".
	Path  string `json:"path"`
	State State  `json:"state"`
	// Wired reports whether Path (by its basename, resolved against
	// HooksDir) is one of the commands the LIVE Claude settings.json
	// actually registers as a PreToolUse hook right now.
	Wired bool `json:"wired"`
	// ShouldWire reports whether origin/main's own settings fragment
	// (settings/claude/settings.json) declares this file as a hook --
	// the second half of #357: a file can be deployed to disk and still
	// not be wired, or wired in the live settings.json while origin/main
	// no longer expects it there at all.
	ShouldWire bool `json:"should_wire"`

	LocalSHA    string `json:"local_sha,omitempty"`
	HeadSHA     string `json:"head_sha,omitempty"`
	UpstreamSHA string `json:"upstream_sha,omitempty"`
}

// Report is the full answer for one checkout.
type Report struct {
	CheckoutDir string `json:"checkout_dir"`
	Repo        string `json:"repo"` // "owner/name", resolved from the checkout's own origin remote

	HeadSHA string `json:"head_sha"`
	// HeadPushed is false when HeadSHA is not a commit GitHub has ever
	// seen -- the deployed checkout's own HEAD can be a purely local
	// commit (agent-dotfiles' a3f6e09 is exactly this), which is itself
	// worth reporting: this checkout is not just behind, it branched.
	HeadPushed bool `json:"head_pushed"`

	UpstreamMainSHA string `json:"upstream_main_sha"`

	// AheadBy is how many commits origin/main leads HEAD by. Computed
	// ONLY from the checkout's own local git history (never a fetch this
	// package performs) and only when that local knowledge is corroborated
	// fresh against GitHub's live main SHA -- see AheadByKnown.
	AheadBy      int    `json:"ahead_by,omitempty"`
	AheadByKnown bool   `json:"ahead_by_known"`
	AheadByNote  string `json:"ahead_by_note,omitempty"`

	Files []File `json:"files"`
}

// Upstream is the GitHub-backed read seam every origin/main fact goes
// through -- swapped out in tests so they need no network, mirroring
// internal/gate's listMainRuns seam. The real implementation shells to
// `gh api` (this repo's CLI-first convention); see github.go.
type Upstream interface {
	// MainSHA returns the live commit SHA at the tip of repo's default
	// branch.
	MainSHA(repo string) (string, error)
	// Tree returns path -> blob SHA for every blob whose path has prefix,
	// as of ref.
	Tree(repo, ref, prefix string) (map[string]string, error)
	// File returns the raw bytes of path as of ref.
	File(repo, ref, path string) ([]byte, error)
	// CommitExists reports whether GitHub has ever seen sha at all. The
	// deployed checkout's own HEAD can be a purely local commit no push
	// ever reached (agent-dotfiles' a3f6e09 is exactly this case) --
	// distinct from "behind", and worth its own field.
	CommitExists(repo, sha string) (bool, error)
}

// DefaultCheckoutDir is the frozen local checkout every deployed PreToolUse
// hook resolves from today -- overridable via ESTATE_HOOK_CHECKOUT for a
// test or a second checkout, never hardcoded past this one function.
func DefaultCheckoutDir() (string, error) {
	if p := os.Getenv("ESTATE_HOOK_CHECKOUT"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "source", "repos", "Personal", "agent-dotfiles"), nil
}

// DefaultSettingsPath is the live Claude settings file every PreToolUse
// hook is actually wired from -- overridable via ESTATE_HOOK_SETTINGS.
func DefaultSettingsPath() (string, error) {
	if p := os.Getenv("ESTATE_HOOK_SETTINGS"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// HooksPrefix is the one directory this package inspects. Scoped
// deliberately narrow (agent-dotfiles#356's own brief): the hooks/ tree,
// not the whole repository.
const HooksPrefix = "hooks/"

// SettingsFragmentPath is where origin/main declares which hooks/* files
// SHOULD be wired -- compared against the live Claude settings.json to
// catch #357's second half (a file present on disk but never registered).
const SettingsFragmentPath = "settings/claude/settings.json"

// Compute builds the full report for one checkout. settingsPath is the
// live Claude settings.json to read wiring from (typically
// ~/.claude/settings.json); checkoutDir is the frozen local checkout
// (typically ~/source/repos/Personal/agent-dotfiles). Read-only: Compute
// never writes to checkoutDir and never runs `git fetch`, `git pull`, or
// anything else that mutates it -- every local read is `git show`,
// `git ls-tree`, `git hash-object`, `git rev-parse`, and `git remote
// get-url`, none of which touch the working tree, index, or refs.
func Compute(checkoutDir, settingsPath string, up Upstream) (Report, error) {
	repo, err := originRepo(checkoutDir)
	if err != nil {
		return Report{}, fmt.Errorf("could not resolve %s's own origin remote: %w", checkoutDir, err)
	}
	headSHA, err := gitOutput(checkoutDir, "rev-parse", "HEAD")
	if err != nil {
		return Report{}, fmt.Errorf("could not read %s's own HEAD: %w", checkoutDir, err)
	}

	headBlobs, err := headTree(checkoutDir, headSHA)
	if err != nil {
		return Report{}, fmt.Errorf("could not read %s's own HEAD tree under %s: %w", checkoutDir, HooksPrefix, err)
	}
	localBlobs, err := localTree(checkoutDir)
	if err != nil {
		return Report{}, fmt.Errorf("could not read %s's own working tree under %s: %w", checkoutDir, HooksPrefix, err)
	}

	rep := Report{
		CheckoutDir: checkoutDir,
		Repo:        repo,
		HeadSHA:     headSHA,
	}

	upstreamMain, err := up.MainSHA(repo)
	if err != nil {
		return Report{}, fmt.Errorf("could not ask GitHub for %s's live main SHA: %w", repo, err)
	}
	rep.UpstreamMainSHA = upstreamMain

	headPushed, err := up.CommitExists(repo, headSHA)
	if err != nil {
		return Report{}, fmt.Errorf("could not ask GitHub whether %s's HEAD (%s) was ever pushed: %w", repo, short(headSHA), err)
	}
	rep.HeadPushed = headPushed

	rep.AheadBy, rep.AheadByKnown, rep.AheadByNote = aheadCount(checkoutDir, headSHA, upstreamMain)

	upstreamBlobs, err := up.Tree(repo, "main", HooksPrefix)
	if err != nil {
		return Report{}, fmt.Errorf("could not ask GitHub for %s's hooks/ tree at main: %w", repo, err)
	}

	wired, err := wiredPaths(settingsPath, checkoutDir)
	if err != nil {
		return Report{}, fmt.Errorf("could not read the live settings file %s: %w", settingsPath, err)
	}
	shouldWireBytes, ferr := up.File(repo, "main", SettingsFragmentPath)
	var shouldWire map[string]bool
	switch {
	case ferr == nil:
		shouldWire, err = parseSettingsHookPaths(shouldWireBytes)
		if err != nil {
			return Report{}, fmt.Errorf("could not parse origin/main's %s: %w", SettingsFragmentPath, err)
		}
	case errors.Is(ferr, ErrNotFound):
		// origin/main genuinely not having this file is itself worth
		// reporting rather than failing Compute outright -- every file's
		// ShouldWire simply reads false, honestly, not "unknown". A real
		// read failure (auth, rate limit, network) is NOT folded in here
		// -- that still fails Compute below, so a silent auth problem
		// never masquerades as "nothing should be wired".
		shouldWire = map[string]bool{}
	default:
		return Report{}, fmt.Errorf("could not read origin/main's %s: %w", SettingsFragmentPath, ferr)
	}

	paths := map[string]bool{}
	for p := range headBlobs {
		paths[p] = true
	}
	for p := range localBlobs {
		paths[p] = true
	}
	for p := range upstreamBlobs {
		paths[p] = true
	}

	for path := range paths {
		f := File{
			Path:        path,
			LocalSHA:    localBlobs[path],
			HeadSHA:     headBlobs[path],
			UpstreamSHA: upstreamBlobs[path],
			Wired:       wired[path],
			ShouldWire:  shouldWire[path],
		}
		f.State = classify(f)
		rep.Files = append(rep.Files, f)
	}
	sort.Slice(rep.Files, func(i, j int) bool { return rep.Files[i].Path < rep.Files[j].Path })

	return rep, nil
}

func classify(f File) State {
	_, local := f.pathsExist()
	switch {
	case !local && f.UpstreamSHA != "":
		return Absent
	case f.UpstreamSHA == "" && local:
		return LocalOnly
	case f.LocalSHA != "" && f.LocalSHA == f.UpstreamSHA:
		return Current
	case f.LocalSHA != "" && f.LocalSHA == f.HeadSHA:
		return Stale
	default:
		return Drifted
	}
}

// pathsExist is a small readability seam for classify's first branch --
// named so the two-value discard above reads as intentional, not
// forgotten.
func (f File) pathsExist() (upstream, local bool) {
	return f.UpstreamSHA != "", f.LocalSHA != ""
}

// originRepo resolves checkoutDir's own "origin" remote to "owner/name",
// read-only (`git remote get-url`, never a fetch).
func originRepo(checkoutDir string) (string, error) {
	url, err := gitOutput(checkoutDir, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return parseOwnerRepo(url)
}

func parseOwnerRepo(url string) (string, error) {
	url = strings.TrimSuffix(strings.TrimSpace(url), ".git")
	switch {
	case strings.HasPrefix(url, "git@github.com:"):
		return strings.TrimPrefix(url, "git@github.com:"), nil
	case strings.Contains(url, "github.com/"):
		i := strings.Index(url, "github.com/")
		return url[i+len("github.com/"):], nil
	default:
		return "", fmt.Errorf("origin remote %q is not a github.com URL this package knows how to read", url)
	}
}

// headTree reads HEAD's own tree under HooksPrefix -- purely local, no
// network, no write: `git ls-tree` reads objects already on disk.
func headTree(checkoutDir, headSHA string) (map[string]string, error) {
	out, err := gitOutput(checkoutDir, "ls-tree", "-r", headSHA, "--", strings.TrimSuffix(HooksPrefix, "/"))
	if err != nil {
		return nil, err
	}
	blobs := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		// "<mode> blob <sha>\t<path>"
		line := sc.Text()
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		fields := strings.Fields(line[:tab])
		if len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		blobs[line[tab+1:]] = fields[2]
	}
	return blobs, sc.Err()
}

// localTree walks the checkout's own working copy of hooks/ and computes
// each regular file's git blob SHA via `git hash-object` -- the exact
// value git itself would assign, without staging or committing anything.
func localTree(checkoutDir string) (map[string]string, error) {
	root := filepath.Join(checkoutDir, filepath.FromSlash(strings.TrimSuffix(HooksPrefix, "/")))
	var rels []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == root {
				return nil // no hooks/ dir at all -- an empty tree, not an error
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil // a symlink is not a deployed copy to classify
		}
		rel, rerr := filepath.Rel(checkoutDir, p)
		if rerr != nil {
			return rerr
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(rels) == 0 {
		return map[string]string{}, nil
	}

	cmd := exec.Command("git", "-C", checkoutDir, "hash-object", "--stdin-paths")
	cmd.Stdin = strings.NewReader(strings.Join(rels, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git hash-object --stdin-paths: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(rels) {
		return nil, fmt.Errorf("git hash-object returned %d hash(es) for %d path(s)", len(lines), len(rels))
	}
	blobs := make(map[string]string, len(rels))
	for i, rel := range rels {
		blobs[rel] = lines[i]
	}
	return blobs, nil
}

// aheadCount reports how many commits origin/main leads headSHA by, using
// ONLY the checkout's own already-fetched local knowledge of origin/main
// (`git rev-parse origin/main`, `git rev-list --count`) -- this package
// never runs `git fetch` itself. The local remote-tracking ref is trusted
// ONLY when it is corroborated fresh: its own SHA must equal upstreamMain,
// which the caller already read live from GitHub. A stale or absent local
// ref reports honestly as unknown rather than a wrong number.
func aheadCount(checkoutDir, headSHA, upstreamMain string) (n int, known bool, note string) {
	localMain, err := gitOutput(checkoutDir, "rev-parse", "origin/main")
	if err != nil {
		return 0, false, "no local origin/main ref to read (and this package does not fetch): " + err.Error()
	}
	if localMain != upstreamMain {
		return 0, false, fmt.Sprintf(
			"local origin/main ref (%s) does not match GitHub's live main (%s) -- stale local knowledge, "+
				"and this package does not fetch to refresh it; per-file classification above is still "+
				"authoritative (it reads origin/main directly via the GitHub API)", short(localMain), short(upstreamMain))
	}
	out, err := gitOutput(checkoutDir, "rev-list", "--count", headSHA+"..origin/main")
	if err != nil {
		return 0, false, "git rev-list --count failed: " + err.Error()
	}
	var count int
	if _, serr := fmt.Sscanf(out, "%d", &count); serr != nil {
		return 0, false, "could not parse git rev-list's own output: " + out
	}
	return count, true, ""
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func gitOutput(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
