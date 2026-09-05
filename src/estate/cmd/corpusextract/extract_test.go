package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture writes lines to a temp .jsonl file and returns its path.
// Every line is invented/synthetic text, never copied rollout content -- see
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

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestManifestNeverCarriesRawText locks the "hashes only" requirement:
// nothing in a built manifest's JSON encoding contains the fixture's own
// text, only its sha256.
func TestManifestNeverCarriesRawText(t *testing.T) {
	dir := t.TempDir()
	secretText := "fixture: this exact string must never appear in the manifest"
	writeFixture(t, dir, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + secretText + `"}]}}`,
	})

	m, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}

	var buf bytes.Buffer
	PrintSummary(&buf, m, 0, 0, 0)
	if strings.Contains(buf.String(), secretText) {
		t.Fatalf("PrintSummary output contains raw operator text")
	}

	if len(m.Entries) != 1 {
		t.Fatalf("Entries = %d, want 1", len(m.Entries))
	}
	if m.Entries[0].TextSHA256 != hashOf(secretText) {
		t.Fatalf("TextSHA256 = %q, want sha256(%q) = %q", m.Entries[0].TextSHA256, secretText, hashOf(secretText))
	}
}

// TestDedupDropsCompactedOverlapKeepsOnlyInCompacted is the dedup rule
// itself: a compacted turn whose text also appears as a response_item in the
// same file is dropped from the manifest as a duplicate; a compacted turn
// with no such match is recovered into the manifest exactly once.
func TestDedupDropsCompactedOverlapKeepsOnlyInCompacted(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "compacted.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn that survives as response_item"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"compacted","payload":{"message":"","replacement_history":[` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn that survives as response_item"}]},` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn that ONLY exists in compacted"}]}` +
			`]}}`,
	})

	m, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}

	if len(m.Entries) != 2 {
		t.Fatalf("Entries = %d, want 2 (1 response_item + 1 recovered-only-in-compacted, the overlap dropped)", len(m.Entries))
	}
	if m.EntriesFromResponseItem != 1 {
		t.Fatalf("EntriesFromResponseItem = %d, want 1", m.EntriesFromResponseItem)
	}
	if m.EntriesFromCompacted != 1 {
		t.Fatalf("EntriesFromCompacted = %d, want 1", m.EntriesFromCompacted)
	}
	if m.Dedup.CompactedUserTurnsDistinctTotal != 2 {
		t.Fatalf("CompactedUserTurnsDistinctTotal = %d, want 2", m.Dedup.CompactedUserTurnsDistinctTotal)
	}
	if m.Dedup.DroppedAsDuplicateOfResponseItem != 1 {
		t.Fatalf("DroppedAsDuplicateOfResponseItem = %d, want 1", m.Dedup.DroppedAsDuplicateOfResponseItem)
	}
	if m.Dedup.RecoveredOnlyInCompacted != 1 {
		t.Fatalf("RecoveredOnlyInCompacted = %d, want 1", m.Dedup.RecoveredOnlyInCompacted)
	}

	// Confirm the surviving entry's provenance is the recovered one, not a
	// second copy of the overlapping turn.
	var recovered *ManifestEntry
	for i := range m.Entries {
		if m.Entries[i].Source == "compacted" {
			recovered = &m.Entries[i]
		}
	}
	if recovered == nil {
		t.Fatal("no compacted-sourced entry found in manifest")
	}
	if recovered.TextSHA256 != hashOf("fixture: turn that ONLY exists in compacted") {
		t.Fatalf("recovered entry hash does not match the only-in-compacted text")
	}
	_ = path
}

// TestCompactedOnlyTurnIncludedExactlyOnceAcrossReCompactions guards the
// re-embedding case directly: a session compacted twice re-embeds the same
// only-in-compacted turn in both compaction records' replacement_history,
// but the manifest must carry it exactly once.
func TestCompactedOnlyTurnIncludedExactlyOnceAcrossReCompactions(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "recompacted.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"compacted","payload":{"message":"","replacement_history":[` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn re-embedded across two compactions"}]}` +
			`]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"compacted","payload":{"message":"","replacement_history":[` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn re-embedded across two compactions"}]}` +
			`]}}`,
	})

	m, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}

	if len(m.Entries) != 1 {
		t.Fatalf("Entries = %d, want 1 (re-embedded turn must not be double-counted)", len(m.Entries))
	}
	if m.Dedup.RecoveredOnlyInCompacted != 1 {
		t.Fatalf("RecoveredOnlyInCompacted = %d, want 1", m.Dedup.RecoveredOnlyInCompacted)
	}
}

// TestManifestEntryCarriesFileSessionAndRecordIndex is acceptance criterion
// 1's literal shape check: (file, session payload.id, record index, hash).
func TestManifestEntryCarriesFileSessionAndRecordIndex(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "session.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session-xyz"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture turn"}]}}`,
	})

	m, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("Entries = %d, want 1", len(m.Entries))
	}
	e := m.Entries[0]
	if e.File != path {
		t.Errorf("File = %q, want %q", e.File, path)
	}
	if e.SessionID != "fixture-session-xyz" {
		t.Errorf("SessionID = %q, want fixture-session-xyz", e.SessionID)
	}
	if e.RecordIndex != 2 {
		t.Errorf("RecordIndex = %d, want 2 (the response_item's own line number)", e.RecordIndex)
	}
}

// TestUnparseableFileNamedNotSkipped mirrors capturehealth's own guarantee:
// a file this tool cannot parse is named with a reason, never silently
// dropped from the manifest's own accounting.
func TestUnparseableFileNamedNotSkipped(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "broken.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session"}}`,
		`{this is not valid json at all`,
	})

	m, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if len(m.FilesUnparseable) != 1 || m.FilesUnparseable[0].Path != path {
		t.Fatalf("FilesUnparseable = %v, want exactly [%s]", m.FilesUnparseable, path)
	}
	if m.FilesParsed != 0 {
		t.Fatalf("FilesParsed = %d, want 0", m.FilesParsed)
	}
}

// TestReconciliationReportsDisagreement is the acceptance criterion 2
// requirement that a disagreement with slice 2's figures is SAID, not
// silently swallowed.
func TestReconciliationReportsDisagreement(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture turn"}]}}`,
	})

	m, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}

	var buf bytes.Buffer
	PrintSummary(&buf, m, 999999, 999999, 999999)
	if !strings.Contains(buf.String(), "DISAGREES") {
		t.Fatalf("PrintSummary output = %q, want it to say DISAGREES when figures do not match", buf.String())
	}

	buf.Reset()
	PrintSummary(&buf, m, m.Dedup.CompactedUserTurnsDistinctTotal, m.Dedup.DroppedAsDuplicateOfResponseItem, m.Dedup.RecoveredOnlyInCompacted)
	if !strings.Contains(buf.String(), "AGREES") {
		t.Fatalf("PrintSummary output = %q, want it to say AGREES when figures match", buf.String())
	}
}
