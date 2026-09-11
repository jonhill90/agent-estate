package hookstatus

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeUpstream is an in-memory Upstream -- every test here runs with no
// network, mirroring internal/gate's own seam-swap pattern for `gh` calls.
type fakeUpstream struct {
	mainSHA      string
	tree         map[string]string // path -> blob sha, under "hooks/"
	files        map[string][]byte // "ref/path" -> content
	knownCommits map[string]bool
}

func (f *fakeUpstream) MainSHA(repo string) (string, error) { return f.mainSHA, nil }

func (f *fakeUpstream) Tree(repo, ref, prefix string) (map[string]string, error) {
	out := map[string]string{}
	for p, sha := range f.tree {
		if len(p) >= len(prefix) && p[:len(prefix)] == prefix {
			out[p] = sha
		}
	}
	return out, nil
}

func (f *fakeUpstream) File(repo, ref, path string) ([]byte, error) {
	b, ok := f.files[ref+"/"+path]
	if !ok {
		return nil, ErrNotFound
	}
	return b, nil
}

func (f *fakeUpstream) CommitExists(repo, sha string) (bool, error) {
	return f.knownCommits[sha], nil
}

// gitRepo builds a real temp git repo with one commit under hooks/,
// mirroring internal/isolate's own repo(t) test helper -- git plumbing is
// cheap and exact, so tests exercise real `git ls-tree`/`hash-object`
// rather than a reimplementation of what they'd return.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("remote", "add", "origin", "https://github.com/example/repo.git")
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-qm", "seed")
	return root
}

func headSHA(t *testing.T, dir string) string {
	t.Helper()
	sha, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

func blobSHA(t *testing.T, dir, path string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "hash-object", filepath.Join(dir, path)).Output()
	if err != nil {
		t.Fatal(err)
	}
	return trimNL(string(out))
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func writeSettings(t *testing.T, checkoutDir string, commands []string) string {
	t.Helper()
	type h struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type matcher struct {
		Matcher string `json:"matcher"`
		Hooks   []h    `json:"hooks"`
	}
	var hs []h
	for _, c := range commands {
		hs = append(hs, h{Type: "command", Command: c})
	}
	doc := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []matcher{{Matcher: "Bash", Hooks: hs}},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func fragmentBytes(t *testing.T, relCommands []string) []byte {
	t.Helper()
	type h struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type matcher struct {
		Matcher string `json:"matcher"`
		Hooks   []h    `json:"hooks"`
	}
	var hs []h
	for _, c := range relCommands {
		hs = append(hs, h{Type: "command", Command: c})
	}
	doc := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []matcher{{Matcher: "Bash", Hooks: hs}},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fileByPath(t *testing.T, rep Report, path string) File {
	t.Helper()
	for _, f := range rep.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("Compute's report has no entry for %q; files: %+v", path, rep.Files)
	return File{}
}

// A file untouched since HEAD, whose HEAD content upstream has since
// changed, is Stale -- the ordinary "checkout is old" case, exactly
// gh-body-guard.sh's real shape in agent-dotfiles right now.
func TestStaleFileMatchesHeadNotUpstream(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"hooks/gh-body-guard.sh": "#!/bin/sh\necho old\n",
	})
	head := headSHA(t, dir)
	headBlob := blobSHA(t, dir, "hooks/gh-body-guard.sh")

	up := &fakeUpstream{
		mainSHA:      "deadbeef",
		tree:         map[string]string{"hooks/gh-body-guard.sh": "newblobsha"},
		files:        map[string][]byte{"main/settings/claude/settings.json": fragmentBytes(t, []string{"hooks/gh-body-guard.sh"})},
		knownCommits: map[string]bool{head: true},
	}
	settings := writeSettings(t, dir, []string{filepath.Join(dir, "hooks/gh-body-guard.sh")})

	rep, err := Compute(dir, settings, up)
	if err != nil {
		t.Fatal(err)
	}
	f := fileByPath(t, rep, "hooks/gh-body-guard.sh")
	if f.State != Stale {
		t.Fatalf("got state %q, want %q (local sha %s, head sha %s, upstream sha %s)", f.State, Stale, f.LocalSHA, f.HeadSHA, f.UpstreamSHA)
	}
	if f.LocalSHA != headBlob {
		t.Fatalf("local sha %s != head blob %s -- test's own fixture is wrong", f.LocalSHA, headBlob)
	}
	if !f.Wired {
		t.Fatal("gh-body-guard.sh is registered in the live settings.json fixture; Wired should be true")
	}
	if !f.ShouldWire {
		t.Fatal("gh-body-guard.sh is registered in origin/main's own fragment fixture; ShouldWire should be true")
	}
}

// A file whose deployed content matches origin/main exactly is Current --
// even when it is untracked at HEAD, the exact shape command_guard.py was
// in during the three-file partial deploy.
func TestCurrentFileMatchesUpstreamEvenWhenUntrackedAtHead(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"hooks/lib/common.sh": "#!/bin/sh\necho placeholder\n",
	})
	// A file present in the working tree but never committed -- HEAD's own
	// tree has no entry for it at all.
	if err := os.WriteFile(filepath.Join(dir, "hooks/lib/command_guard.py"), []byte("print('parser')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newBlob := blobSHA(t, dir, "hooks/lib/command_guard.py")
	head := headSHA(t, dir)

	up := &fakeUpstream{
		mainSHA: "deadbeef",
		tree: map[string]string{
			"hooks/lib/command_guard.py": newBlob,
		},
		files:        map[string][]byte{"main/settings/claude/settings.json": fragmentBytes(t, nil)},
		knownCommits: map[string]bool{head: true},
	}
	settings := writeSettings(t, dir, nil)

	rep, err := Compute(dir, settings, up)
	if err != nil {
		t.Fatal(err)
	}
	f := fileByPath(t, rep, "hooks/lib/command_guard.py")
	if f.State != Current {
		t.Fatalf("got state %q, want %q -- untracked-at-HEAD must not be confused with drifted", f.State, Current)
	}
	if f.HeadSHA != "" {
		t.Fatalf("HeadSHA should be empty for a file HEAD never had, got %q", f.HeadSHA)
	}
}

// A file edited by hand in the live checkout -- matching neither HEAD nor
// origin/main -- is Drifted. ledger-write-guard.sh's real, confirmed shape.
func TestDriftedFileMatchesNeitherHeadNorUpstream(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"hooks/ledger-write-guard.sh": "#!/bin/sh\necho committed\n",
	})
	head := headSHA(t, dir)
	// Hand-edit after the commit -- never staged, never committed.
	if err := os.WriteFile(filepath.Join(dir, "hooks/ledger-write-guard.sh"), []byte("#!/bin/sh\necho hand-patched\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	up := &fakeUpstream{
		mainSHA:      "deadbeef",
		tree:         map[string]string{"hooks/ledger-write-guard.sh": "someotherupstreamsha"},
		files:        map[string][]byte{"main/settings/claude/settings.json": fragmentBytes(t, nil)},
		knownCommits: map[string]bool{head: true},
	}
	settings := writeSettings(t, dir, nil)

	rep, err := Compute(dir, settings, up)
	if err != nil {
		t.Fatal(err)
	}
	f := fileByPath(t, rep, "hooks/ledger-write-guard.sh")
	if f.State != Drifted {
		t.Fatalf("got state %q, want %q", f.State, Drifted)
	}
}

