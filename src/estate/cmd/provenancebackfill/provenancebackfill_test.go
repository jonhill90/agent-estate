package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeClaudeFixture(t *testing.T, dir, project, base string, lines []string) string {
	t.Helper()
	projDir := filepath.Join(dir, project)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projDir, base)
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestExtractFilePositiveFilter locks the positive filter: genuine
// type=="user"/role=="user"/string-content turns are extracted; a
// tool_result array-content "user" record and a non-user type are not.
func TestExtractFilePositiveFilter(t *testing.T) {
	dir := t.TempDir()
	path := writeClaudeFixture(t, dir, "proj", "s1.jsonl", []string{
		`{"type":"summary","sessionId":"sess-1"}`,
		`{"type":"user","sessionId":"sess-1","message":{"role":"user","content":"fixture: genuine turn one"}}`,
		`{"type":"user","sessionId":"sess-1","message":{"role":"user","content":[{"type":"tool_result","content":"not operator text"}]}}`,
		`{"type":"assistant","sessionId":"sess-1","message":{"role":"assistant","content":"reply"}}`,
		`{"type":"user","sessionId":"sess-1","message":{"role":"user","content":"fixture: genuine turn two"}}`,
	})

	units, malformed, err := ExtractFile(path)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	if malformed != 0 {
		t.Fatalf("malformed = %d, want 0", malformed)
	}
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2", len(units))
	}
	if units[0].Text != "fixture: genuine turn one" || units[0].Provenance.RecordIndex != 0 {
		t.Errorf("units[0] = %+v", units[0])
	}
	if units[1].Text != "fixture: genuine turn two" || units[1].Provenance.RecordIndex != 1 {
		t.Errorf("units[1] = %+v", units[1])
	}
	for _, u := range units {
		if u.Provenance.Harness != "claude" || u.Provenance.SourceName != "claude-transcript" {
			t.Errorf("unit identity fields wrong: %+v", u.Provenance)
		}
		if u.Provenance.SourceFile != path {
			t.Errorf("SourceFile = %q, want %q", u.Provenance.SourceFile, path)
		}
	}
}

// TestExtractFileMalformedLineCountedNotFatal covers a line that is not
// valid JSON: it is counted, and does not stop extraction of the rest of
// the file.
func TestExtractFileMalformedLineCountedNotFatal(t *testing.T) {
	dir := t.TempDir()
	path := writeClaudeFixture(t, dir, "proj", "s2.jsonl", []string{
		`{"type":"user","sessionId":"sess-1","message":{"role":"user","content":"fixture: turn"}}`,
		`not even json`,
	})
	units, malformed, err := ExtractFile(path)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	if malformed != 1 {
		t.Fatalf("malformed = %d, want 1", malformed)
	}
	if len(units) != 1 {
		t.Fatalf("units = %d, want 1", len(units))
	}
}

// TestPlanWatermarkExcludesFilesChangedAfter is the revised acceptance's
// core rule: a file whose mtime is after the recorded watermark is excluded
// and listed, never silently read.
func TestPlanWatermarkExcludesFilesChangedAfter(t *testing.T) {
	dir := t.TempDir()
	oldPath := writeClaudeFixture(t, dir, "proj", "old.jsonl", []string{`{"type":"summary"}`})
	newPath := writeClaudeFixture(t, dir, "proj", "new.jsonl", []string{`{"type":"summary"}`})

	watermark := time.Now().Add(-1 * time.Hour)
	// old.jsonl predates the watermark; new.jsonl (just written) postdates it.
	if err := os.Chtimes(oldPath, watermark.Add(-time.Minute), watermark.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	byBase, collisions, err := FindClaudeFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(collisions) != 0 {
		t.Fatalf("collisions = %v, want none", collisions)
	}
	plan := PlanWatermark(byBase, collisions, watermark)
	if _, ok := plan.Eligible["old.jsonl"]; !ok {
		t.Error("old.jsonl should be eligible")
	}
	if _, ok := plan.Eligible["new.jsonl"]; ok {
		t.Error("new.jsonl should NOT be eligible (changed after watermark)")
	}
	found := false
	for _, p := range plan.Excluded {
		if p == newPath {
			found = true
		}
	}
	if !found {
		t.Errorf("Excluded = %v, want it to list %s", plan.Excluded, newPath)
	}
}

// TestFindClaudeFilesReportsBasenameCollision covers the ambiguous-collision
// path: two different full paths sharing one basename are excluded from the
// eligible map entirely and named, never guessed at.
func TestFindClaudeFilesReportsBasenameCollision(t *testing.T) {
	dir := t.TempDir()
	writeClaudeFixture(t, dir, "proj-a", "dup.jsonl", []string{`{"type":"summary"}`})
	writeClaudeFixture(t, dir, "proj-b", "dup.jsonl", []string{`{"type":"summary"}`})

	byBase, collisions, err := FindClaudeFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(collisions) != 1 || collisions[0] != "dup.jsonl" {
		t.Fatalf("collisions = %v, want [dup.jsonl]", collisions)
	}
	if _, ok := byBase["dup.jsonl"]; ok {
		t.Error("dup.jsonl must not appear in the eligible-basename map")
	}
}

func sqliteAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not on PATH")
	}
}

