package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestPositiveFilterExcludesDeveloperAndNonInputText is rule 1: the filter
// is positive (role=="user" AND content[0].type=="input_text"), never a
// negative "not assistant" filter that would wrongly admit a developer
// instruction or a non-text content item.
func TestPositiveFilterExcludesDeveloperAndNonInputText(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "roles.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session"}}`,
		// developer role: never a genuine operator turn, even with input_text content.
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"fixture: injected developer instruction"}]}}`,
		// user role but non-input_text content (e.g. an image): never extracted.
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_image","text":""}]}}`,
		// the one genuine turn in this fixture.
		`{"timestamp":"2026-01-01T00:00:03.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: the one genuine operator turn"}]}}`,
	})

	m, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("Entries = %d, want 1 (developer role and non-input_text content must both be excluded)", len(m.Entries))
	}
	if m.Entries[0].TextSHA256 != hashOf("fixture: the one genuine operator turn") {
		t.Fatalf("surviving entry does not match the one genuine operator turn")
	}
}

// TestAttributionByMostRecentPrecedingSessionMeta is rule 2: a turn
// attributes to the most recent preceding session_meta.payload.id in file
// order, and a turn following a second session_meta re-attributes to the new
// id, never the first.
func TestAttributionByMostRecentPrecedingSessionMeta(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "two-sessions.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session-one"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn under session one"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"session_meta","payload":{"id":"fixture-session-two"}}`,
		`{"timestamp":"2026-01-01T00:00:03.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn under session two"}]}}`,
	})

	m, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if len(m.Entries) != 2 {
		t.Fatalf("Entries = %d, want 2", len(m.Entries))
	}
	byHash := map[string]ManifestEntry{}
	for _, e := range m.Entries {
		byHash[e.TextSHA256] = e
	}
	first := byHash[hashOf("fixture: turn under session one")]
	second := byHash[hashOf("fixture: turn under session two")]
	if first.SessionID != "fixture-session-one" {
		t.Errorf("first turn's SessionID = %q, want fixture-session-one", first.SessionID)
	}
	if second.SessionID != "fixture-session-two" {
		t.Errorf("second turn's SessionID = %q, want fixture-session-two", second.SessionID)
	}
}

// TestUnknownRecordTypeRejectedLoudly is rule 5: a record whose "type" is
// not in knownRecordTypes refuses the whole file (folded into
// FilesUnparseable with a reason naming the line and the type), never
// silently skipped or bucketed as "other".
func TestUnknownRecordTypeRejectedLoudly(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "unknown-type.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"some_shape_nobody_has_seen","payload":{}}`,
	})

	m, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if len(m.FilesUnparseable) != 1 || m.FilesUnparseable[0].Path != path {
		t.Fatalf("FilesUnparseable = %v, want exactly [%s]", m.FilesUnparseable, path)
	}
	if !strings.Contains(m.FilesUnparseable[0].Reason, "some_shape_nobody_has_seen") {
		t.Fatalf("FilesUnparseable reason = %q, want it to name the unknown type", m.FilesUnparseable[0].Reason)
	}
	if m.FilesParsed != 0 {
		t.Fatalf("FilesParsed = %d, want 0 (the unknown-type file must not be counted as parsed)", m.FilesParsed)
	}
	if len(m.Entries) != 0 {
		t.Fatalf("Entries = %d, want 0 (nothing from a refused file)", len(m.Entries))
	}
}

