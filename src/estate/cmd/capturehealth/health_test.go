package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture writes lines to a temp .jsonl file and returns its path. Every
// line here is invented/synthetic text, never copied rollout content -- see
// agent-estate#1139's "never put raw operator prompts into source control".
func writeFixture(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

// TestSessionIDReadFromPayloadID is correction 1: the session id is
// session_meta.payload.id, never a top-level or payload "session_id" field.
func TestSessionIDReadFromPayloadID(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "session.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session-aaa","session_id":"WRONG-FIELD-DO-NOT-READ","cwd":"/tmp/fixture","originator":"fixture_originator","source":"fixture"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"a fabricated instruction for the fixture, not a real prompt"}]}}`,
	})

	fr, err := analyzeFile(path)
	if err != nil {
		t.Fatalf("analyzeFile: %v", err)
	}
	if len(fr.SessionIDs) != 1 || fr.SessionIDs[0] != "fixture-session-aaa" {
		t.Fatalf("SessionIDs = %v, want [fixture-session-aaa] (must read payload.id, not payload.session_id)", fr.SessionIDs)
	}
}

// TestDeveloperRoleNeverCountsAsOperatorTurn is correction 2: role=="developer"
// must never be counted as a genuine operator turn, and must never be folded
// into "assistant" or any other bucket that a negative "not assistant" filter
// would produce -- it gets its own count.
func TestDeveloperRoleNeverCountsAsOperatorTurn(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "developer.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"fixture: an injected system instruction, not the operator speaking"}]}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: a genuine operator turn"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: an assistant reply"}]}}`,
	})

	fr, err := analyzeFile(path)
	if err != nil {
		t.Fatalf("analyzeFile: %v", err)
	}
	if fr.OperatorTurns != 1 {
		t.Fatalf("OperatorTurns = %d, want 1 (the developer and assistant records must not count)", fr.OperatorTurns)
	}
	if fr.RoleCounts["developer"] != 1 {
		t.Fatalf("RoleCounts[developer] = %d, want 1 (developer must be counted, not dropped)", fr.RoleCounts["developer"])
	}
	if fr.RoleCounts["assistant"] != 1 {
		t.Fatalf("RoleCounts[assistant] = %d, want 1", fr.RoleCounts["assistant"])
	}
	if fr.RoleCounts["user"] != 1 {
		t.Fatalf("RoleCounts[user] = %d, want 1", fr.RoleCounts["user"])
	}
}

// TestGenuineOperatorTurnPredicateIsPositive locks the predicate itself: a
// role=="developer" record must fail it even though it is not "assistant" --
// proving the filter is positive (role==user AND content[0].type==input_text),
// never the negative "not assistant" the issue calls out as the failure mode.
func TestGenuineOperatorTurnPredicateIsPositive(t *testing.T) {
	cases := []struct {
		name string
		p    responseItemPayload
		want bool
	}{
		{"user input_text", responseItemPayload{Role: "user", Content: []contentItem{{Type: "input_text"}}}, true},
		{"developer input_text", responseItemPayload{Role: "developer", Content: []contentItem{{Type: "input_text"}}}, false},
		{"assistant output_text", responseItemPayload{Role: "assistant", Content: []contentItem{{Type: "output_text"}}}, false},
		{"user input_image", responseItemPayload{Role: "user", Content: []contentItem{{Type: "input_image"}}}, false},
		{"user no content", responseItemPayload{Role: "user"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := genuineOperatorTurn(c.p); got != c.want {
				t.Errorf("genuineOperatorTurn(%+v) = %v, want %v", c.p, got, c.want)
			}
		})
	}
}

