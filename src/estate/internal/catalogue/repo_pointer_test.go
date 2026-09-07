package catalogue

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initGitRepo creates a minimal real git repository at dir, so
// observeRepoPointer has a real `git rev-parse HEAD` to run against --
// never a hand-invented commit hash.
func initGitRepo(t *testing.T, dir string) string {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "fixture commit")

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return string(out)
}

// TestRegisterRepoPointer_CarriesBothLocatorFieldsSeparately is P8's own
// core acceptance test: remote_url and local_path are both recorded, as
// two distinct fields, alongside Locator -- never merged, never one
// derived from the other.
func TestRegisterRepoPointer_CarriesBothLocatorFieldsSeparately(t *testing.T) {
	registerDir := t.TempDir()
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	reg := &Register{}
	in := RegisterInput{
		Kind:            "kind/repo",
		ExtractionKind:  ExtractionRepoPointer,
		Locator:         "https://github.com/jonhill90/fixture-repo",
		RemoteURL:       "https://github.com/jonhill90/fixture-repo",
		LocalPath:       repoDir,
		RepoDescription: "fixture repo for P8 tests",
		RoutingSurface:  "AGENTS.md",
		WhyIndexed:      "P8 fixture",
	}
	e, created := reg.Register(registerDir, in, time.Now())
	if !created {
		t.Fatal("Register: created = false, want true")
	}
	if e.RemoteURL != in.RemoteURL {
		t.Fatalf("RemoteURL = %q, want %q", e.RemoteURL, in.RemoteURL)
	}
	if e.LocalPath != in.LocalPath {
		t.Fatalf("LocalPath = %q, want %q", e.LocalPath, in.LocalPath)
	}
	if e.RepoDescription != in.RepoDescription {
		t.Fatalf("RepoDescription = %q, want %q", e.RepoDescription, in.RepoDescription)
	}
	if e.RoutingSurface != in.RoutingSurface {
		t.Fatalf("RoutingSurface = %q, want %q", e.RoutingSurface, in.RoutingSurface)
	}
	if e.LocalCheckoutStatus != LocalCheckoutPresent {
		t.Fatalf("LocalCheckoutStatus = %v, want LocalCheckoutPresent (repoDir is a real checkout)", e.LocalCheckoutStatus)
	}
	if e.ObservedRevision == "" {
		t.Fatal("ObservedRevision is empty, want the real checkout's HEAD commit")
	}
}

// TestRepoPointer_MissingLocalCheckoutIsRecordedStateNotError is P8's
// binding rule verbatim: a repo registered with no local checkout (or
// one that has since vanished) must resolve to LocalCheckoutAbsent, a
// typed state, never an error and never an empty string that could be
// misread as "no path was ever declared."
func TestRepoPointer_MissingLocalCheckoutIsRecordedStateNotError(t *testing.T) {
	registerDir := t.TempDir()
	reg := &Register{}
	in := RegisterInput{
		Kind:           "kind/repo",
		ExtractionKind: ExtractionRepoPointer,
		Locator:        "https://github.com/jonhill90/never-cloned-here",
		RemoteURL:      "https://github.com/jonhill90/never-cloned-here",
		LocalPath:      "", // never registered a local checkout at all
		WhyIndexed:     "P8 fixture: no local checkout",
	}
	e, created := reg.Register(registerDir, in, time.Now())
	if !created {
		t.Fatal("Register: created = false, want true")
	}
	if e.LocalCheckoutStatus != LocalCheckoutAbsent {
		t.Fatalf("LocalCheckoutStatus = %v, want LocalCheckoutAbsent", e.LocalCheckoutStatus)
	}
	if e.ExtractionCachePath != "" {
		t.Fatalf("ExtractionCachePath = %q, want empty -- a repo-pointer never extracts file content", e.ExtractionCachePath)
	}

	// A LocalPath naming a real-looking but nonexistent directory must
	// resolve the same way -- absent, not an error.
	in2 := in
	in2.LocalPath = filepath.Join(registerDir, "does-not-exist-on-this-machine")
	reg2 := &Register{}
	e2, _ := reg2.Register(registerDir, in2, time.Now())
	if e2.LocalCheckoutStatus != LocalCheckoutAbsent {
		t.Fatalf("LocalCheckoutStatus (nonexistent path) = %v, want LocalCheckoutAbsent", e2.LocalCheckoutStatus)
	}
	if e2.LocalPath != in2.LocalPath {
		t.Fatalf("LocalPath = %q, want the last-known path %q preserved even though absent", e2.LocalPath, in2.LocalPath)
	}
}

