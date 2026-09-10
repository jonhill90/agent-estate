package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// filterEnv returns os.Environ() with the named variables removed --
// used here so this test's own ambient environment (whatever a developer
// or CI happens to have set for ESTATE_KNOWLEDGE_INDEX or
// ESTATE_KNOWLEDGE_ALLOW_SHARED_WRITE) can never leak into a test that
// specifically needs both absent to exercise the real fall-through path.
func filterEnv(remove ...string) []string {
	drop := map[string]bool{}
	for _, r := range remove {
		drop[r] = true
	}
	var out []string
	for _, kv := range os.Environ() {
		name := strings.SplitN(kv, "=", 2)[0]
		if !drop[name] {
			out = append(out, kv)
		}
	}
	return out
}

// realSharedIndexSnapshot reads the actual, real shared knowledge index
// path's existence and content -- never fabricated, never a fixture --
// so a test that must reproduce the genuine cwd-fallthrough (which always
// resolves to this exact home-relative path, unlike ResolveWritePath's
// other callers, which can be redirected via ESTATE_KNOWLEDGE_INDEX) can
// prove it left that real file exactly as found, whether or not it exists
// at all on this host.
type sharedIndexSnapshot struct {
	path   string
	exists bool
	bytes  []byte // nil when !exists
}

func snapshotRealSharedIndex(t *testing.T) sharedIndexSnapshot {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}
	path := filepath.Join(home, ".local", "state", "agent-estate", "knowledge", "index.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sharedIndexSnapshot{path: path, exists: false}
		}
		t.Fatalf("read real shared index %s: %v", path, err)
	}
	return sharedIndexSnapshot{path: path, exists: true, bytes: b}
}

func (s sharedIndexSnapshot) assertUnchanged(t *testing.T) {
	t.Helper()
	after := snapshotRealSharedIndex(t)
	if after.exists != s.exists {
		t.Fatalf("real shared index existence changed: was exists=%v, now exists=%v (%s) -- this test must never write it", s.exists, after.exists, s.path)
	}
	if s.exists && string(after.bytes) != string(s.bytes) {
		t.Fatalf("real shared index content changed at %s -- this test must never write it", s.path)
	}
}

// TestKnowledgeSharedWriteRefusalNamesThePrivateRemedy is agent-estate#1184's
// own "prove it" requirement, driven through the real compiled binary from
// a real, unrecognised cwd -- a plain t.TempDir(), the same shape a
// reviewer's ad-hoc `git worktree add /tmp/...` has, not a fixture and not
// a dispatch worktree isolate.Create ever made.
//
// internal/knowledge/write_test.go's own
// TestResolveWritePathRequiresAckForAnUnrecognisedCwd already proves the
// pure resolution refuses (requiresAck=true); it does not touch main.go's
// printed message at all. This test is the CLI-level complement: it
// exercises the actual refusal a human or agent would read, and checks it
// against agent-estate#1326's own established remedy phrasing
// (knowledge.PrivateIndexRemedy) rather than a second, hand-typed copy of
// "point ESTATE_KNOWLEDGE_INDEX at a private path."
//
// FAILS on main before this change: the refusal fired (exit 1, no write --
// that half of #1184 was already fixed by #1185) but its message only
// named the `--allow-shared-write` override, never the safe remedy #1326
// established -- a caller refused here had to already know the workaround
// existed. PASSES after: the message contains knowledge.PrivateIndexRemedy
// verbatim.
func TestKnowledgeSharedWriteRefusalNamesThePrivateRemedy(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir() // NOT under TMPDIR/estate-dispatch/... -- an ad-hoc, unrecognised checkout

	before := snapshotRealSharedIndex(t)

	cmd := exec.Command(bin, "knowledge")
	cmd.Dir = dir
	cmd.Env = filterEnv(knowledge.KnowledgeIndexEnv, knowledge.AllowSharedWriteEnv)
	out, runErr := cmd.CombinedOutput()

	before.assertUnchanged(t)

	code := 0
	if runErr != nil {
		exitErr, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("run estate knowledge: %v\n%s", runErr, out)
		}
		code = exitErr.ExitCode()
	}
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 -- an unrecognised cwd must refuse to write the shared index, not silently succeed (agent-estate#1184)\n%s", code, out)
	}
	if !strings.Contains(string(out), knowledge.PrivateIndexRemedy) {
		t.Fatalf("refusal message does not name knowledge.PrivateIndexRemedy -- it must match agent-estate#1326's established remedy wording rather than inventing a second phrasing:\n%s", out)
	}
}

// The brief's other "prove it" half -- a recognised cwd still resolves as
// before -- is deliberately NOT a new test here. An earlier version of
// this file drove `estate knowledge --allow-shared-write` through a real
// explicit override to prove that end to end, and it failed in CI: that
// path runs knowledge.Generate() for real, which needs all four of
// vault/corpus/github-stars/Loops-Research -- structurally absent on a
// public runner the same way AGENT_MEMORY_VAULT and the corpus are for
// internal/corpus/standinglaw_live_test.go (agent-estate#1329) and
// cmd/goldenquery/live_ratchet_test.go (agent-estate#1210/#1335) -- and
// the brief itself already says why not to do this: "If your test needs
// to observe the resolution, assert on the returned path, do not perform
// the write."
//
// The resolution itself needs no write to prove, and is already fully
// covered, safely, at the unit level by
// internal/knowledge/write_test.go's TestResolveWritePathNeverRequiresAckWithAnExplicitOverride
// and TestResolveWritePathNeverRequiresAckInsideADispatchedTurn -- both
// assert requiresAck directly, neither ever calls Generate/Write. This
// change edits only the text printed inside the `if requiresAck { ... }`
// block, a branch the override path's requiresAck=false never enters, so
// those two tests already prove the override path is structurally
// untouched by it.
