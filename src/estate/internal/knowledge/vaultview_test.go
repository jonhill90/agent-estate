package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// vaultViewFixtureDDL is a real corpus items table with at least one
// weight='hard' row of every kind, including thought -- unlike
// fixtureDDL (corpus_test.go), which corpusSource's own tests use and
// which has no thought=hard row, since #1035 deliberately excludes thought
// from the compiled index. A separate fixture is what proves readHardItems
// includes thought without changing corpusSource's own test data.
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
  ('it-p2', 'mp-2', 'parameter', 'A soft parameter, must not appear.', 'preference', 'acted', 'soft=yes'),
  ('it-d1', 'mp-3', 'directive', 'Do not touch Traefik before DNS.', 'hard', 'acted', NULL),
  ('it-c1', 'mp-4', 'correction', 'Not X after all -- Y is correct.', 'hard', 'acted', NULL),
  ('it-q1', 'mp-5', 'question', 'When will these parameters be viewable?', 'hard', 'resolved', NULL),
  ('it-t1', 'mp-6', 'thought', 'A hard-weight thought -- must appear here, unlike corpusSource.', 'hard', 'acted', NULL),
  ('it-r1', 'mp-7', 'parameter', 'A retracted parameter, must not appear.', 'retracted', 'acted', 'gone=yes');