func newTestCorpus(t *testing.T, dir string, rows [][2]string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "corpus-copy.sqlite3")
	ddl := `CREATE TABLE prompts (id TEXT PRIMARY KEY, at INTEGER NOT NULL, text_raw TEXT NOT NULL, text_clean TEXT, context TEXT NOT NULL DEFAULT '', session TEXT, source_file TEXT);`
	if err := exec.Command("sqlite3", dbPath, ddl).Run(); err != nil {
		t.Fatalf("creating test corpus: %v", err)
	}
	for i, row := range rows {
		id, sourceFile := row[0], row[1]
		text := "row-text-" + id
		q := "INSERT INTO prompts (id, at, text_raw, context, source_file) VALUES ('" + id + "', " +
			itoa(i) + ", 'row-text-" + id + "', 'ctx', '" + sourceFile + "');"
		if err := exec.Command("sqlite3", dbPath, q).Run(); err != nil {
			t.Fatalf("inserting test row: %v", err)
		}
		_ = text
	}
	return dbPath
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		return "-" + string(buf)
	}
	return string(buf)
}

// TestEndToEndDryRunApplyRerunIsIdempotent is the full revised-acceptance
// proof against a throwaway copy: dry-run changes nothing, apply attributes
// exactly the rows whose text matches an extracted unit, and re-applying at
// the SAME watermark against the SAME db inserts zero further rows.
func TestEndToEndDryRunApplyRerunIsIdempotent(t *testing.T) {
	sqliteAvailable(t)
	claudeDir := t.TempDir()
	writeClaudeFixture(t, claudeDir, "proj", "row-abc.jsonl", []string{
		`{"type":"user","sessionId":"sess-1","message":{"role":"user","content":"row-text-row-abc"}}`,
	})
	watermark := time.Now().Add(time.Hour) // comfortably after the fixture's mtime

	dbDir := t.TempDir()
	dbPath := newTestCorpus(t, dbDir, [][2]string{{"row-abc", "row-abc.jsonl"}})

	// dry-run: must not create the table's rows.
	dryReport, err := buildReport(dbPath, claudeDir, watermark, false)
	if err != nil {
		t.Fatalf("dry-run buildReport: %v", err)
	}
	if dryReport.AttributedCount != 1 {
		t.Fatalf("dry-run AttributedCount = %d, want 1", dryReport.AttributedCount)
	}
	if dryReport.TableCountAfter != 0 {
		t.Fatalf("dry-run must write nothing; TableCountAfter = %d, want 0", dryReport.TableCountAfter)
	}

	// apply: exactly one row written.
	applyReport, err := buildReport(dbPath, claudeDir, watermark, true)
	if err != nil {
		t.Fatalf("apply buildReport: %v", err)
	}
	if applyReport.AttributedCount != 1 {
		t.Fatalf("apply AttributedCount = %d, want 1", applyReport.AttributedCount)
	}
	if applyReport.TableCountAfter != 1 {
		t.Fatalf("apply TableCountAfter = %d, want 1", applyReport.TableCountAfter)
	}
	if applyReport.TableCountAfter-applyReport.TableCountBefore != applyReport.AttributedCount {
		t.Fatalf("row count does not equal manifest: grew by %d, attributed %d",
			applyReport.TableCountAfter-applyReport.TableCountBefore, applyReport.AttributedCount)
	}

	// rerun at the SAME watermark: zero new inserts, everything reported already_attributed.
	rerunReport, err := buildReport(dbPath, claudeDir, watermark, true)
	if err != nil {
		t.Fatalf("rerun buildReport: %v", err)
	}
	if rerunReport.AttributedCount != 0 {
		t.Fatalf("rerun AttributedCount = %d, want 0 (idempotent)", rerunReport.AttributedCount)
	}
	if rerunReport.TableCountAfter != 1 {
		t.Fatalf("rerun TableCountAfter = %d, want 1 (unchanged)", rerunReport.TableCountAfter)
	}
	foundAlready := false
	for _, d := range rerunReport.Decisions {
		if d.Outcome == outcomeAlready {
			foundAlready = true
		}
	}
	if !foundAlready {
		t.Error("rerun should report the row as already_attributed, not silently drop it")
	}
}

// TestRefuseLivePath is the in-process backstop: -apply must refuse against
// the well-known live corpus locations regardless of the hook.
func TestRefuseLivePath(t *testing.T) {
	cases := []struct {
		path string
		live bool
	}{
		{filepath.Join(os.Getenv("HOME"), "corpus", "ledger.sqlite3"), true},
		{"/Users/jon/.local/state/agent-dotfiles-supervisor/ledger.sqlite3", true},
		{"/tmp/corpus-copy.sqlite3", false},
	}
	for _, c := range cases {
		_, live := refuseLivePath(c.path)
		if live != c.live {
			t.Errorf("refuseLivePath(%q) live = %v, want %v", c.path, live, c.live)
		}
	}
}
