package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/candidates"
)

// TestCandidatesRegisterSourceCLI exercises `estate candidates
// register-source` end to end: live-write gating identical to derive/decide,
// idempotent rerun, and a real row that never fabricates a prompt/provenance
// citation.
func TestCandidatesRegisterSourceCLI(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	db := candidatesFixtureDB(t, dir)
	env := append(os.Environ(), "ESTATE_CORPUS="+db)

	args := []string{"candidates", "register-source", "-db", db,
		"-id", "src-1", "-locator", "docs/fixture.md", "-hash", "hash-src-1",
		"-apply", "-authorized-live-write"}
	stdout, stderr, code := runEstateCapture(t, bin, env, args...)
	if code != 0 {
		t.Fatalf("register-source: exit=%d stderr=%s", code, stderr)
	}
	var res candidates.RegisterResult
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("decoding result: %v (%s)", err, stdout)
	}
	if res.Existed || !res.Applied || res.ID == "" {
		t.Fatalf("result = %+v", res)
	}

	// Rerun is a zero-write no-op reported as Existed=true.
	stdout2, _, code2 := runEstateCapture(t, bin, env, args...)
	if code2 != 0 {
		t.Fatalf("rerun: exit=%d", code2)
	}
	var res2 candidates.RegisterResult
	if err := json.Unmarshal([]byte(stdout2), &res2); err != nil {
		t.Fatal(err)
	}
	if !res2.Existed {
		t.Fatalf("rerun result = %+v, want Existed=true", res2)
	}

	d, err := candidates.Get(db, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.PromptID != "" || d.ProvenanceID != "" {
		t.Fatalf("catalogue candidate has a non-empty prompt/provenance id: %+v", d)
	}
}

// TestCandidatesRegisterSourceRefusesLiveWriteWithoutAuthorization mirrors
// the same guard already proven for derive/decide -- the third write path
// this task adds must carry the identical refusal.
func TestCandidatesRegisterSourceRefusesLiveWriteWithoutAuthorization(t *testing.T) {
	bin := buildEstateBinary(t)
	dir := t.TempDir()
	livePath := candidatesFixtureDB(t, dir)
	env := append(os.Environ(), "ESTATE_CORPUS="+livePath)

	before, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runEstateCapture(t, bin, env, "candidates", "register-source",
		"-id", "src-1", "-locator", "docs/x.md", "-hash", "h", "-apply")
	if code == 0 {
		t.Fatal("register-source against the live path succeeded without -authorized-live-write")
	}
	if !strings.Contains(stderr, "refusing") {
		t.Fatalf("stderr = %q, want a refusal message", stderr)
	}
	after, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("fixture bytes changed after a refused register-source")
	}
}

// TestCandidatesMemoryRepoDestinationCLI exercises the repo-publication
// receipt path end to end: propose with destination_kind=repo, accept via
// the same `candidates memory -action accept` entry point (routed
// internally to PublishRepo by the saved proposal's own destination_kind),
// and a receipt naming exactly the proposed path.
func TestCandidatesMemoryRepoDestinationCLI(t *testing.T) {
	bin := buildEstateBinary(t)
	db := candidatesFixtureDB(t, t.TempDir())
	env := append(os.Environ(), "ESTATE_CORPUS="+db)

	if _, err := candidates.RegisterCatalogueSource(db, candidates.CatalogueSource{
		ID: "src-1", Locator: "docs/fixture.md", ContentHash: "hash-src-1",
	}, true); err != nil {
		t.Fatal(err)
	}
	id := "catalogue:src-1"

	proposal := filepath.Join(t.TempDir(), "proposal.json")
	os.WriteFile(proposal, []byte(`{"slug":"repo-fixture","type":"reference","title":"Repo fixture",`+
		`"description":"Invented test","learning":"Invented learning","operator_context":"Invented operator",`+
		`"assistant_context":"Invented assistant","reviewer":"fixture","destination_kind":"repo",`+
		`"destination":"docs/knowledge-workflow-evidence.md","reason":"Invented reason"}`), 0600)

	run := func(args ...string) string {
		t.Helper()
		out, stderr, code := runEstateCapture(t, bin, env, args...)
		if code != 0 {
			t.Fatalf("%v: exit=%d %s", args, code, stderr)
		}
		return out
	}
	run("candidates", "memory", "-db", db, "-id", id, "-action", "propose", "-proposal", proposal, "-apply", "-authorized-live-write")
	run("candidates", "memory", "-db", db, "-id", id, "-action", "accept",
		"-repo-path", "docs/knowledge-workflow-evidence.md", "-repo-commit", "abc1234def",
		"-apply", "-authorized-live-write")
	raw := run("candidates", "memory", "-db", db, "-id", id, "-action", "show")
	var got candidates.MemoryReview
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.State != "promoted" || got.RepoPath != "docs/knowledge-workflow-evidence.md" || got.RepoCommit != "abc1234def" {
		t.Fatalf("memory review = %+v", got)
	}
}