// A file origin/main has and the checkout does not is Absent -- #357's own
// shape (keychain-write-guard.sh), which a diff of only locally-present
// files would never surface.
func TestAbsentFileExistsUpstreamOnly(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"hooks/main-branch-guard.sh": "#!/bin/sh\necho present\n",
	})
	head := headSHA(t, dir)

	up := &fakeUpstream{
		mainSHA: "deadbeef",
		tree: map[string]string{
			"hooks/main-branch-guard.sh":    blobSHA(t, dir, "hooks/main-branch-guard.sh"),
			"hooks/keychain-write-guard.sh": "keychainblobsha",
		},
		files: map[string][]byte{
			"main/settings/claude/settings.json": fragmentBytes(t, []string{"hooks/main-branch-guard.sh", "hooks/keychain-write-guard.sh"}),
		},
		knownCommits: map[string]bool{head: true},
	}
	settings := writeSettings(t, dir, []string{filepath.Join(dir, "hooks/main-branch-guard.sh")})

	rep, err := Compute(dir, settings, up)
	if err != nil {
		t.Fatal(err)
	}
	f := fileByPath(t, rep, "hooks/keychain-write-guard.sh")
	if f.State != Absent {
		t.Fatalf("got state %q, want %q", f.State, Absent)
	}
	if f.Wired {
		t.Fatal("a file that does not exist locally cannot be Wired")
	}
	if !f.ShouldWire {
		t.Fatal("origin/main's own fragment fixture registers keychain-write-guard.sh; ShouldWire should be true")
	}
}

// A checkout HEAD that GitHub has never seen (agent-dotfiles' real a3f6e09)
// is reported as such, distinctly from "behind".
func TestHeadNeverPushedIsReportedNotHiddenAsBehind(t *testing.T) {
	dir := gitRepo(t, map[string]string{"hooks/x.sh": "echo x\n"})

	up := &fakeUpstream{
		mainSHA:      "deadbeef",
		tree:         map[string]string{"hooks/x.sh": blobSHA(t, dir, "hooks/x.sh")},
		files:        map[string][]byte{"main/settings/claude/settings.json": fragmentBytes(t, nil)},
		knownCommits: map[string]bool{}, // HEAD deliberately absent -- never pushed
	}
	settings := writeSettings(t, dir, nil)

	rep, err := Compute(dir, settings, up)
	if err != nil {
		t.Fatal(err)
	}
	if rep.HeadPushed {
		t.Fatal("HeadPushed should be false when CommitExists reports the commit unknown to GitHub")
	}
}