// TestUnknownRecordTypesAreCountedNotDropped is correction 3: event_msg and
// turn_context (the two the issue names), plus a made-up further type
// standing in for "whatever a future rollout adds", must all show up in
// RecordTypeCounts rather than only the four types a hardcoded enum would
// recognise.
func TestUnknownRecordTypesAreCountedNotDropped(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "mixed.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session-bbb","cwd":"/tmp","originator":"fixture","source":"fixture"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"event_msg","payload":{"fixture":"event"}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"turn_context","payload":{"fixture":"context"}}`,
		`{"timestamp":"2026-01-01T00:00:03.000Z","type":"a_future_record_type_nobody_has_named_yet","payload":{"fixture":true}}`,
		`{"timestamp":"2026-01-01T00:00:04.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture turn"}]}}`,
	})

	fr, err := analyzeFile(path)
	if err != nil {
		t.Fatalf("analyzeFile: %v", err)
	}
	want := map[string]int{
		"session_meta": 1,
		"event_msg":    1,
		"turn_context": 1,
		"a_future_record_type_nobody_has_named_yet": 1,
		"response_item": 1,
	}
	for k, v := range want {
		if fr.RecordTypeCounts[k] != v {
			t.Errorf("RecordTypeCounts[%q] = %d, want %d (record types must not require a hardcoded enum)", k, fr.RecordTypeCounts[k], v)
		}
	}
}

// TestSessionMetaSourceMayBeAnObject guards the real regression this suite
// caught over the live ~/.codex/sessions tree: a forked/subagent session's
// session_meta.payload.source is an object, not the plain string an ordinary
// top-level session carries. Decoding session_meta typed against every field
// must not fail the whole file (and drop its genuine operator turns) just
// because an UNUSED field's shape varies -- only payload.id is required.
func TestSessionMetaSourceMayBeAnObject(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "forked.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-forked-session","originator":"fixture-cli","source":{"subagent":{"thread_spawn":{"parent_thread_id":"fixture-parent","depth":1}}}}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture turn inside a forked session"}]}}`,
	})

	fr, err := analyzeFile(path)
	if err != nil {
		t.Fatalf("analyzeFile: %v (object-shaped payload.source must not fail the file)", err)
	}
	if len(fr.SessionIDs) != 1 || fr.SessionIDs[0] != "fixture-forked-session" {
		t.Fatalf("SessionIDs = %v, want [fixture-forked-session]", fr.SessionIDs)
	}
	if fr.OperatorTurns != 1 {
		t.Fatalf("OperatorTurns = %d, want 1 (must not be lost to a session_meta decode failure)", fr.OperatorTurns)
	}
}

// TestUnparseableFileReportedWithPathAndReason ensures a file that fails to
// parse is named, with a reason, and never silently skipped.
func TestUnparseableFileReportedWithPathAndReason(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "broken.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session-ccc"}}`,
		`{this is not valid json at all`,
	})

	_, err := analyzeFile(path)
	if err == nil {
		t.Fatal("analyzeFile: want error for malformed line, got nil")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q does not name the failing line", err.Error())
	}

	report, buildErr := buildReport(dir)
	if buildErr != nil {
		t.Fatalf("buildReport: %v", buildErr)
	}
	if len(report.FilesUnparseable) != 1 {
		t.Fatalf("FilesUnparseable = %v, want exactly 1 entry", report.FilesUnparseable)
	}
	if report.FilesUnparseable[0].Path != path {
		t.Errorf("FilesUnparseable[0].Path = %q, want %q", report.FilesUnparseable[0].Path, path)
	}
	if report.FilesUnparseable[0].Reason == "" {
		t.Error("FilesUnparseable[0].Reason is empty, want a reason")
	}
	if report.FilesParsed != 0 {
		t.Errorf("FilesParsed = %d, want 0 (the broken file must not count as parsed)", report.FilesParsed)
	}
}

// TestMultiSessionFileAttributesTurnsToCorrectSession is K2 slice 2's first
// settled question (agent-estate#1139): a rollout file CAN carry more than
// one session_meta.payload.id, and turns must attribute to whichever session
// most recently preceded them in file order, never all fold into the file's
// first session.
func TestMultiSessionFileAttributesTurnsToCorrectSession(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "multi-session.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session-one"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: first session, turn 1"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: first session, turn 2"}]}}`,
		`{"timestamp":"2026-01-01T00:00:03.000Z","type":"session_meta","payload":{"id":"fixture-session-two"}}`,
		`{"timestamp":"2026-01-01T00:00:04.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: second session, only turn"}]}}`,
	})

	fr, err := analyzeFile(path)
	if err != nil {
		t.Fatalf("analyzeFile: %v", err)
	}
	if len(fr.SessionIDs) != 2 || fr.SessionIDs[0] != "fixture-session-one" || fr.SessionIDs[1] != "fixture-session-two" {
		t.Fatalf("SessionIDs = %v, want [fixture-session-one fixture-session-two]", fr.SessionIDs)
	}
	if fr.OperatorTurns != 3 {
		t.Fatalf("OperatorTurns = %d, want 3", fr.OperatorTurns)
	}
	if len(fr.Sessions) != 2 {
		t.Fatalf("Sessions = %v, want 2 entries", fr.Sessions)
	}
	if fr.Sessions[0].SessionID != "fixture-session-one" || fr.Sessions[0].OperatorTurns != 2 {
		t.Errorf("Sessions[0] = %+v, want {fixture-session-one 2}", fr.Sessions[0])
	}
	if fr.Sessions[1].SessionID != "fixture-session-two" || fr.Sessions[1].OperatorTurns != 1 {
		t.Errorf("Sessions[1] = %+v, want {fixture-session-two 1}", fr.Sessions[1])
	}
}

