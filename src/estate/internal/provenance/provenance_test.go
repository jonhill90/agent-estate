package provenance

import "testing"

// TestIDIsStableAcrossCalls is the idempotent-id requirement agent-estate#1139
// names directly: the same identity fields must always derive the same ID,
// so re-running a backfill over unchanged source data cannot duplicate rows.
func TestIDIsStableAcrossCalls(t *testing.T) {
	p := UnitProvenance{
		SourceName:  "codex-rollout",
		Harness:     "codex",
		SourceFile:  "/tmp/fixture/session.jsonl",
		SessionID:   "fixture-session-aaa",
		RecordIndex: 3,
		ContentHash: HashContent("a fabricated fixture turn"),
	}
	if p.ID() != p.ID() {
		t.Fatal("ID() is not stable across repeated calls on the same value")
	}
}

// TestIDChangesWithAnyIdentityField exercises the doc comment's own claim:
// each of the six identity fields, changed alone, produces a different ID.
// Metadata fields (tested separately below) must NOT do this.
func TestIDChangesWithAnyIdentityField(t *testing.T) {
	base := UnitProvenance{
		SourceName:  "codex-rollout",
		Harness:     "codex",
		SourceFile:  "/tmp/fixture/session.jsonl",
		SessionID:   "fixture-session-aaa",
		RecordIndex: 3,
		ContentHash: HashContent("a fabricated fixture turn"),
	}
	baseID := base.ID()

	variants := []UnitProvenance{
		{SourceName: "claude-transcript", Harness: base.Harness, SourceFile: base.SourceFile, SessionID: base.SessionID, RecordIndex: base.RecordIndex, ContentHash: base.ContentHash},
		{SourceName: base.SourceName, Harness: "claude", SourceFile: base.SourceFile, SessionID: base.SessionID, RecordIndex: base.RecordIndex, ContentHash: base.ContentHash},
		{SourceName: base.SourceName, Harness: base.Harness, SourceFile: "/tmp/fixture/other.jsonl", SessionID: base.SessionID, RecordIndex: base.RecordIndex, ContentHash: base.ContentHash},
		{SourceName: base.SourceName, Harness: base.Harness, SourceFile: base.SourceFile, SessionID: "fixture-session-bbb", RecordIndex: base.RecordIndex, ContentHash: base.ContentHash},
		{SourceName: base.SourceName, Harness: base.Harness, SourceFile: base.SourceFile, SessionID: base.SessionID, RecordIndex: 4, ContentHash: base.ContentHash},
		{SourceName: base.SourceName, Harness: base.Harness, SourceFile: base.SourceFile, SessionID: base.SessionID, RecordIndex: base.RecordIndex, ContentHash: HashContent("a different fabricated turn")},
	}
	for i, v := range variants {
		if v.ID() == baseID {
			t.Errorf("variant %d: ID() unchanged after changing an identity field; got same ID as base", i)
		}
	}
}

// TestIDUnaffectedByMetadataFields is the flip side: CapturedAt, Originator,
// OriginSource, and PriorAssistantContext are documented as metadata, never
// identity -- changing only those must never change ID().
func TestIDUnaffectedByMetadataFields(t *testing.T) {
	base := UnitProvenance{
		SourceName:  "codex-rollout",
		Harness:     "codex",
		SourceFile:  "/tmp/fixture/session.jsonl",
		SessionID:   "fixture-session-aaa",
		RecordIndex: 3,
		ContentHash: HashContent("a fabricated fixture turn"),
	}
	withMetadata := base
	withMetadata.CapturedAt = "2026-01-01T00:00:00Z"
	withMetadata.Originator = "fixture_originator"
	withMetadata.OriginSource = "fixture_source"
	withMetadata.PriorAssistantContext = "a fabricated prior assistant reply"

	if base.ID() != withMetadata.ID() {
		t.Fatal("ID() changed when only metadata fields differed; metadata must never affect identity")
	}
}

// TestIDDoesNotCollideAcrossFieldBoundaries guards the length-prefix framing
// in ID(): without it, SourceFile="ab"+SessionID="c" and SourceFile="a"+
// SessionID="bc" would concatenate to the same string and hash identically,
// silently merging two different units into one ID.
func TestIDDoesNotCollideAcrossFieldBoundaries(t *testing.T) {
	a := UnitProvenance{SourceName: "s", Harness: "h", SourceFile: "ab", SessionID: "c", RecordIndex: 0, ContentHash: "x"}
	b := UnitProvenance{SourceName: "s", Harness: "h", SourceFile: "a", SessionID: "bc", RecordIndex: 0, ContentHash: "x"}
	if a.ID() == b.ID() {
		t.Fatal("ID() collided across a field boundary; length-prefix framing is not preventing concatenation ambiguity")
	}
}

func TestHashContentIsDeterministic(t *testing.T) {
	if HashContent("same text") != HashContent("same text") {
		t.Fatal("HashContent is not deterministic for identical input")
	}
	if HashContent("text one") == HashContent("text two") {
		t.Fatal("HashContent collided for different input")
	}
}