`

func buildVaultViewFixtureCorpus(t *testing.T) string {
	t.Helper()
	return buildFixtureCorpus(t, vaultViewFixtureDDL)
}

// buildFixtureVault creates a minimal real vault directory -- just enough
// for backupVault to have something to copy and for GenerateVaultView's
// os.Stat(cfg.VaultDir) check to pass. A test vault, never the real one.
func buildFixtureVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent", "facts"), 0o755); err != nil {
		t.Fatalf("mkdir facts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "index.md"), []byte("# index\n"), 0o644); err != nil {
		t.Fatalf("write index.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "facts", "example.md"), []byte("---\ntype: user\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write example fact: %v", err)
	}
	return dir
}

var fixedNow = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func TestGenerateVaultViewGroupsByKindAndIncludesThought(t *testing.T) {
	vault := buildFixtureVault(t)
	corpusPath := buildVaultViewFixtureCorpus(t)
	cfg := VaultViewConfig{VaultDir: vault, CorpusDBPath: corpusPath, BackupRoot: t.TempDir()}

	res, err := GenerateVaultView(cfg, fixedNow)
	if err != nil {
		t.Fatalf("GenerateVaultView() error = %v", err)
	}

	want := map[string]int{"directive": 1, "parameter": 1, "correction": 1, "question": 1, "thought": 1}
	for kind, count := range want {
		if res.Counts[kind] != count {
			t.Errorf("Counts[%q] = %d, want %d (%+v)", kind, res.Counts[kind], count, res.Counts)
		}
	}
	if res.Total != 5 {
		t.Fatalf("Total = %d, want 5 (retracted and preference rows must be excluded)", res.Total)
	}

	thoughtsFile := filepath.Join(res.OutputDir, "thoughts.md")
	data, err := os.ReadFile(thoughtsFile)
	if err != nil {
		t.Fatalf("read thoughts.md: %v", err)
	}
	if !strings.Contains(string(data), "hard-weight thought") {
		t.Fatalf("thoughts.md does not contain the fixture's hard thought item -- readHardItems must include kind=thought, unlike corpusSource: %s", data)
	}
}

// TestGenerateVaultViewQuestionsMarkedDistinctly is agent-estate#1084's own
// named requirement: a reader must be able to tell "he asked this" from
// "he decided this" at a glance -- #1051 recorded a hard-weight question
// reaching retrieval as if it were settled law.
func TestGenerateVaultViewQuestionsMarkedDistinctly(t *testing.T) {
	vault := buildFixtureVault(t)
	corpusPath := buildVaultViewFixtureCorpus(t)
	cfg := VaultViewConfig{VaultDir: vault, CorpusDBPath: corpusPath, BackupRoot: t.TempDir()}

	res, err := GenerateVaultView(cfg, fixedNow)
	if err != nil {
		t.Fatalf("GenerateVaultView() error = %v", err)
	}

	questions, err := os.ReadFile(filepath.Join(res.OutputDir, "questions.md"))
	if err != nil {
		t.Fatalf("read questions.md: %v", err)
	}
	if !strings.Contains(string(questions), "not decisions he made") {
		t.Fatalf("questions.md does not distinguish questions from decisions: %s", questions)
	}
	if !strings.Contains(string(questions), "agent-estate#1051") {
		t.Fatalf("questions.md does not cite #1051: %s", questions)
	}

	directives, err := os.ReadFile(filepath.Join(res.OutputDir, "directives.md"))
	if err != nil {
		t.Fatalf("read directives.md: %v", err)
	}
	if strings.Contains(string(directives), "not decisions he made") {
		t.Fatalf("directives.md carries the questions-only banner -- it must not: %s", directives)
	}

	index, err := os.ReadFile(filepath.Join(res.OutputDir, "index.md"))
	if err != nil {
		t.Fatalf("read index.md: %v", err)
	}
	if !strings.Contains(string(index), "not a decision") {
		t.Fatalf("index.md does not state the question/decision distinction up front: %s", index)
	}
}

// TestGenerateVaultViewCarriesPromptIDAndStatus is agent-estate#1084's
// explicit requirement: "each item carrying its prompt_id and status so a
// reader can trace a claim."
func TestGenerateVaultViewCarriesPromptIDAndStatus(t *testing.T) {
	vault := buildFixtureVault(t)
	corpusPath := buildVaultViewFixtureCorpus(t)
	cfg := VaultViewConfig{VaultDir: vault, CorpusDBPath: corpusPath, BackupRoot: t.TempDir()}

	res, err := GenerateVaultView(cfg, fixedNow)
	if err != nil {
		t.Fatalf("GenerateVaultView() error = %v", err)
	}
	params, err := os.ReadFile(filepath.Join(res.OutputDir, "parameters.md"))
	if err != nil {
		t.Fatalf("read parameters.md: %v", err)
	}
	if !strings.Contains(string(params), "prompt_id: mp-1") {
		t.Fatalf("parameters.md does not carry the fixture item's prompt_id: %s", params)
	}
	if !strings.Contains(string(params), "status: acted") {
		t.Fatalf("parameters.md does not carry the fixture item's status: %s", params)
	}
	if !strings.Contains(string(params), "id: it-p1") {
		t.Fatalf("parameters.md does not carry the fixture item's own id: %s", params)
	}
}

// TestGenerateVaultViewFilesSayDerivedAndRegenerable is agent-estate#1084's
// explicit requirement: "the file must say so itself, the way the compiled
// index's own note field does."
func TestGenerateVaultViewFilesSayDerivedAndRegenerable(t *testing.T) {
	vault := buildFixtureVault(t)
	corpusPath := buildVaultViewFixtureCorpus(t)
	cfg := VaultViewConfig{VaultDir: vault, CorpusDBPath: corpusPath, BackupRoot: t.TempDir()}

	res, err := GenerateVaultView(cfg, fixedNow)
	if err != nil {
		t.Fatalf("GenerateVaultView() error = %v", err)
	}
	for _, f := range res.Files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if !strings.Contains(string(data), "GENERATED by `estate vault-view`") {
			t.Errorf("%s does not say it is generated: %s", f, data)
		}
		if !strings.Contains(string(data), "regenerable") {
			t.Errorf("%s does not say it is regenerable: %s", f, data)
		}
		if !strings.Contains(string(data), "Do not hand-edit") {
			t.Errorf("%s does not say do not hand-edit: %s", f, data)
		}
	}
}

// TestGenerateVaultViewIdempotent is agent-estate#1084's explicit
// requirement: "running twice produces byte-identical output. Prove it."
// Both runs pass the same fixed `now` -- exactly like knowledge.Generate's
// own now parameter (write.go/generate.go), this package never calls the
// wall clock itself, so "idempotent" means "identical given identical
// inputs", the same meaning the rest of this package already uses.
func TestGenerateVaultViewIdempotent(t *testing.T) {
	vault := buildFixtureVault(t)
	corpusPath := buildVaultViewFixtureCorpus(t)
	cfg := VaultViewConfig{VaultDir: vault, CorpusDBPath: corpusPath, BackupRoot: t.TempDir()}

	res1, err := GenerateVaultView(cfg, fixedNow)
	if err != nil {
		t.Fatalf("first GenerateVaultView() error = %v", err)
	}
	first := map[string][]byte{}
	for _, f := range res1.Files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s after first run: %v", f, err)
		}
		first[filepath.Base(f)] = data
	}

	// Second run: a fresh backup root avoids the "refuse to overwrite a
	// prior backup" guard (backupVault) colliding on the same fixedNow --
	// that guard exists for the real backup path, not to block a second
	// generation of the view itself, so this test sidesteps it exactly the
	// way two real runs a second apart would.
	cfg2 := cfg
	cfg2.BackupRoot = t.TempDir()
	res2, err := GenerateVaultView(cfg2, fixedNow)
	if err != nil {
		t.Fatalf("second GenerateVaultView() error = %v", err)
	}

	if len(res1.Files) != len(res2.Files) {
		t.Fatalf("file count changed between runs: %d vs %d", len(res1.Files), len(res2.Files))
	}
	for _, f := range res2.Files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s after second run: %v", f, err)
		}
		name := filepath.Base(f)
		if string(data) != string(first[name]) {
			t.Fatalf("%s differs between two runs over the same corpus and the same `now` -- not idempotent\n--- run 1 ---\n%s\n--- run 2 ---\n%s", name, first[name], data)
		}
	}
}

// TestGenerateVaultViewBacksUpBeforeWriting is agent-estate#1084's explicit
// requirement: "Before the first write, back up the vault."
func TestGenerateVaultViewBacksUpBeforeWriting(t *testing.T) {
	vault := buildFixtureVault(t)
	corpusPath := buildVaultViewFixtureCorpus(t)
	backupRoot := t.TempDir()
	cfg := VaultViewConfig{VaultDir: vault, CorpusDBPath: corpusPath, BackupRoot: backupRoot}

	res, err := GenerateVaultView(cfg, fixedNow)
	if err != nil {
		t.Fatalf("GenerateVaultView() error = %v", err)
	}
	if res.BackupPath == "" {
		t.Fatal("VaultViewResult.BackupPath is empty")
	}
	if !strings.HasPrefix(res.BackupPath, backupRoot) {
		t.Fatalf("BackupPath %s is not under the configured BackupRoot %s", res.BackupPath, backupRoot)
	}
	// The backup must be a genuine copy of what was in the vault BEFORE
	// this run's own writes -- checked by confirming the pre-existing
	// fixture fact made it into the backup.
	backedUpFact := filepath.Join(res.BackupPath, "agent", "facts", "example.md")
	if _, err := os.Stat(backedUpFact); err != nil {
		t.Fatalf("backup does not contain the pre-existing vault fact: %v", err)
	}
}

func TestGenerateVaultViewRefusesWithoutVaultDir(t *testing.T) {
	corpusPath := buildVaultViewFixtureCorpus(t)
	cfg := VaultViewConfig{VaultDir: "", CorpusDBPath: corpusPath, BackupRoot: t.TempDir()}
	if _, err := GenerateVaultView(cfg, fixedNow); err == nil {
		t.Fatal("GenerateVaultView() with no VaultDir returned nil error, want a refusal")
	}
}

// TestGenerateVaultViewRefusesOnUnreadableCorpusAndWritesNothing is the
// "refuse rather than write a half or stale-looking view" case: a corpus
// read failure must leave the vault exactly as the backup already caught
// it, never a partially-written agent/parameters/.
func TestGenerateVaultViewRefusesOnUnreadableCorpusAndWritesNothing(t *testing.T) {
	vault := buildFixtureVault(t)
	cfg := VaultViewConfig{VaultDir: vault, CorpusDBPath: filepath.Join(t.TempDir(), "absent.sqlite3"), BackupRoot: t.TempDir()}

	if _, err := GenerateVaultView(cfg, fixedNow); err == nil {
		t.Fatal("GenerateVaultView() with an unreadable corpus returned nil error, want a refusal")
	}
	if _, err := os.Stat(filepath.Join(vault, "agent", "parameters")); err == nil {
		t.Fatal("agent/parameters/ was created despite the corpus read failing -- must refuse before writing anything")
	}
}

// TestReadHardItemsExcludesRetractedAndPreference confirms readHardItems'
// own filter directly, independent of GenerateVaultView's aggregation.
func TestReadHardItemsExcludesRetractedAndPreference(t *testing.T) {
	corpusPath := buildVaultViewFixtureCorpus(t)
	items, err := readHardItems(corpusPath)
	if err != nil {
		t.Fatalf("readHardItems() error = %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("readHardItems() returned %d items, want 5", len(items))
	}
	for _, it := range items {
		if it.Weight != "hard" {
			t.Fatalf("readHardItems() returned a non-hard item: %+v", it)
		}
		if strings.Contains(it.Body, "must not appear") {
			t.Fatalf("readHardItems() included a row that should have been filtered: %+v", it)
		}
	}
}