// TestBuildReportCountsFilesWithMultipleSessions is the report-level
// counterpart: FilesWithMultipleSessions must count only files with more than
// one DISTINCT session id, not a file that merely repeats session_meta for
// the same id (which is not a second session -- see SessionMetaRecords).
func TestBuildReportCountsFilesWithMultipleSessions(t *testing.T) {
	root := t.TempDir()

	writeFixture(t, root, "single.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-single"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: single-session turn"}]}}`,
	})
	writeFixture(t, root, "repeated-same-id.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-repeat"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: repeated-session turn"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"session_meta","payload":{"id":"fixture-repeat"}}`,
	})
	writeFixture(t, root, "two-sessions.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-multi-a"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: multi-session A turn"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"session_meta","payload":{"id":"fixture-multi-b"}}`,
		`{"timestamp":"2026-01-01T00:00:03.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: multi-session B turn"}]}}`,
	})

	report, err := buildReport(root)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	if report.FilesWithMultipleSessions != 1 {
		t.Fatalf("FilesWithMultipleSessions = %d, want 1 (only two-sessions.jsonl has 2 DISTINCT ids)", report.FilesWithMultipleSessions)
	}
	if report.SessionMetaTotal != 5 {
		t.Fatalf("SessionMetaTotal = %d, want 5 (1 single + 2 repeated-same-id + 2 two-sessions raw session_meta records)", report.SessionMetaTotal)
	}
}

// TestCompactedOverlapWithResponseItem is K2 slice 2's second settled
// question (agent-estate#1139): a compacted record's replacement_history CAN
// carry a turn that ALSO appears as an ordinary response_item elsewhere in
// the same file (the double-count risk) and CAN carry a turn that appears
// NOWHERE else (the loss risk) -- both in the same fixture, so both are
// distinguished rather than folded into one count.
func TestCompactedOverlapWithResponseItem(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "compacted.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-compacted-session"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn that survives as response_item"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: assistant reply"}]}}`,
		`{"timestamp":"2026-01-01T00:00:03.000Z","type":"compacted","payload":{"message":"","replacement_history":[` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn that survives as response_item"}]},` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn that ONLY exists in compacted"}]},` +
			`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: assistant reply"}]}` +
			`]}}`,
	})

	fr, err := analyzeFile(path)
	if err != nil {
		t.Fatalf("analyzeFile: %v", err)
	}
	if fr.CompactedRecords != 1 {
		t.Fatalf("CompactedRecords = %d, want 1", fr.CompactedRecords)
	}
	// Two user/input_text entries in replacement_history (the assistant
	// entry is not a role=="user" turn at all).
	if fr.CompactedUserTurnsRaw != 2 {
		t.Fatalf("CompactedUserTurnsRaw = %d, want 2", fr.CompactedUserTurnsRaw)
	}
	if fr.CompactedUserTurnsDistinct != 2 {
		t.Fatalf("CompactedUserTurnsDistinct = %d, want 2", fr.CompactedUserTurnsDistinct)
	}
	if fr.CompactedOverlapWithResponseItem != 1 {
		t.Fatalf("CompactedOverlapWithResponseItem = %d, want 1 (the turn that also appears as response_item)", fr.CompactedOverlapWithResponseItem)
	}
	if fr.CompactedOnlyInCompacted != 1 {
		t.Fatalf("CompactedOnlyInCompacted = %d, want 1 (the turn found ONLY in compacted)", fr.CompactedOnlyInCompacted)
	}
}

