package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/provenance"
)

func sqliteAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not on PATH")
	}
}

// writeRolloutFixture writes lines to a temp .jsonl file and returns its
// path. Every string here is invented fixture text -- never a real captured
// operator prompt.
func writeRolloutFixture(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
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
	stdout = drainPipe(t, outR)
	stderr = drainPipe(t, errR)
	return stdout, stderr, exit
}

func drainPipe(t *testing.T, r *os.File) string {
	t.Helper()
	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

// newTestCorpus creates a fresh SQLite corpus copy with the real prompts
// schema plus codex_provenance -- the two tables cmd/codexingest already
// writes together, which is what this command joins against.
func newTestCorpus(t *testing.T, dir string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "corpus-copy.sqlite3")
	ddl := `CREATE TABLE prompts (id TEXT PRIMARY KEY, at INTEGER NOT NULL, text_raw TEXT NOT NULL, text_clean TEXT, context TEXT NOT NULL DEFAULT '', session TEXT, source_file TEXT);
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
);`
	if err := exec.Command("sqlite3", dbPath, ddl).Run(); err != nil {
		t.Fatalf("creating test corpus: %v", err)
	}
	return dbPath
}

// seedRow hand-inserts one prompts row (context=”, exactly as codexingest
// leaves it) and its codex_provenance row, addressed by the same three
// fields (source_file, session_id, record_index) codexingest's own
// insertUnit writes. contentHash defaults to the real hash of text unless
// overridden by the caller afterward (see TestRecordUnresolved).
func seedRow(t *testing.T, dbPath, id, sourceFile, sessionID string, recordIndex int, text string) {
	t.Helper()
	hash := provenance.HashContent(text)
	q := `BEGIN;
INSERT INTO prompts (id, at, text_raw, context, session, source_file) VALUES ('` + sqlEscape(id) + `', 0, '` + sqlEscape(text) + `', '', '` + sqlEscape(sessionID) + `', '` + sqlEscape(filepath.Base(sourceFile)) + `');
INSERT INTO codex_provenance (id, prompt_id, source_name, harness, source_file, session_id, record_index, content_hash, ingested_at_watermark)
VALUES ('` + sqlEscape(id) + `', '` + sqlEscape(id) + `', 'codex-rollout', 'codex', '` + sqlEscape(sourceFile) + `', '` + sqlEscape(sessionID) + `', ` + itoa(recordIndex) + `, '` + sqlEscape(hash) + `', '2026-01-01T00:00:00Z');
COMMIT;`
	if err := exec.Command("sqlite3", dbPath, q).Run(); err != nil {
		t.Fatalf("seeding row %s: %v", id, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func queryOne(t *testing.T, dbPath, sql string) string {
	t.Helper()
	out, err := exec.Command("sqlite3", dbPath, sql).Output()
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	if sc.Scan() {
		return sc.Text()
	}
	return ""
}

func recordWatermark(t *testing.T) string {
	t.Helper()
	stdout, stderr, exit := runCapture(t, []string{"-record-watermark"})
	if exit != 0 {
		t.Fatalf("-record-watermark exit=%d stderr=%s", exit, stderr)
	}
	return strings.TrimSpace(stdout)
}

// buildCorpusViaRealPipeline runs the real cmd/corpusextract and
// cmd/codexingest binaries against root, into a fresh corpus copy -- proving
// this command's candidates are exactly what that real pipeline leaves
// behind (context=”), not a hand-typed stand-in for its shape.
func buildCorpusViaRealPipeline(t *testing.T, dir, root string) string {
	t.Helper()
	manifestPath := filepath.Join(dir, "manifest.json")
	cmd := exec.Command("go", "run", "../corpusextract", "-root", root, "-json", "-out", manifestPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running corpusextract: %v\n%s", err, out)
	}
	dbPath := newTestCorpus(t, dir)
	cmd = exec.Command("go", "run", "../codexingest", "-db", dbPath, "-manifest", manifestPath, "-apply", "-sessions-root", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running codexingest: %v\n%s", err, out)
	}
	return dbPath
}

// contextOf decodes the JSON envelope stored in one prompt's context column.
func contextOf(t *testing.T, dbPath, promptID string) contextEnvelope {
	t.Helper()
	raw := queryOne(t, dbPath, "select context from prompts where id = '"+sqlEscape(promptID)+"';")
	var env contextEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("context for %s is not valid JSON: %v (raw=%q)", promptID, err, raw)
	}
	return env
}

// TestEndToEndDerivesFromRealPipeline is the core deliverable: against a
// corpus a real corpusextract+codexingest run produced, this command derives
// "derived" for the second turn (from the assistant reply between the two)
// and "no_prior_turn" for the first (it opens the session).
func TestEndToEndDerivesFromRealPipeline(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"fixture-session-1"}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: operator turn one, opens the session"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: assistant reply between the two turns"}]}}`,
		`{"timestamp":"2026-01-01T00:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: operator turn two"}]}}`,
	})
	dbPath := buildCorpusViaRealPipeline(t, dir, root)

	promptIDs := strings.Fields(queryOne(t, dbPath, "select group_concat(id, ' ') from prompts order by at;"))
	if len(promptIDs) != 2 {
		t.Fatalf("prompts seeded by the real pipeline = %d, want 2 (ids=%v)", len(promptIDs), promptIDs)
	}

	wm := recordWatermark(t)
	stdout, stderr, exit := runCapture(t, []string{"-db", dbPath, "-watermark", wm, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "candidates (prompts with codex_provenance and context=''): 2") {
		t.Errorf("stdout = %q, want 2 candidates", stdout)
	}
	if !strings.Contains(stdout, "derived: 1") || !strings.Contains(stdout, "no_prior_turn: 1") {
		t.Errorf("stdout = %q, want derived: 1 and no_prior_turn: 1", stdout)
	}

	env1 := contextOf(t, dbPath, promptIDs[0])
	if env1.State != stateNoPriorTurn || env1.Role != roleNone {
		t.Errorf("first turn's context = %+v, want state=no_prior_turn role=none", env1)
	}
	env2 := contextOf(t, dbPath, promptIDs[1])
	if env2.State != stateDerived || env2.Role != roleAssistant {
		t.Errorf("second turn's context = %+v, want state=derived role=assistant", env2)
	}
	if env2.Text != "fixture: assistant reply between the two turns" {
		t.Errorf("second turn's derived text = %q, want the exact preceding assistant reply", env2.Text)
	}
	if env2.Truncated {
		t.Errorf("second turn's context reports truncated=true, want false (short fixture text)")
	}

	gotProv := queryOne(t, dbPath, "select count(*) from prompt_context;")
	if gotProv != "2" {
		t.Fatalf("prompt_context row count = %s, want 2", gotProv)
	}
}

// TestNoPriorTurnState locks the "no_prior_turn" typed state directly,
// without going through the real pipeline.
func TestNoPriorTurnState(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixturePath := writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: only turn, no assistant precedes it"}]}}`,
	})
	dbPath := newTestCorpus(t, dir)
	seedRow(t, dbPath, "prompt-1", fixturePath, "", 0, "fixture: only turn, no assistant precedes it")

	wm := recordWatermark(t)
	stdout, stderr, exit := runCapture(t, []string{"-db", dbPath, "-watermark", wm, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "no_prior_turn: 1") {
		t.Errorf("stdout = %q, want no_prior_turn: 1", stdout)
	}
	env := contextOf(t, dbPath, "prompt-1")
	if env.State != stateNoPriorTurn || env.Role != roleNone || env.Text != "" {
		t.Errorf("context = %+v, want state=no_prior_turn role=none text=\"\"", env)
	}
}