// TestRepoPointer_NeverDerivesOneLocatorFromTheOther constructs an entry
// with a RemoteURL that shares nothing in common with LocalPath, and
// confirms Register never tries to "fix" LocalPath from RemoteURL or vice
// versa -- both fields end up exactly what the caller supplied, nothing
// computed from the other.
func TestRepoPointer_NeverDerivesOneLocatorFromTheOther(t *testing.T) {
	registerDir := t.TempDir()
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	reg := &Register{}
	in := RegisterInput{
		Kind:           "kind/repo",
		ExtractionKind: ExtractionRepoPointer,
		Locator:        "https://github.com/jonhill90/totally-unrelated-name",
		RemoteURL:      "https://github.com/jonhill90/totally-unrelated-name",
		LocalPath:      repoDir, // deliberately shares no substring with RemoteURL
	}
	e, _ := reg.Register(registerDir, in, time.Now())
	if e.RemoteURL != in.RemoteURL || e.LocalPath != in.LocalPath {
		t.Fatalf("fields were not preserved verbatim: RemoteURL=%q LocalPath=%q", e.RemoteURL, e.LocalPath)
	}
	// The point: this must not panic, error, or silently rewrite either
	// field to match the other -- the assertions above are the whole
	// test.
}

// TestRepoPointer_Refresh_ReObservesExistingPathOnly proves Refresh
// re-checks the ALREADY-recorded LocalPath rather than searching for one
// or deriving one from RemoteURL -- and that a checkout going from
// present to absent between Register and Refresh correctly flips the
// typed state.
func TestRepoPointer_Refresh_ReObservesExistingPathOnly(t *testing.T) {
	registerDir := t.TempDir()
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	reg := &Register{}
	in := RegisterInput{
		Kind:           "kind/repo",
		ExtractionKind: ExtractionRepoPointer,
		Locator:        "https://github.com/jonhill90/fixture-goes-away",
		RemoteURL:      "https://github.com/jonhill90/fixture-goes-away",
		LocalPath:      repoDir,
	}
	e, _ := reg.Register(registerDir, in, time.Now())
	if e.LocalCheckoutStatus != LocalCheckoutPresent {
		t.Fatalf("initial LocalCheckoutStatus = %v, want present", e.LocalCheckoutStatus)
	}

	// Simulate the checkout being removed from this machine (moved, deleted)
	if err := os.RemoveAll(repoDir); err != nil {
		t.Fatal(err)
	}
	refreshed, ok := reg.Refresh(registerDir, e.ID, time.Now())
	if !ok {
		t.Fatal("Refresh: entry not found")
	}
	if refreshed.LocalCheckoutStatus != LocalCheckoutAbsent {
		t.Fatalf("LocalCheckoutStatus after removal = %v, want LocalCheckoutAbsent", refreshed.LocalCheckoutStatus)
	}
	if refreshed.LocalPath != repoDir {
		t.Fatalf("LocalPath = %q, want the last-known path %q preserved, not cleared", refreshed.LocalPath, repoDir)
	}
	if refreshed.RemoteURL != in.RemoteURL {
		t.Fatalf("RemoteURL changed across Refresh: %q, want unchanged %q", refreshed.RemoteURL, in.RemoteURL)
	}
}

