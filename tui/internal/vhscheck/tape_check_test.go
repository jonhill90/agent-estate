package vhscheck

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFixtureRepo builds a throwaway repo shape -- testdata/vhs/*.tape
// plus whatever cmd/ directories are listed -- and returns its root.
func writeFixtureRepo(t *testing.T, tapes map[string]string, cmdDirs []string) string {
	t.Helper()
	root := t.TempDir()
	tapesDir := filepath.Join(root, "testdata", "vhs")
	if err := os.MkdirAll(tapesDir, 0o755); err != nil {
		t.Fatalf("mkdir tapesDir: %v", err)
	}
	for name, content := range tapes {
		path := filepath.Join(tapesDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	for _, name := range cmdDirs {
		if err := os.MkdirAll(filepath.Join(root, "cmd", name), 0o755); err != nil {
			t.Fatalf("mkdir cmd/%s: %v", name, err)
		}
	}
	return root
}

// --- the two mandatory mutation-check directions --------------------

func TestMissingCmdDirsFlagsATapePointingAtAMissingBinary(t *testing.T) {
	root := writeFixtureRepo(t, map[string]string{
		"broken.tape": "Hide\n" +
			`Type "go build -o /tmp/x ./cmd/doesnotexist && clear" Enter` + "\n" +
			"Show\n",
	}, nil) // no cmd/ dirs at all -- doesnotexist genuinely doesn't exist

	refs, err := ScanTapes(filepath.Join(root, "testdata", "vhs"))
	if err != nil {
		t.Fatalf("ScanTapes: %v", err)
	}
	if len(refs) != 1 || refs[0].CmdName != "doesnotexist" {
		t.Fatalf("ScanTapes found %+v, want one reference to doesnotexist", refs)
	}

	missing := MissingCmdDirs(root, refs)
	if len(missing) != 1 {
		t.Fatalf("MissingCmdDirs = %+v, want exactly one missing reference (proves the guard by construction, not by reading it)", missing)
	}
	if missing[0].TapePath != filepath.Join("vhs", "broken.tape") || missing[0].CmdName != "doesnotexist" {
		t.Fatalf("MissingCmdDirs reported the wrong reference: %+v", missing[0])
	}
}

func TestMissingCmdDirsPassesATapePointingAtARealBinary(t *testing.T) {
	root := writeFixtureRepo(t, map[string]string{
		"valid.tape": "Hide\n" +
			`Type "go build -o /tmp/x ./cmd/realthing && clear" Enter` + "\n" +
			"Show\n",
	}, []string{"realthing"}) // cmd/realthing genuinely exists this time

	refs, err := ScanTapes(filepath.Join(root, "testdata", "vhs"))
	if err != nil {
		t.Fatalf("ScanTapes: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("ScanTapes found %+v, want exactly one reference", refs)
	}

	missing := MissingCmdDirs(root, refs)
	if len(missing) != 0 {
		t.Fatalf("MissingCmdDirs = %+v, want none -- a tape pointing at a real binary must pass", missing)
	}
}

// --- precision: a comment mentioning a path is not a build dependency --

func TestACommentMentioningAPathWithNoGoBuildOnTheSameLineIsNotAReference(t *testing.T) {
	// The exact shape agent-tui#132 found in the wild: knowledge-route.tape
	// (before this fix) described knowledge.tape's own build target in
	// prose -- "against cmd/knowledgedemo" -- with no "go build" on that
	// line. A naive substring scan for "cmd/knowledgedemo" anywhere in the
	// file would have flagged this forever, including for a tape that
	// never actually depends on that path being buildable.
	root := writeFixtureRepo(t, map[string]string{
		"describes-a-sibling.tape": "# This is what a sibling tape drives, standalone, against cmd/doesnotexist.\n" +
			`Type "go build -o /tmp/x ./cmd/realthing && clear" Enter` + "\n",
	}, []string{"realthing"})

	refs, err := ScanTapes(filepath.Join(root, "testdata", "vhs"))
	if err != nil {
		t.Fatalf("ScanTapes: %v", err)
	}
	if len(refs) != 1 || refs[0].CmdName != "realthing" {
		t.Fatalf("ScanTapes found %+v, want exactly one reference (realthing) -- the prose mention must not be scanned as a dependency", refs)
	}
}

func TestACommentedGoBuildLineIsStillAReference(t *testing.T) {
	// The other real shape in this repo: chat-send.tape documents its
	// prerequisite build commands as `#`-prefixed comments (built by the
	// operator before running vhs, not by a live Type line) -- still a
	// genuine dependency this tape cannot run without, and must still be
	// caught if the target ever goes missing.
	root := writeFixtureRepo(t, map[string]string{
		"documented-prereq.tape": "# Run:\n" +
			"#   go build -o /tmp/x ./cmd/prereq\n" +
			`Type "/tmp/x" Enter` + "\n",
	}, nil)

	refs, err := ScanTapes(filepath.Join(root, "testdata", "vhs"))
	if err != nil {
		t.Fatalf("ScanTapes: %v", err)
	}
	if len(refs) != 1 || refs[0].CmdName != "prereq" {
		t.Fatalf("ScanTapes found %+v, want the commented prerequisite build to still be caught", refs)
	}
	missing := MissingCmdDirs(root, refs)
	if len(missing) != 1 {
		t.Fatalf("MissingCmdDirs = %+v, want the missing prereq flagged", missing)
	}
}

// --- agent-estate#754: exported filesystem paths, not just cmd/ targets --

func TestMissingExportedPathsFlagsATapeExportingAMissingPath(t *testing.T) {
	root := writeFixtureRepo(t, map[string]string{
		"broken-export.tape": "Hide\n" +
			`Type "export FOO=${FOO:-./scripts/does-not-exist}" Enter` + "\n" +
			"Show\n",
	}, nil)

	exports, err := ScanExportedPaths(filepath.Join(root, "testdata", "vhs"))
	if err != nil {
		t.Fatalf("ScanExportedPaths: %v", err)
	}
	if len(exports) != 1 || exports[0].VarName != "FOO" {
		t.Fatalf("ScanExportedPaths found %+v, want one FOO export", exports)
	}

	missing := MissingExportedPaths(root, "", exports, nil)
	if len(missing) != 1 {
		t.Fatalf("MissingExportedPaths = %+v, want exactly one missing export (proves the guard by construction, not by reading it)", missing)
	}
	t.Logf("changed line: %s:%d %q", missing[0].TapePath, missing[0].Line, `Type "export FOO=${FOO:-./scripts/does-not-exist}" Enter`)
}

func TestMissingExportedPathsPassesATapeExportingARealPath(t *testing.T) {
	root := writeFixtureRepo(t, map[string]string{
		"good-export.tape": "Hide\n" +
			`Type "export FOO=${FOO:-./cmd/realthing}" Enter` + "\n" +
			"Show\n",
	}, []string{"realthing"})
	if err := os.MkdirAll(filepath.Join(root, "cmd", "realthing"), 0o755); err != nil {
		t.Fatalf("mkdir cmd/realthing: %v", err)
	}

	exports, err := ScanExportedPaths(filepath.Join(root, "testdata", "vhs"))
	if err != nil {
		t.Fatalf("ScanExportedPaths: %v", err)
	}

	missing := MissingExportedPaths(root, "", exports, nil)
	if len(missing) != 0 {
		t.Fatalf("MissingExportedPaths = %+v, want none -- an export pointing at a real directory must pass", missing)
	}
	t.Logf("reverted line still passes: %s:%d %q", exports[0].TapePath, exports[0].Line, `Type "export FOO=${FOO:-./cmd/realthing}" Enter`)
}

func TestMissingExportedPathsSkipsADerivedDefault(t *testing.T) {
	// The exact shape agent-estate#754 repointed AGENT_SUPERVISOR_REPO to:
	// a `$(...)` command substitution has nothing static to check, and
	// must never be flagged just because the literal text isn't a path
	// that exists on disk.
	root := writeFixtureRepo(t, map[string]string{
		"derived.tape": "Hide\n" +
			`Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}" Enter` + "\n" +
			"Show\n",
	}, nil)

	exports, err := ScanExportedPaths(filepath.Join(root, "testdata", "vhs"))
	if err != nil {
		t.Fatalf("ScanExportedPaths: %v", err)
	}
	if len(exports) != 1 || !exports[0].IsDerived() {
		t.Fatalf("ScanExportedPaths found %+v, want one derived AGENT_SUPERVISOR_REPO export", exports)
	}

	missing := MissingExportedPaths(root, "", exports, nil)
	if len(missing) != 0 {
		t.Fatalf("MissingExportedPaths = %+v, want none -- a derived default is out of scope for existence-checking", missing)
	}
}

func TestMissingExportedPathsSkipsAnOptionalVar(t *testing.T) {
	// HILL90_APP_REPO's own shape in this repo today: a literal $HOME
	// default pointing at a sibling checkout that is genuinely absent in
	// CI (tui-ci.yml never sets it) and whose own downstream code
	// (internal/apidocs, internal/secrets) already treats absence as a
	// supported "not configured" state. The named allowlist is what keeps
	// that legitimate case from being flagged alongside a genuine
	// regression.
	root := writeFixtureRepo(t, map[string]string{
		"optional.tape": "Hide\n" +
			`Type "export HILL90_APP_REPO=${HILL90_APP_REPO:-$HOME/source/repos/Personal/hill90-app}" Enter` + "\n" +
			"Show\n",
	}, nil)

	exports, err := ScanExportedPaths(filepath.Join(root, "testdata", "vhs"))
	if err != nil {
		t.Fatalf("ScanExportedPaths: %v", err)
	}
	if len(exports) != 1 || exports[0].VarName != "HILL90_APP_REPO" {
		t.Fatalf("ScanExportedPaths found %+v, want one HILL90_APP_REPO export", exports)
	}

	// No home dir supplied and the allowlist names the var -- either alone
	// would suppress the flag; both apply here, matching the real check.
	missing := MissingExportedPaths(root, "/nonexistent-home", exports, map[string]bool{"HILL90_APP_REPO": true})
	if len(missing) != 0 {
		t.Fatalf("MissingExportedPaths = %+v, want none -- HILL90_APP_REPO is an explicit, documented exemption", missing)
	}
}

// --- the real repo: this is the guard that actually runs in CI ---------

func TestNoTapeReferencesAMissingCmdDirectory(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	tapesDir := filepath.Join(repoRoot, "testdata", "vhs")
	if _, err := os.Stat(tapesDir); err != nil {
		t.Fatalf("testdata/vhs not found at %s: %v", tapesDir, err)
	}

	refs, err := ScanTapes(tapesDir)
	if err != nil {
		t.Fatalf("ScanTapes: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("ScanTapes found zero go-build references across every .tape file -- almost certainly a scan bug, not reality (this repo has many tapes that build cmd/estate alone)")
	}

	missing := MissingCmdDirs(repoRoot, refs)
	for _, m := range missing {
		t.Errorf("%s:%d references %s, which does not exist -- go build inside this tape fails silently, producing no screenshot (agent-tui#130/agent-tui#132)",
			m.TapePath, m.Line, m.CmdDir())
	}
}

// optionalTapeExportVars is the named, explicit exemption list
// MissingExportedPaths's own doc comment requires -- see there for why
// HILL90_APP_REPO belongs on it. Add a new entry only with the same kind
// of citation, never by widening ScanExportedPaths/resolvePathExport to
// stop seeing a var instead.
var optionalTapeExportVars = map[string]bool{
	"HILL90_APP_REPO": true,
}

func TestNoTapeExportsAMissingFilesystemPath(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	tapesDir := filepath.Join(repoRoot, "testdata", "vhs")
	if _, err := os.Stat(tapesDir); err != nil {
		t.Fatalf("testdata/vhs not found at %s: %v", tapesDir, err)
	}

	exports, err := ScanExportedPaths(tapesDir)
	if err != nil {
		t.Fatalf("ScanExportedPaths: %v", err)
	}
	if len(exports) == 0 {
		t.Fatal("ScanExportedPaths found zero exported-path lines across every .tape file -- almost certainly a scan bug, not reality (this repo's tapes export AGENT_SUPERVISOR_REPO throughout)")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Logf("os.UserHomeDir failed (%v) -- every $HOME-prefixed export is skipped this run, not falsely flagged", err)
		home = ""
	}

	missing := MissingExportedPaths(repoRoot, home, exports, optionalTapeExportVars)
	for _, m := range missing {
		t.Errorf("%s:%d exports %s=%s, which does not exist -- agent-estate#754's own failure shape, a hardcoded host path going stale on the next rename",
			m.TapePath, m.Line, m.VarName, m.Default)
	}
}