// TestSourceUnreadableState locks the "source_unreadable" typed state: a
// codex_provenance row naming a source_file that does not exist right now.
func TestSourceUnreadableState(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	dbPath := newTestCorpus(t, dir)
	missing := filepath.Join(dir, "does-not-exist.jsonl")
	seedRow(t, dbPath, "prompt-1", missing, "sess", 0, "fixture: text for a file that will never exist")

	wm := recordWatermark(t)
	stdout, stderr, exit := runCapture(t, []string{"-db", dbPath, "-watermark", wm, "-apply", "-sessions-root", dir})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "source_unreadable: 1") {
		t.Errorf("stdout = %q, want source_unreadable: 1", stdout)
	}
	env := contextOf(t, dbPath, "prompt-1")
	if env.State != stateSourceUnreadable || env.Role != roleNone || env.Detail == "" {
		t.Errorf("context = %+v, want state=source_unreadable role=none non-empty detail", env)
	}
}

// TestRecordUnresolvedState locks the "record_unresolved" typed state: the
// codex_provenance row's own content_hash no longer matches what re-parsing
// the (still perfectly readable) file finds at that position -- the file's
// shape changed since codexingest ran.
func TestRecordUnresolvedState(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixturePath := writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: live text"}]}}`,
	})
	dbPath := newTestCorpus(t, dir)
	// Seed with a content hash that does NOT match the file's live text --
	// simulating "the file changed since codexingest ingested this row".
	seedRow(t, dbPath, "prompt-1", fixturePath, "", 0, "fixture: STALE text codexingest saw before")

	wm := recordWatermark(t)
	stdout, stderr, exit := runCapture(t, []string{"-db", dbPath, "-watermark", wm, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "record_unresolved: 1") {
		t.Errorf("stdout = %q, want record_unresolved: 1", stdout)
	}
	env := contextOf(t, dbPath, "prompt-1")
	if env.State != stateRecordUnresolved || env.Role != roleNone || env.Detail == "" {
		t.Errorf("context = %+v, want state=record_unresolved role=none non-empty detail", env)
	}
}