// Compute must never shell out to `git fetch`, `git pull`, or anything
// that writes to the checkout -- it is read-only by contract. This does
// not prove absence of every possible mutating call, but it does assert
// the one observable a caller can check without instrumenting exec.Command
// itself: the working tree and HEAD are byte-for-byte unchanged after a
// full Compute run.
func TestComputeNeverMutatesTheCheckout(t *testing.T) {
	dir := gitRepo(t, map[string]string{"hooks/x.sh": "echo x\n"})
	head := headSHA(t, dir)
	before, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}

	up := &fakeUpstream{
		mainSHA:      "deadbeef",
		tree:         map[string]string{"hooks/x.sh": blobSHA(t, dir, "hooks/x.sh")},
		files:        map[string][]byte{"main/settings/claude/settings.json": fragmentBytes(t, nil)},
		knownCommits: map[string]bool{head: true},
	}
	settings := writeSettings(t, dir, nil)

	if _, err := Compute(dir, settings, up); err != nil {
		t.Fatal(err)
	}

	after, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("Compute changed the checkout's own git status: before=%q after=%q", before, after)
	}
	if headSHA(t, dir) != head {
		t.Fatal("Compute moved HEAD")
	}
}

// A real read failure asking origin/main's settings fragment (not a 404)
// must fail Compute outright, never silently read as "nothing should be
// wired" -- ErrNotFound is the only tolerated absence.
func TestRealUpstreamErrorFailsRatherThanReadingAsAbsent(t *testing.T) {
	dir := gitRepo(t, map[string]string{"hooks/x.sh": "echo x\n"})
	head := headSHA(t, dir)

	up := &erroringSettingsUpstream{
		fakeUpstream: fakeUpstream{
			mainSHA:      "deadbeef",
			tree:         map[string]string{"hooks/x.sh": blobSHA(t, dir, "hooks/x.sh")},
			knownCommits: map[string]bool{head: true},
		},
	}
	settings := writeSettings(t, dir, nil)

	if _, err := Compute(dir, settings, up); err == nil {
		t.Fatal("Compute should fail when reading origin/main's settings fragment fails for a reason other than 404")
	}
}

type erroringSettingsUpstream struct{ fakeUpstream }

func (e *erroringSettingsUpstream) File(repo, ref, path string) ([]byte, error) {
	if path == SettingsFragmentPath {
		return nil, errors.New("gh: rate limited")
	}
	return e.fakeUpstream.File(repo, ref, path)
}

// AheadBy is trusted only when the checkout's own local origin/main ref is
// corroborated fresh against GitHub's live main SHA -- a stale local ref
// (this package never fetches to refresh it) must report unknown, never a
// wrong number.
func TestAheadByUnknownWhenLocalRefIsStale(t *testing.T) {
	dir := gitRepo(t, map[string]string{"hooks/x.sh": "echo x\n"})
	head := headSHA(t, dir)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// A local origin/main ref exists but does not match what GitHub
	// reports live -- e.g. never fetched since a force-push, or simply old.
	run("update-ref", "refs/remotes/origin/main", head)

	n, known, note := aheadCount(dir, head, "some-different-live-sha")
	if known {
		t.Fatalf("aheadCount reported known=true off a stale local ref (n=%d, note=%q)", n, note)
	}
	if note == "" {
		t.Fatal("aheadCount reported unknown with no explanation")
	}
}

// The mirror case: a local origin/main ref that DOES match GitHub's live
// main SHA is trusted, and the count comes from ordinary local git history.
func TestAheadByKnownWhenLocalRefIsFresh(t *testing.T) {
	dir := gitRepo(t, map[string]string{"hooks/x.sh": "echo x\n"})
	head := headSHA(t, dir)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks/y.sh"), []byte("echo y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("checkout", "-qb", "scratch-upstream")
	run("add", "-A")
	run("commit", "-qm", "one commit ahead")
	upstreamHead := headSHA(t, dir)
	run("update-ref", "refs/remotes/origin/main", upstreamHead)
	run("checkout", "-q", "main")

	n, known, note := aheadCount(dir, head, upstreamHead)
	if !known {
		t.Fatalf("aheadCount reported known=false with a fresh, corroborated local ref: %s", note)
	}
	if n != 1 {
		t.Fatalf("got ahead-by %d, want 1", n)
	}
}
