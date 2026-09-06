package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// candidatesFixtureDB builds a throwaway corpus copy with one derivable
// candidate, exactly like internal/candidates' own newFixtureDB, so these
// tests exercise the real CLI dispatch in main.go (case "candidates":
// "decide") end to end rather than internal/candidates' functions directly.
func candidatesFixtureDB(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	dbPath := filepath.Join(dir, "corpus-copy.sqlite3")
	sql := `
CREATE TABLE prompts (id TEXT PRIMARY KEY, at INTEGER NOT NULL, text_raw TEXT NOT NULL, text_clean TEXT, context TEXT NOT NULL DEFAULT '', session TEXT, source_file TEXT);
CREATE TABLE codex_provenance (
	id TEXT PRIMARY KEY,
	prompt_id TEXT NOT NULL,
	source_name TEXT NOT NULL,
	harness TEXT NOT NULL,
	source_file TEXT NOT NULL,
	session_id TEXT NOT NULL,
	record_index INTEGER NOT NULL,
	content_hash TEXT NOT NULL,
	ingested_at_watermark TEXT NOT NULL
);
INSERT INTO prompts (id, at, text_raw, context) VALUES ('p1', 1, 'fixture raw text', 'ctx');
INSERT INTO codex_provenance (id, prompt_id, source_name, harness, source_file, session_id, record_index, content_hash, ingested_at_watermark)
VALUES ('prov1', 'p1', 'codex-rollout', 'codex', 'f.jsonl', 'sess-1', 0, 'hash-1', '2026-09-05T00:00:00Z');
`
	if err := exec.Command("sqlite3", dbPath, sql).Run(); err != nil {
		t.Fatalf("creating fixture db: %v", err)
	}
	return dbPath
}