// TestWatermarkExcludesFileChangedAfter locks "files changed after it are
// excluded and listed": a source file whose mtime is after -watermark is
// deferred, never derived, never marked source_unreadable.
func TestWatermarkExcludesFileChangedAfter(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixturePath := writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn"}]}}`,
	})
	dbPath := newTestCorpus(t, dir)
	seedRow(t, dbPath, "prompt-1", fixturePath, "", 0, "fixture: turn")

	// A watermark from the past -- the fixture file's real mtime is after it.
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)

	stdout, stderr, exit := runCapture(t, []string{"-db", dbPath, "-watermark", past, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "excluded, source changed after watermark: 1") {
		t.Errorf("stdout = %q, want the row excluded by watermark", stdout)
	}
	if !strings.Contains(stdout, "  excluded: "+fixturePath) {
		t.Errorf("stdout = %q, want the excluded file listed by path", stdout)
	}
	if got := queryOne(t, dbPath, "select context from prompts where id='prompt-1';"); got != "" {
		t.Errorf("context = %q, want still '' -- a watermark-excluded row must not be written", got)
	}
}

// TestTwoIdenticalDryRunsProduceIdenticalOutput locks the "zero-write by
// default" contract's own determinism requirement.
func TestTwoIdenticalDryRunsProduceIdenticalOutput(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixturePath := writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn one"}]}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: reply"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn two"}]}}`,
	})
	dbPath := newTestCorpus(t, dir)
	seedRow(t, dbPath, "prompt-1", fixturePath, "", 0, "fixture: turn one")
	seedRow(t, dbPath, "prompt-2", fixturePath, "", 1, "fixture: turn two")

	wm := recordWatermark(t)
	stdout1, stderr1, exit1 := runCapture(t, []string{"-db", dbPath, "-watermark", wm, "-sessions-root", root})
	stdout2, stderr2, exit2 := runCapture(t, []string{"-db", dbPath, "-watermark", wm, "-sessions-root", root})
	if exit1 != 0 || exit2 != 0 {
		t.Fatalf("exit1=%d exit2=%d stderr1=%s stderr2=%s", exit1, exit2, stderr1, stderr2)
	}
	if stdout1 != stdout2 {
		t.Errorf("two dry runs produced different output:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", stdout1, stdout2)
	}
	if !strings.Contains(stdout1, "mode: dry-run") {
		t.Errorf("stdout = %q, want dry-run mode", stdout1)
	}
	tableExists := queryOne(t, dbPath, "select count(*) from sqlite_master where type='table' and name='prompt_context';")
	if tableExists != "0" {
		t.Fatalf("prompt_context table exists after a dry run, want it never created")
	}
}

