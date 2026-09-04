package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vaultViewFixtureDDL mirrors internal/knowledge's own vaultview_test.go
// fixture -- kept as a separate literal here rather than exported from that
// package, since this file drives the real compiled `estate vault-view`
// binary end to end (CLI dispatch, flag/arg handling, exit codes) rather
// than calling knowledge.GenerateVaultView directly, and a CLI-level test
// owns its own fixture rather than reaching into another package's
// unexported test helpers.
const vaultViewFixtureDDL = `
CREATE TABLE items (
  id TEXT PRIMARY KEY,
  prompt_id TEXT,
  kind TEXT,
  body TEXT,
  weight TEXT,
  status TEXT,
  status_reason TEXT,
  resolved_to TEXT,
  acked_at TEXT
);
INSERT INTO items (id, prompt_id, kind, body, weight, status, resolved_to) VALUES
  ('it-p1', 'mp-1', 'parameter', 'Prefer CLI-backed workflows.', 'hard', 'acted', 'tooling=cli_first'),
  ('it-d1', 'mp-3', 'directive', 'Do not touch Traefik before DNS.', 'hard', 'acted', NULL),
  ('it-q1', 'mp-5', 'question', 'When will these parameters be viewable?', 'hard', 'resolved', NULL);
`

func buildVaultViewCLIFixtureCorpus(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.sqlite3")
	cmd := exec.Command("sqlite3", path)
	cmd.Stdin = strings.NewReader(vaultViewFixtureDDL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 fixture setup failed: %v\n%s", err, out)
	}
	return path
}

func buildVaultViewCLIFixtureVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent", "facts"), 0o755); err != nil {
		t.Fatalf("mkdir facts: %v", err)
	}
	return dir
}

// TestVaultViewCommandWritesUnderAgentParametersOnly is the CLI-level
// regression for agent-estate#1084's own hard constraint: writes only to a
// new directory, never to agent/facts/ (vault.go's own read source).
func TestVaultViewCommandWritesUnderAgentParametersOnly(t *testing.T) {
	bin := buildEstateBinary(t)
	vault := buildVaultViewCLIFixtureVault(t)
	corpusPath := buildVaultViewCLIFixtureCorpus(t)
	dir := t.TempDir()

	cmd := exec.Command(bin, "vault-view")
	cmd.Env = append(os.Environ(),
		"AGENT_MEMORY_VAULT="+vault,
		"ESTATE_CORPUS="+corpusPath,
		"ESTATE_VAULT_BACKUP_ROOT="+filepath.Join(dir, "backups"),
		"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("estate vault-view: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "vault backed up to") {
		t.Fatalf("output does not report the backup location: %s", out)
	}
	if !strings.Contains(string(out), "written to") {
		t.Fatalf("output does not report where the view was written: %s", out)
	}

	paramsDir := filepath.Join(vault, "agent", "parameters")
	entries, err := os.ReadDir(paramsDir)
	if err != nil {
		t.Fatalf("read %s: %v", paramsDir, err)
	}
	if len(entries) == 0 {
		t.Fatal("agent/parameters/ is empty")
	}

	factsEntries, err := os.ReadDir(filepath.Join(vault, "agent", "facts"))
	if err != nil {
		t.Fatalf("read agent/facts/: %v", err)
	}
	if len(factsEntries) != 0 {
		t.Fatalf("agent/facts/ was written to -- must stay untouched, it is a read source for vault.go: %v", factsEntries)
	}
}

// TestVaultViewCommandRefusesExtraArgs guards against the command silently
// absorbing a typo'd flag or argument the way #1061 Finding 3 documented
// for `estate knowledge <typo>` before that was fixed.
func TestVaultViewCommandRefusesExtraArgs(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()

	cmd := exec.Command(bin, "vault-view", "--bogus")
	cmd.Env = append(os.Environ(),
		"AGENT_MEMORY_VAULT="+buildVaultViewCLIFixtureVault(t),
		"ESTATE_LEDGER="+filepath.Join(dir, "ledger.jsonl"),
	)
	out, runErr := cmd.CombinedOutput()
	code := 0
	if runErr != nil {
		exitErr, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("run estate vault-view --bogus: %v\n%s", runErr, out)
		}
		code = exitErr.ExitCode()
	}
	if code != 2 {
		t.Fatalf("estate vault-view --bogus exit code = %d, want 2\n%s", code, out)
	}
}

// TestVaultViewCommandRefusesWithoutVault confirms the CLI surfaces
// GenerateVaultView's own refusal (no $AGENT_MEMORY_VAULT) as a non-zero
// exit rather than silently doing nothing.
func TestVaultViewCommandRefusesWithoutVault(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()

	cmd := exec.Command(bin, "vault-view")
	env := []string{"ESTATE_LEDGER=" + filepath.Join(dir, "ledger.jsonl")}
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "AGENT_MEMORY_VAULT=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = env
	out, runErr := cmd.CombinedOutput()
	code := 0
	if runErr != nil {
		exitErr, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("run estate vault-view: %v\n%s", runErr, out)
		}
		code = exitErr.ExitCode()
	}
	if code == 0 {
		t.Fatalf("estate vault-view with no $AGENT_MEMORY_VAULT exited 0, want a refusal\n%s", out)
	}
}
