package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sqliteAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not on PATH")
	}
}

func runCapture(t *testing.T, args []string) (stdout, stderr string, exit int) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	exit = run(args, outW, errW)
	outW.Close()
	errW.Close()
	stdout = drain(t, outR)
	stderr = drain(t, errR)
	return stdout, stderr, exit
}

func drain(t *testing.T, r *os.File) string {
	t.Helper()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}

const testDDL = `
CREATE TABLE prompts (id TEXT PRIMARY KEY, at INTEGER NOT NULL, text_raw TEXT NOT NULL, text_clean TEXT,
  context TEXT NOT NULL, session TEXT, source_file TEXT, project TEXT, tmux_pane TEXT, tmux_pane_target TEXT,
  author TEXT NOT NULL DEFAULT 'unknown');
CREATE TABLE items (id TEXT PRIMARY KEY, prompt_id TEXT NOT NULL, kind TEXT NOT NULL, body TEXT NOT NULL,
  weight TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'open', status_reason TEXT, resolved_to TEXT, acked_at INTEGER);
CREATE VIEW live_parameters AS SELECT * FROM items WHERE kind = 'parameter' AND weight != 'retracted';
INSERT INTO prompts VALUES ('mp-1', 100, 'this consistantly fails, dont ignore it', NULL, 'ctx', 's1', NULL, '', NULL, NULL, 'unknown');
INSERT INTO prompts VALUES ('mp-2', 200, 'ship it', NULL, 'ctx', 's1', NULL, '', NULL, NULL, 'unknown');
INSERT INTO items VALUES ('it-1', 'mp-1', 'parameter', 'a rule', 'hard', 'acted', NULL, NULL, NULL);
INSERT INTO items VALUES ('it-2', 'mp-2', 'parameter', 'another rule', 'hard', 'open', NULL, NULL, NULL);
`

func newTestCorpus(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "corpus.sqlite3")
	cmd := exec.Command("sqlite3", path)
	cmd.Stdin = strings.NewReader(testDDL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture setup: %v\n%s", err, out)
	}
	return path
}

func writeManifest(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "manifest.json")
	m := backupManifestFile{}
	m.Results = append(m.Results, struct {
		Name          string `json:"name"`
		Status        string `json:"status"`
		LiveUntouched bool   `json:"live_untouched"`
		RestoreTest   struct {
			ByteIdentical bool `json:"byte_identical"`
		} `json:"restore_test"`
	}{
		Name:          "corpus-sqlite3",
		Status:        "ok",
		LiveUntouched: true,
		RestoreTest: struct {
			ByteIdentical bool `json:"byte_identical"`
		}{ByteIdentical: true},
	})
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReportModeAgainstLivePathIsAllowedAndWritesNothing(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	live := newTestCorpus(t, dir)
	t.Setenv("ESTATE_CORPUS", live)

	before, err := os.Stat(live)
	if err != nil {
		t.Fatal(err)
	}
	stdout, _, exit := runCapture(t, nil) // no -db => reads the "live" path (faked via ESTATE_CORPUS)
	if exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	if !strings.Contains(stdout, "REPORT (read-only, nothing written)") {
		t.Errorf("stdout missing report-mode banner: %s", stdout)
	}
	after, err := os.Stat(live)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("report mode must never touch the corpus it read, even the 'live' one")
	}
}

// MUTATION-CHECK, direction one: -apply against the live path must be
// refused, in-process (not via a shell guard) -- this is cmd/textclean's
// OWN check, proven independently of ledger-write-guard.sh.
func TestApplyAgainstLivePathRefusesAndWritesNothing(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	live := newTestCorpus(t, dir)
	t.Setenv("ESTATE_CORPUS", live)
	manifest := writeManifest(t, dir)

	before, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, exit := runCapture(t, []string{"-db", live, "-apply", "-backup-manifest", manifest})
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "refusing -apply against") {
		t.Errorf("stderr = %q, want a live-path refusal", stderr)
	}
	after, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a refused -apply must not write a single byte")
	}
}

func TestApplyWithoutDbFlagRefuses(t *testing.T) {
	dir := t.TempDir()
	manifest := writeManifest(t, dir)
	_, stderr, exit := runCapture(t, []string{"-apply", "-backup-manifest", manifest})
	if exit != 2 || !strings.Contains(stderr, "requires -db") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
}

func TestApplyWithoutBackupManifestRefuses(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	scratch := newTestCorpus(t, dir)
	_, stderr, exit := runCapture(t, []string{"-db", scratch, "-apply"})
	if exit != 2 || !strings.Contains(stderr, "requires -backup-manifest") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
}

func TestApplyWithBadManifestRefuses(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	scratch := newTestCorpus(t, dir)
	badManifest := filepath.Join(dir, "bad.json")
	os.WriteFile(badManifest, []byte(`{"results":[{"name":"corpus-sqlite3","status":"failed"}]}`), 0o644)
	_, stderr, exit := runCapture(t, []string{"-db", scratch, "-apply", "-backup-manifest", badManifest})
	if exit != 1 || !strings.Contains(stderr, "did not verify") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
}

// MUTATION-CHECK, direction two: -apply against a genuine scratch copy,
// with a valid manifest, actually writes -- proving the refusal above is
// about the LIVE path specifically, not a blanket refusal that would make
// -apply useless everywhere.
func TestApplyAgainstScratchCopyWritesAndIsIdempotent(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	scratch := newTestCorpus(t, dir)
	manifest := writeManifest(t, dir)

	stdout, stderr, exit := runCapture(t, []string{"-db", scratch, "-apply", "-backup-manifest", manifest, "-show-refused=false"})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, "2 row(s) written") {
		t.Fatalf("expected 2 rows written, got: %s", stdout)
	}

	// idempotent: second run finds zero candidates left
	stdout2, _, exit2 := runCapture(t, []string{"-db", scratch, "-apply", "-backup-manifest", manifest})
	if exit2 != 0 {
		t.Fatalf("exit=%d", exit2)
	}
	if !strings.Contains(stdout2, "zero candidates") {
		t.Fatalf("second apply should find nothing left to do, got: %s", stdout2)
	}
}