// TestCompactedReembeddingDoesNotInflateDistinctCount guards the raw-vs-
// distinct distinction directly: a session compacted TWICE re-embeds its
// full prior history in the second compaction's replacement_history, so the
// same turn appears twice in CompactedUserTurnsRaw but must count once in
// CompactedUserTurnsDistinct (this is exactly what the live tree does --
// see this package's doc comment, 9,158 raw vs 1,579 distinct).
func TestCompactedReembeddingDoesNotInflateDistinctCount(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "recompacted.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-recompacted"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"compacted","payload":{"message":"","replacement_history":[` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn re-embedded across two compactions"}]}` +
			`]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"compacted","payload":{"message":"","replacement_history":[` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn re-embedded across two compactions"}]}` +
			`]}}`,
	})

	fr, err := analyzeFile(path)
	if err != nil {
		t.Fatalf("analyzeFile: %v", err)
	}
	if fr.CompactedRecords != 2 {
		t.Fatalf("CompactedRecords = %d, want 2", fr.CompactedRecords)
	}
	if fr.CompactedUserTurnsRaw != 2 {
		t.Fatalf("CompactedUserTurnsRaw = %d, want 2 (re-embedded once per compaction)", fr.CompactedUserTurnsRaw)
	}
	if fr.CompactedUserTurnsDistinct != 1 {
		t.Fatalf("CompactedUserTurnsDistinct = %d, want 1 (same text re-embedded, not a second turn)", fr.CompactedUserTurnsDistinct)
	}
	if fr.CompactedOnlyInCompacted != 1 {
		t.Fatalf("CompactedOnlyInCompacted = %d, want 1", fr.CompactedOnlyInCompacted)
	}
}

// TestBuildReportAggregatesAcrossFiles is a small end-to-end sanity check
// over a synthetic multi-file tree, confirming aggregation sums per-file
// counts correctly and walks subdirectories the way ~/.codex/sessions/YYYY/MM/DD
// is actually laid out.
func TestBuildReportAggregatesAcrossFiles(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "2026", "01", "01")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeFixture(t, sub, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-a"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture a turn 1"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture a turn 2"}]}}`,
	})
	writeFixture(t, sub, "b.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-b"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture b turn 1"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"fixture b developer instruction"}]}}`,
	})
	// A non-.jsonl file must be ignored entirely.
	writeFixture(t, sub, "notes.txt", []string{"not a rollout file"})

	report, err := buildReport(root)
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	if report.FilesTotal != 2 {
		t.Fatalf("FilesTotal = %d, want 2 (notes.txt must be excluded)", report.FilesTotal)
	}
	if report.OperatorTurnsTotal != 3 {
		t.Fatalf("OperatorTurnsTotal = %d, want 3", report.OperatorTurnsTotal)
	}
	if report.RoleCounts["developer"] != 1 {
		t.Fatalf("RoleCounts[developer] = %d, want 1", report.RoleCounts["developer"])
	}
	if len(report.FilesUnparseable) != 0 {
		t.Fatalf("FilesUnparseable = %v, want none", report.FilesUnparseable)
	}
}
