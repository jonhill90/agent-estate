package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// writeEvalStatus writes a docs/eval-status.json-shaped fixture at path,
// matching jonhill90/skills#230's real on-disk shape (a top-level
// "$comment" this package ignores plus a "skills" map).
func writeEvalStatus(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestEvalStatusFetcher_MergesVerdictAndDate is this test's main positive
// case: a skill with a matching record in the store gets its Verdict and
// LastEval filled in, all five verdict values (including
// VerdictCouldNotMeasure) surviving as themselves -- agent-tui#151's own
// "could_not_measure is NOT the same as unevaluated" requirement.
func TestEvalStatusFetcher_MergesVerdictAndDate(t *testing.T) {
	skillsDir := t.TempDir()
	writeSkill(t, skillsDir, "adopt-or-build", "---\nname: adopt-or-build\ndescription: decide\n---\n")
	writeSkill(t, skillsDir, "research-the-limit", "---\nname: research-the-limit\ndescription: check\n---\n")
	writeSkill(t, skillsDir, "no-record", "---\nname: no-record\ndescription: never scanned by the store\n---\n")

	storePath := filepath.Join(t.TempDir(), "docs", "eval-status.json")
	writeEvalStatus(t, storePath, `{
		"$comment": "ignored by this package",
		"skills": {
			"adopt-or-build": {"verdict": "improve", "date": "2026-08-22", "evidence": "skills/adopt-or-build/references/eval-result.md"},
			"research-the-limit": {"verdict": "could_not_measure", "date": "2026-08-23", "evidence": "skills/research-the-limit/references/eval-result.md"}
		}
	}`)

	got, err := EvalStatusFetcher(skillsDir, storePath)()
	if err != nil {
		t.Fatalf("EvalStatusFetcher: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d skills, want 3: %+v", len(got), got)
	}

	byDir := map[string]Skill{}
	for _, s := range got {
		byDir[s.Dir] = s
	}

	if v := byDir["adopt-or-build"].Verdict; v != "improve" {
		t.Errorf("adopt-or-build Verdict = %q, want %q", v, "improve")
	}
	if d := byDir["adopt-or-build"].LastEval; d == nil || *d != "2026-08-22" {
		t.Errorf("adopt-or-build LastEval = %v, want 2026-08-22", d)
	}

	// The one thing that must not be flattened: could_not_measure survives
	// as itself, distinct from VerdictUnevaluated.
	if v := byDir["research-the-limit"].Verdict; v != VerdictCouldNotMeasure {
		t.Errorf("research-the-limit Verdict = %q, want %q", v, VerdictCouldNotMeasure)
	}
	if v := byDir["research-the-limit"].Verdict; v == VerdictUnevaluated {
		t.Errorf("research-the-limit Verdict collapsed onto VerdictUnevaluated")
	}

	// A skill with no record in the store stays at Scan's own honest
	// zero values, never invented.
	if v := byDir["no-record"].Verdict; v != "" {
		t.Errorf("no-record Verdict = %q, want zero value (renders as unevaluated)", v)
	}
	if d := byDir["no-record"].LastEval; d != nil {
		t.Errorf("no-record LastEval = %v, want nil", *d)
	}

	// INVOCATIONS has no source in the store at all -- agent-tui#151's own
	// scope line: leave it unknown, never invent one.
	for _, s := range got {
		if s.InvocationCount != nil {
			t.Errorf("%s InvocationCount = %v, want nil (no source in the eval store)", s.Dir, *s.InvocationCount)
		}
	}
}

// TestEvalStatusFetcher_MissingStoreDegradesToUnevaluated is agent-tui#151's
// own scope line made concrete: a store path that does not exist must not
// error the whole skill list, and every skill must still render honestly
// unevaluated -- exactly today's behaviour, not a regression to a crash.
func TestEvalStatusFetcher_MissingStoreDegradesToUnevaluated(t *testing.T) {
	skillsDir := t.TempDir()
	writeSkill(t, skillsDir, "alpha", "---\nname: alpha\ndescription: x\n---\n")

	missing := filepath.Join(t.TempDir(), "does-not-exist", "eval-status.json")
	got, err := EvalStatusFetcher(skillsDir, missing)()
	if err != nil {
		t.Fatalf("EvalStatusFetcher with a missing store: %v, want nil error (degrade, don't fail)", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %+v, want exactly one skill", got)
	}
	if got[0].Verdict != "" {
		t.Errorf("Verdict = %q, want zero value", got[0].Verdict)
	}
	if got[0].LastEval != nil {
		t.Errorf("LastEval = %v, want nil", *got[0].LastEval)
	}
}

// TestEvalStatusFetcher_EmptyPathDegradesToUnevaluated covers the
// "no -skills-repo configured at all" case (evalStatusPath == "") -- the
// common, expected standalone-run shape, not merely a missing-file edge
// case.
func TestEvalStatusFetcher_EmptyPathDegradesToUnevaluated(t *testing.T) {
	skillsDir := t.TempDir()
	writeSkill(t, skillsDir, "alpha", "---\nname: alpha\ndescription: x\n---\n")

	got, err := EvalStatusFetcher(skillsDir, "")()
	if err != nil {
		t.Fatalf("EvalStatusFetcher with no store configured: %v, want nil error", err)
	}
	if len(got) != 1 || got[0].Verdict != "" || got[0].LastEval != nil {
		t.Fatalf("got %+v, want one skill with zero-value Verdict/LastEval", got)
	}
}

// TestEvalStatusFetcher_MalformedStoreDegradesToUnevaluated -- a store that
// exists but is not valid JSON in the expected shape must degrade the same
// way a missing one does, not surface as m.fetchErr (this is jonhill90/skills'
// own file, not this package's -- a bad write there must not paint the
// whole skills scan red).
func TestEvalStatusFetcher_MalformedStoreDegradesToUnevaluated(t *testing.T) {
	skillsDir := t.TempDir()
	writeSkill(t, skillsDir, "alpha", "---\nname: alpha\ndescription: x\n---\n")

	storePath := filepath.Join(t.TempDir(), "eval-status.json")
	writeEvalStatus(t, storePath, "not json at all")

	got, err := EvalStatusFetcher(skillsDir, storePath)()
	if err != nil {
		t.Fatalf("EvalStatusFetcher with a malformed store: %v, want nil error", err)
	}
	if len(got) != 1 || got[0].Verdict != "" {
		t.Fatalf("got %+v, want one skill with zero-value Verdict", got)
	}
}

// TestEvalStatusFetcher_ScanErrorStillPropagates: the store logic must not
// swallow a genuine "could not scan the skills directory at all" error --
// that is a real failure (AGENTS.md's "blind, not quiet"), unlike a
// missing eval-status.json.
func TestEvalStatusFetcher_ScanErrorStillPropagates(t *testing.T) {
	missingSkillsDir := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := EvalStatusFetcher(missingSkillsDir, "")()
	if err == nil {
		t.Fatal("EvalStatusFetcher with a missing skills dir returned nil error, want a real one")
	}
}