func candidateIDIn(t *testing.T, dbPath string) string {
	t.Helper()
	out, err := exec.Command("sqlite3", "-readonly", dbPath, "select id from knowledge_candidates limit 1;").Output()
	if err != nil {
		t.Fatalf("reading candidate id: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func runEstateCapture(t *testing.T, bin string, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("running estate %v: %v", args, err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// TestCandidatesDecideRefusesLiveWriteWithoutAuthorization is this task's
// own required acceptance test #1: `estate candidates decide -apply`
// against a path that resolves as the live corpus (via ESTATE_CORPUS, the
// same override internal/corpus.Path() and cmd/codexingest's own
// TestRefusesLiveCorpusPath use) must refuse, unconditionally, without
// -authorized-live-write -- and must write nothing.
func TestCandidatesDecideRefusesLiveWriteWithoutAuthorization(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	livePath := candidatesFixtureDB(t, dir)
	env := append(os.Environ(), "ESTATE_CORPUS="+livePath)

	// Derive first (against the "live" fixture, standing in for the real
	// live corpus) so knowledge_candidates and a real id exist.
	if _, _, exit := runEstateCapture(t, bin, env, "candidates"); exit != 0 {
		t.Fatalf("derive against fixture: exit = %d", exit)
	}
	id := candidateIDIn(t, livePath)

	before, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatalf("reading fixture before decide: %v", err)
	}

	_, stderr, exit := runEstateCapture(t, bin, env, "candidates", "decide", "-apply", id, "promote")
	if exit == 0 {
		t.Fatalf("exit = 0, want refusal against the live corpus path")
	}
	if !strings.Contains(stderr, "refusing") {
		t.Fatalf("stderr = %q, want a refusal message naming the guard", stderr)
	}

	after, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatalf("reading fixture after decide: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("fixture bytes changed after a refused decide -- a refusal must write nothing")
	}
}

// TestCandidatesDecideAuthorizedLiveWriteSucceeds is acceptance test #1's
// other direction: with -authorized-live-write AND -db explicitly naming
// the (fixture) live path, the write proceeds and prints the loud banner
// naming the resolved path before writing.
func TestCandidatesDecideAuthorizedLiveWriteSucceeds(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	livePath := candidatesFixtureDB(t, dir)
	env := append(os.Environ(), "ESTATE_CORPUS="+livePath)

	if _, _, exit := runEstateCapture(t, bin, env, "candidates"); exit != 0 {
		t.Fatalf("derive against fixture: exit = %d", exit)
	}
	id := candidateIDIn(t, livePath)

	stdout, stderr, exit := runEstateCapture(t, bin, env, "candidates", "decide",
		"-db", livePath, "-apply", "-authorized-live-write", id, "discard")
	if exit != 0 {
		t.Fatalf("authorized live write: exit = %d, stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "AUTHORIZED LIVE-CORPUS WRITE") {
		t.Fatalf("stderr = %q, want the loud pre-write banner", stderr)
	}
	if !strings.Contains(stdout, "recorded") {
		t.Fatalf("stdout = %q, want confirmation the decision was recorded", stdout)
	}

	got, err := exec.Command("sqlite3", "-readonly", livePath,
		"select decision from knowledge_candidates where id='"+id+"';").Output()
	if err != nil {
		t.Fatalf("reading back decision: %v", err)
	}
	if strings.TrimSpace(string(got)) != "discarded" {
		t.Fatalf("decision = %q, want discarded", strings.TrimSpace(string(got)))
	}
}

// TestCandidatesDecideAuthorizationDoesNotLeakToNonLivePath is acceptance
// test #2: -authorized-live-write is only EVER effective when -db itself
// resolves as the live path. Passing it alongside a -db that is NOT live
// must not print the live-write banner and must not require the flag at
// all -- the flag has zero effect here, which is the guarantee that keeps
// it from being usable to paper over a -db that doesn't actually match what
// was authorized.
func TestCandidatesDecideAuthorizationDoesNotLeakToNonLivePath(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	nonLive := candidatesFixtureDB(t, dir)
	// A DIFFERENT path stands in for "the live corpus" via ESTATE_CORPUS,
	// so RefuseLivePath's identity check has something to compare against
	// that is NOT -db here -- -db names nonLive, not the "live" fixture.
	otherLive := candidatesFixtureDB(t, filepath.Join(dir, "other"))
	env := append(os.Environ(), "ESTATE_CORPUS="+otherLive)

	if _, _, exit := runEstateCapture(t, bin, env, "candidates", "-db", nonLive); exit != 0 {
		t.Fatalf("derive against non-live fixture: exit = %d", exit)
	}
	id := candidateIDIn(t, nonLive)

	stdout, stderr, exit := runEstateCapture(t, bin, env, "candidates", "decide",
		"-db", nonLive, "-apply", "-authorized-live-write", id, "promote")
	if exit != 0 {
		t.Fatalf("decide against non-live -db: exit = %d, stderr=%s", exit, stderr)
	}
	if strings.Contains(stderr, "AUTHORIZED LIVE-CORPUS WRITE") {
		t.Fatalf("stderr = %q, want no live-write banner -- -db here does not name the live path", stderr)
	}
	if !strings.Contains(stdout, "recorded") {
		t.Fatalf("stdout = %q, want a normal (non-live) recorded confirmation", stdout)
	}
}

// TestCandidatesDecideDryRunIsZeroWriteByDefault is the "zero-write is the
// default with no flag required" half of this task's guard requirement:
// omitting -apply entirely must never write, even against a path that is
// not live at all.
func TestCandidatesDecideDryRunIsZeroWriteByDefault(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	db := candidatesFixtureDB(t, dir)

	if _, _, exit := runEstateCapture(t, bin, os.Environ(), "candidates", "-db", db); exit != 0 {
		t.Fatalf("derive: exit != 0")
	}
	id := candidateIDIn(t, db)

	stdout, _, exit := runEstateCapture(t, bin, os.Environ(), "candidates", "decide", "-db", db, id, "promote")
	if exit != 0 {
		t.Fatalf("dry-run decide: exit = %d", exit)
	}
	if !strings.Contains(stdout, "dry run") {
		t.Fatalf("stdout = %q, want it to say this was a dry run", stdout)
	}

	out, err := exec.Command("sqlite3", "-readonly", db,
		"select count(*) from pragma_table_info('knowledge_candidates') where name='decision';").Output()
	if err != nil {
		t.Fatalf("checking decision column: %v", err)
	}
	if strings.TrimSpace(string(out)) != "0" {
		t.Fatal("decision column exists after a dry run with no -apply -- something wrote when it should not have")
	}
}
