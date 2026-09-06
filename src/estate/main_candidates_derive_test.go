package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCandidatesBareDeriveRefusesLiveWriteWithoutAuthorization is this
// task's own acceptance test #1 (agent-estate#1251): bare `estate
// candidates` -- no -apply, no -authorized-live-write -- against a path
// that resolves as the live corpus (via ESTATE_CORPUS, the same override
// internal/corpus.Path() and cmd/codexingest's own tests use) must refuse,
// unconditionally, and must create nothing. Asserted by
// knowledge_candidates NOT EXISTING afterwards, not by exit code alone --
// the defect this closes is a schema-creating write, so the proof has to
// look at the schema.
func TestCandidatesBareDeriveRefusesLiveWriteWithoutAuthorization(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	livePath := candidatesFixtureDB(t, dir)
	env := append(os.Environ(), "ESTATE_CORPUS="+livePath)

	before, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatalf("reading fixture before derive: %v", err)
	}

	stdout, stderr, exit := runEstateCapture(t, bin, env, "candidates")
	if exit == 0 {
		t.Fatalf("exit = 0, want refusal against the live corpus path; stdout=%s", stdout)
	}
	if !strings.Contains(stderr, "refusing") {
		t.Fatalf("stderr = %q, want a refusal message naming the guard", stderr)
	}

	after, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatalf("reading fixture after derive: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("fixture bytes changed after a refused derive -- a refusal must write nothing")
	}

	out, err := exec.Command("sqlite3", "-readonly", livePath,
		"select name from sqlite_master where type='table' and name='knowledge_candidates';").Output()
	if err != nil {
		t.Fatalf("checking knowledge_candidates: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatal("knowledge_candidates exists after a refused bare derive -- the guard did not stop the CREATE TABLE")
	}
}

// TestCandidatesBareDeriveAuthorizedLiveWriteSucceeds is acceptance test
// #1's other direction: with -apply and -authorized-live-write against a
// path that resolves as live, the write proceeds and prints the loud
// pre-write banner naming the resolved path.
func TestCandidatesBareDeriveAuthorizedLiveWriteSucceeds(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	livePath := candidatesFixtureDB(t, dir)
	env := append(os.Environ(), "ESTATE_CORPUS="+livePath)

	stdout, stderr, exit := runEstateCapture(t, bin, env, "candidates", "-apply", "-authorized-live-write")
	if exit != 0 {
		t.Fatalf("authorized live derive: exit = %d, stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "AUTHORIZED LIVE-CORPUS WRITE") {
		t.Fatalf("stderr = %q, want the loud pre-write banner", stderr)
	}
	if !strings.Contains(stdout, "knowledge_candidates total") {
		t.Fatalf("stdout = %q, want the real-run summary line", stdout)
	}

	id := candidateIDIn(t, livePath)
	if id == "" {
		t.Fatal("no candidate row written despite an authorized apply run")
	}
}

// TestCandidatesBareDeriveAuthorizationDoesNotLeakToNonLivePath is
// acceptance test #2: -authorized-live-write has zero effect when -db does
// not resolve as the live path -- passing it (or omitting it) makes no
// difference to a plain -apply run against a path that ISN'T live.
func TestCandidatesBareDeriveAuthorizationDoesNotLeakToNonLivePath(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	nonLive := candidatesFixtureDB(t, dir)
	// A DIFFERENT path stands in for "the live corpus" via ESTATE_CORPUS, so
	// RefuseLivePath's identity check has something to compare against that
	// is NOT -db here.
	otherLive := candidatesFixtureDB(t, filepath.Join(dir, "other"))
	env := append(os.Environ(), "ESTATE_CORPUS="+otherLive)

	stdout, stderr, exit := runEstateCapture(t, bin, env, "candidates", "-db", nonLive, "-apply", "-authorized-live-write")
	if exit != 0 {
		t.Fatalf("derive against non-live -db: exit = %d, stderr=%s", exit, stderr)
	}
	if strings.Contains(stderr, "AUTHORIZED LIVE-CORPUS WRITE") {
		t.Fatalf("stderr = %q, want no live-write banner -- -db here does not name the live path", stderr)
	}
	if !strings.Contains(stdout, "knowledge_candidates total") {
		t.Fatalf("stdout = %q, want the real-run summary line", stdout)
	}
}

// TestCandidatesBareDeriveDryRunIsZeroWriteByDefault is acceptance test #3
// at the CLI layer: omitting -apply against a path that is NOT live must
// still refuse to write anything while reporting counts -- zero-write is the
// default with no flag required, mirroring candidates decide's own
// contract.
func TestCandidatesBareDeriveDryRunIsZeroWriteByDefault(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	db := candidatesFixtureDB(t, dir)

	stdout, _, exit := runEstateCapture(t, bin, os.Environ(), "candidates", "-db", db)
	if exit != 0 {
		t.Fatalf("dry-run derive: exit = %d", exit)
	}
	if !strings.Contains(stdout, "dry run") {
		t.Fatalf("stdout = %q, want it to say this was a dry run", stdout)
	}
	if !strings.Contains(stdout, "would insert") {
		t.Fatalf("stdout = %q, want the WouldInsert count reported", stdout)
	}

	out, err := exec.Command("sqlite3", "-readonly", db,
		"select name from sqlite_master where type='table' and name='knowledge_candidates';").Output()
	if err != nil {
		t.Fatalf("checking knowledge_candidates: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatal("knowledge_candidates exists after a dry run with no -apply -- something wrote when it should not have")
	}
}