// TestExistingKindsUnaffectedByRepoPointerFields is the identity-trap
// regression test the Director's own note demands: registering an
// ordinary repo-docs/pdf/conversation entry must produce byte-identical
// behavior to before P8 -- empty repo-pointer-only fields, LocalCheckoutStatus
// omitted (empty), and -- the actual trap -- identityFor's output for a
// given Locator must be UNCHANGED.
func TestExistingKindsUnaffectedByRepoPointerFields(t *testing.T) {
	// This is the literal identity the Director's note is protecting:
	// identityFor(locator) must still be "src-"+sha256(locator)[:8], the
	// Locator string alone, exactly as before P8. A hardcoded golden
	// value here (computed independently, once, via Python's own
	// hashlib -- not by calling this package) would catch identityFor's
	// signature or hash input changing silently, e.g. becoming a
	// composite of Locator+RemoteURL+LocalPath.
	const fixture = "test-locator-for-identity-regression"
	const wantID = "src-b6fe96898b720fe5" // sha256(fixture)[:8], computed independently
	if got := identityFor(fixture); got != wantID {
		t.Fatalf("identityFor(%q) = %q, want %q -- this IS the P8 identity trap the Director's note warns about: identityFor's input changed", fixture, got, wantID)
	}

	dir := t.TempDir()
	docPath := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(docPath, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := &Register{}
	e, _ := reg.Register(dir, RegisterInput{Kind: "kind/doc", ExtractionKind: ExtractionRepoDocs, Locator: docPath}, time.Now())
	if e.RemoteURL != "" || e.LocalPath != "" || e.RepoDescription != "" || e.RoutingSurface != "" {
		t.Fatalf("repo-pointer-only fields leaked into a repo-docs entry: %+v", e)
	}
	if e.LocalCheckoutStatus != "" {
		t.Fatalf("LocalCheckoutStatus = %q, want empty (not applicable) for a non-repo-pointer kind", e.LocalCheckoutStatus)
	}
}

// TestObserveRepoPointer_RealGitCheckout proves the revision comes from
// a real `git rev-parse HEAD`, not an invented value -- checked against
// the fixture's own independently-obtained HEAD.
func TestObserveRepoPointer_RealGitCheckout(t *testing.T) {
	repoDir := t.TempDir()
	wantHead := strings.TrimSpace(initGitRepo(t, repoDir))

	revision, status, checkoutState := observeRepoPointer(repoDir)
	if checkoutState != LocalCheckoutPresent {
		t.Fatalf("checkoutState = %v, want LocalCheckoutPresent", checkoutState)
	}
	if revision != wantHead {
		t.Fatalf("revision = %q, want the real HEAD %q", revision, wantHead)
	}
	if strings.Contains(status, "extracted:") {
		t.Fatalf("status = %q, want it to say 'not applicable' -- a repo-pointer never extracts file content", status)
	}
}

// TestObserveRepoPointer_EmptyLocalPathIsAbsentNotError is the exact
// typed-absence acceptance case P8 names.
func TestObserveRepoPointer_EmptyLocalPathIsAbsentNotError(t *testing.T) {
	revision, status, checkoutState := observeRepoPointer("")
	if checkoutState != LocalCheckoutAbsent {
		t.Fatalf("checkoutState = %v, want LocalCheckoutAbsent", checkoutState)
	}
	if revision != "" {
		t.Fatalf("revision = %q, want empty", revision)
	}
	if !strings.Contains(status, "not applicable") {
		t.Fatalf("status = %q, want 'not applicable', not an error phrasing", status)
	}
}

// TestObserveRepoPointer_NotAGitRepoReportsCouldNotExtract covers a
// local_path that exists but is not itself a git checkout -- distinct
// from LocalCheckoutAbsent (no directory at all): the directory IS
// present, so this is "could not extract", not "absent".
func TestObserveRepoPointer_NotAGitRepoReportsCouldNotExtract(t *testing.T) {
	dir := t.TempDir() // exists, but git init was never run here
	revision, status, checkoutState := observeRepoPointer(dir)
	if checkoutState != LocalCheckoutPresent {
		t.Fatalf("checkoutState = %v, want LocalCheckoutPresent -- the directory exists even though it's not a git repo", checkoutState)
	}
	if revision != "" {
		t.Fatalf("revision = %q, want empty (rev-parse failed)", revision)
	}
	if !strings.Contains(status, "could not extract") {
		t.Fatalf("status = %q, want 'could not extract'", status)
	}
}

// TestGenerateSourceView_RepoPointerCarriesBothLocatorFields is the
// view-generation half of P8's acceptance criteria -- the generated
// Markdown record must carry remote_url and local_path as separate
// frontmatter keys, plus repo_description and routing_surface.
func TestGenerateSourceView_RepoPointerCarriesBothLocatorFields(t *testing.T) {
	e := RegisterEntry{
		ID:                  "src-fixture",
		ViewID:              "SRC-2026-09-07-999",
		Kind:                "kind/repo",
		ExtractionKind:      ExtractionRepoPointer,
		Locator:             "https://github.com/jonhill90/fixture",
		RemoteURL:           "https://github.com/jonhill90/fixture",
		LocalPath:           "/Users/jon/source/repos/Personal/fixture",
		LocalCheckoutStatus: LocalCheckoutPresent,
		RepoDescription:     "a fixture repo",
		RoutingSurface:      "AGENTS.md",
		ReviewState:         DefaultReviewState,
		Status:              StatusActive,
	}
	out := GenerateSourceView(e, time.Now())
	for _, want := range []string{
		`remote_url: "https://github.com/jonhill90/fixture"`,
		`local_path: "/Users/jon/source/repos/Personal/fixture"`,
		"local_checkout_status: present",
		`repo_description: "a fixture repo"`,
		`routing_surface: "AGENTS.md"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("view missing %q:\n%s", want, out)
		}
	}
}

// TestGenerateSourceView_NonRepoPointerOmitsRepoFields proves the
// existing 24 views' shape is unaffected: a non-repo-pointer entry's
// generated view must NOT gain remote_url/local_path/etc lines.
func TestGenerateSourceView_NonRepoPointerOmitsRepoFields(t *testing.T) {
	out := GenerateSourceView(fixtureEntry(), time.Now())
	for _, absent := range []string{"remote_url:", "local_path:", "local_checkout_status:", "repo_description:", "routing_surface:"} {
		if strings.Contains(out, absent) {
			t.Fatalf("non-repo-pointer view unexpectedly contains %q:\n%s", absent, out)
		}
	}
}