// TestIdempotentRerunWritesZero locks "a rerun at that watermark writes
// zero": the second -apply against the same db and watermark selects zero
// candidates because the first run already replaced every context=”.
func TestIdempotentRerunWritesZero(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixturePath := writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: rerun turn"}]}}`,
	})
	dbPath := newTestCorpus(t, dir)
	seedRow(t, dbPath, "prompt-1", fixturePath, "", 0, "fixture: rerun turn")

	wm := recordWatermark(t)
	_, stderr, exit := runCapture(t, []string{"-db", dbPath, "-watermark", wm, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("first run exit=%d stderr=%s", exit, stderr)
	}
	firstCount := queryOne(t, dbPath, "select count(*) from prompt_context;")
	if firstCount != "1" {
		t.Fatalf("prompt_context rows after first run = %s, want 1", firstCount)
	}

	stdout, stderr, exit := runCapture(t, []string{"-db", dbPath, "-watermark", wm, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("second run exit=%d stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "candidates (prompts with codex_provenance and context=''): 0") {
		t.Errorf("second run stdout = %q, want 0 candidates (context is no longer '')", stdout)
	}
	secondCount := queryOne(t, dbPath, "select count(*) from prompt_context;")
	if secondCount != "1" {
		t.Fatalf("prompt_context rows after rerun = %s, want still 1 (zero new rows)", secondCount)
	}
}

// TestRefusesLiveCorpusPath proves this command reuses
// internal/livepath.RefuseLivePath.
func TestRefusesLiveCorpusPath(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	livePath := newTestCorpus(t, dir)
	t.Setenv("ESTATE_CORPUS", livePath)

	wm := recordWatermark(t)
	_, stderr, exit := runCapture(t, []string{"-db", livePath, "-watermark", wm, "-apply", "-sessions-root", dir})
	if exit == 0 {
		t.Fatalf("exit=0, want refusal against the live corpus path; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "refusing") {
		t.Errorf("stderr = %q, want a refusal message", stderr)
	}
	gotRows := queryOne(t, livePath, "select count(*) from sqlite_master where type='table' and name='prompt_context';")
	if gotRows != "0" {
		t.Fatalf("prompt_context table exists after a refused run, want it never created")
	}
}

// TestAuthorizationHasNoEffectWhenDbIsNotLive proves -authorized-live-write
// changes nothing when -db does not resolve to the live path -- no banner,
// ordinary apply.
func TestAuthorizationHasNoEffectWhenDbIsNotLive(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixturePath := writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn"}]}}`,
	})
	dbPath := newTestCorpus(t, dir)
	seedRow(t, dbPath, "prompt-1", fixturePath, "", 0, "fixture: turn")

	wm := recordWatermark(t)
	stdout, stderr, exit := runCapture(t, []string{"-db", dbPath, "-watermark", wm, "-apply", "-sessions-root", root, "-authorized-live-write"})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	if strings.Contains(stderr, "AUTHORIZED LIVE-CORPUS WRITE") {
		t.Errorf("stderr = %q, want no live-write banner against a non-live -db", stderr)
	}
	if !strings.Contains(stdout, "no_prior_turn: 1") {
		t.Errorf("stdout = %q, want the ordinary apply to have run normally", stdout)
	}
}

// TestSourcesNeverTouched proves the fixture file's own mtime is unchanged
// by a run.
func TestSourcesNeverTouched(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixturePath := writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: touch-check turn"}]}}`,
	})
	dbPath := newTestCorpus(t, dir)
	seedRow(t, dbPath, "prompt-1", fixturePath, "", 0, "fixture: touch-check turn")

	before, err := os.Stat(fixturePath)
	if err != nil {
		t.Fatal(err)
	}

	wm := recordWatermark(t)
	_, stderr, exit := runCapture(t, []string{"-db", dbPath, "-watermark", wm, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}

	after, err := os.Stat(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("fixture mtime changed: before=%s after=%s", before.ModTime(), after.ModTime())
	}
}

// TestTruncationIsRecorded locks requirement 3: a preceding assistant turn
// longer than maxContextRunes is capped, and Truncated is recorded true --
// truncated and absent must read as different states.
func TestTruncationIsRecorded(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	longReply := strings.Repeat("x", maxContextRunes+500)
	fixturePath := writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn one"}]}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + longReply + `"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn two"}]}}`,
	})
	dbPath := newTestCorpus(t, dir)
	seedRow(t, dbPath, "prompt-1", fixturePath, "", 0, "fixture: turn one")
	seedRow(t, dbPath, "prompt-2", fixturePath, "", 1, "fixture: turn two")

	wm := recordWatermark(t)
	_, stderr, exit := runCapture(t, []string{"-db", dbPath, "-watermark", wm, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	env := contextOf(t, dbPath, "prompt-2")
	if !env.Truncated {
		t.Errorf("context = %+v, want truncated=true for a reply longer than the cap", env)
	}
	if len([]rune(env.Text)) != maxContextRunes {
		t.Errorf("truncated text length = %d runes, want exactly %d", len([]rune(env.Text)), maxContextRunes)
	}
}