// TestUnknownRecordTypeMalformedJSONAlsoRejected covers the other half of
// rule 5: a line that is not even valid JSON is refused the same way as a
// recognised-but-unknown type, not treated differently.
func TestUnknownRecordTypeMalformedJSONAlsoRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "malformed.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session"}}`,
		`not even json`,
	})

	m, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if len(m.FilesUnparseable) != 1 {
		t.Fatalf("FilesUnparseable = %v, want exactly 1 entry", m.FilesUnparseable)
	}
}

// TestRerunProducesZeroDuplicates is rule 6: buildManifest is a pure
// function of the fixture bytes on disk -- no timestamp, random value, or
// scan-order dependency enters TextSHA256 or the manifest's shape -- so
// running it twice over the same unchanged fixtures yields byte-identical
// manifests, and no single run ever produces two entries sharing both the
// same SessionID and the same TextSHA256.
func TestRerunProducesZeroDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn A"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02.000Z","type":"compacted","payload":{"message":"","replacement_history":[` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn A"}]},` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: only-in-compacted turn B"}]}` +
			`]}}`,
	})

	m1, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest (run 1): %v", err)
	}
	m2, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest (run 2): %v", err)
	}

	seen := map[string]bool{}
	for _, e := range m1.Entries {
		key := e.SessionID + "\x00" + e.TextSHA256
		if seen[key] {
			t.Fatalf("duplicate entry within one run: session=%q hash=%q", e.SessionID, e.TextSHA256)
		}
		seen[key] = true
	}

	b1, err := json.Marshal(m1)
	if err != nil {
		t.Fatalf("marshal m1: %v", err)
	}
	b2, err := json.Marshal(m2)
	if err != nil {
		t.Fatalf("marshal m2: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("rerun produced a different manifest:\nrun 1: %s\nrun 2: %s", b1, b2)
	}
}

// TestWatermarkExcludesFilesModifiedAfterIt is B1's revised acceptance
// criteria 1 and 4: a source watermark pinned explicitly, before validation,
// excludes any file whose own mtime is after it and NAMES that file by path
// in the manifest -- it is never silently dropped from the count.
func TestWatermarkExcludesFilesModifiedAfterIt(t *testing.T) {
	dir := t.TempDir()
	oldPath := writeFixture(t, dir, "old.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session-old"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn in the pre-watermark file"}]}}`,
	})
	newPath := writeFixture(t, dir, "new.jsonl", []string{
		`{"timestamp":"2026-02-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session-new"}}`,
		`{"timestamp":"2026-02-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn in the post-watermark file"}]}}`,
	})

	watermark := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	older := watermark.Add(-24 * time.Hour)
	newer := watermark.Add(24 * time.Hour)
	if err := os.Chtimes(oldPath, older, older); err != nil {
		t.Fatalf("Chtimes oldPath: %v", err)
	}
	if err := os.Chtimes(newPath, newer, newer); err != nil {
		t.Fatalf("Chtimes newPath: %v", err)
	}

	m, err := buildManifestAtWatermark(dir, watermark)
	if err != nil {
		t.Fatalf("buildManifestAtWatermark: %v", err)
	}

	if m.WatermarkSource != "explicit" {
		t.Errorf("WatermarkSource = %q, want explicit", m.WatermarkSource)
	}
	if m.FilesTotal != 2 {
		t.Fatalf("FilesTotal = %d, want 2", m.FilesTotal)
	}
	if m.FilesIncluded != 1 {
		t.Fatalf("FilesIncluded = %d, want 1", m.FilesIncluded)
	}
	if len(m.ExcludedAfterWatermark) != 1 || m.ExcludedAfterWatermark[0].Path != newPath {
		t.Fatalf("ExcludedAfterWatermark = %v, want exactly [%s]", m.ExcludedAfterWatermark, newPath)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("Entries = %d, want 1 (only the pre-watermark file's turn)", len(m.Entries))
	}
	if m.Entries[0].File != oldPath {
		t.Errorf("Entries[0].File = %q, want %q", m.Entries[0].File, oldPath)
	}
	if m.Entries[0].SessionID != "fixture-session-old" {
		t.Errorf("Entries[0].SessionID = %q, want fixture-session-old", m.Entries[0].SessionID)
	}
}

// TestRerunAtSameWatermarkIsIdentical is B1's revised acceptance criterion 5:
// two runs pinned to the SAME watermark against an unchanged source tree
// produce byte-identical manifests -- same count, same entry set, same
// order.
func TestRerunAtSameWatermarkIsIdentical(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn A"}]}}`,
	})
	writeFixture(t, dir, "b.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session-2"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn B"}]}}`,
	})

	watermark := time.Now().Add(time.Hour)

	m1, err := buildManifestAtWatermark(dir, watermark)
	if err != nil {
		t.Fatalf("buildManifestAtWatermark (run 1): %v", err)
	}
	m2, err := buildManifestAtWatermark(dir, watermark)
	if err != nil {
		t.Fatalf("buildManifestAtWatermark (run 2): %v", err)
	}

	b1, err := json.Marshal(m1)
	if err != nil {
		t.Fatalf("marshal m1: %v", err)
	}
	b2, err := json.Marshal(m2)
	if err != nil {
		t.Fatalf("marshal m2: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("rerun at the same watermark produced a different manifest:\nrun 1: %s\nrun 2: %s", b1, b2)
	}
	if len(m1.Entries) != 2 {
		t.Fatalf("Entries = %d, want 2", len(m1.Entries))
	}
	if len(m1.ExcludedAfterWatermark) != 0 {
		t.Fatalf("ExcludedAfterWatermark = %v, want none (watermark is after both files' mtimes)", m1.ExcludedAfterWatermark)
	}
}

// TestAutoWatermarkExcludesNothing locks the "auto" mode's own contract:
// when no explicit watermark is supplied, the derived watermark is the
// highest mtime seen in this run's own listing pass, so by construction no
// file in that same pass can be excluded.
func TestAutoWatermarkExcludesNothing(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn A"}]}}`,
	})

	m, err := buildManifest(dir)
	if err != nil {
		t.Fatalf("buildManifest: %v", err)
	}
	if m.WatermarkSource != "auto" {
		t.Errorf("WatermarkSource = %q, want auto", m.WatermarkSource)
	}
	if len(m.ExcludedAfterWatermark) != 0 {
		t.Fatalf("ExcludedAfterWatermark = %v, want none", m.ExcludedAfterWatermark)
	}
	if m.FilesIncluded != m.FilesTotal {
		t.Fatalf("FilesIncluded = %d, FilesTotal = %d, want equal under auto watermark", m.FilesIncluded, m.FilesTotal)
	}
}
