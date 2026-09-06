package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/candidates"
)

func TestCandidatesMemoryCLI(t *testing.T) {
	bin := buildEstateBinary(t)
	db := candidatesFixtureDB(t, t.TempDir())
	env := append(os.Environ(), "ESTATE_CORPUS="+db)
	run := func(args ...string) string {
		t.Helper()
		out, stderr, code := runEstateCapture(t, bin, env, args...)
		if code != 0 {
			t.Fatalf("%v: exit=%d %s", args, code, stderr)
		}
		return out
	}
	if _, err := candidates.Derive(db); err != nil {
		t.Fatal(err)
	}
	id := candidateIDIn(t, db)
	vault := t.TempDir()
	os.MkdirAll(filepath.Join(vault, "agent", "facts"), 0700)
	os.WriteFile(filepath.Join(vault, "agent", "index.md"), []byte("# Facts\n"), 0600)
	proposal := filepath.Join(t.TempDir(), "proposal.json")
	os.WriteFile(proposal, []byte(`{"slug":"cli-fixture","type":"project","title":"CLI fixture","description":"Invented test","learning":"Invented learning","operator_context":"Invented operator","assistant_context":"Invented assistant","reviewer":"fixture"}`), 0600)
	args := []string{"candidates", "memory", "-db", db, "-id", id, "-action", "propose", "-proposal", proposal, "-apply"}
	if _, _, code := runEstateCapture(t, bin, env, args...); code == 0 {
		t.Fatal("live write lacked acknowledgement")
	}
	run(append(args, "-authorized-live-write")...)
	run("candidates", "memory", "-db", db, "-id", id, "-action", "accept", "-vault", vault, "-apply", "-authorized-live-write")
	raw := run("candidates", "memory", "-db", db, "-id", id, "-action", "show")
	var got candidates.MemoryReview
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.State != "promoted" || got.PublishedRevision == "" {
		t.Fatal(raw)
	}
}
