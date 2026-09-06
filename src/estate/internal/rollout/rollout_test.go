package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture writes lines to a temp .jsonl file and returns its path.
// Every string here is invented fixture text, never a real captured prompt.
func writeFixture(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "a.jsonl")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

// TestPriorAssistantTextResponseItem locks the ordinary case: the operator
// turn's PriorAssistantText is the immediately preceding assistant reply.
func TestPriorAssistantTextResponseItem(t *testing.T) {
	path := writeFixture(t, []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: opens the session"}]}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: first reply"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: second turn"}]}}`,
	})
	fa, err := AnalyzeFile(path)
	if err != nil {
		t.Fatalf("AnalyzeFile: %v", err)
	}
	if len(fa.Turns) != 2 {
		t.Fatalf("Turns = %d, want 2", len(fa.Turns))
	}
	if fa.Turns[0].PriorAssistantText != "" {
		t.Errorf("Turns[0].PriorAssistantText = %q, want \"\" (this turn opens the session)", fa.Turns[0].PriorAssistantText)
	}
	if fa.Turns[1].PriorAssistantText != "fixture: first reply" {
		t.Errorf("Turns[1].PriorAssistantText = %q, want the preceding assistant reply", fa.Turns[1].PriorAssistantText)
	}
}

// TestPriorAssistantTextSkipsReasoningAndDeveloper proves a role-less
// reasoning item and a role=="developer" turn interleaved between an
// assistant reply and the next operator turn do not erase the assistant
// reply that actually precedes it.
func TestPriorAssistantTextSkipsReasoningAndDeveloper(t *testing.T) {
	path := writeFixture(t, []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn one"}]}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: the real preceding reply"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"reasoning","content":[{"type":"text","text":"fixture: role-less reasoning, must not be treated as assistant text"}]}}`,
		`{"timestamp":"2026-01-01T00:00:03Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"fixture: injected instruction, not an assistant turn"}]}}`,
		`{"timestamp":"2026-01-01T00:00:04Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn two"}]}}`,
	})
	fa, err := AnalyzeFile(path)
	if err != nil {
		t.Fatalf("AnalyzeFile: %v", err)
	}
	if len(fa.Turns) != 2 {
		t.Fatalf("Turns = %d, want 2", len(fa.Turns))
	}
	if got := fa.Turns[1].PriorAssistantText; got != "fixture: the real preceding reply" {
		t.Errorf("Turns[1].PriorAssistantText = %q, want the assistant reply through the reasoning/developer noise", got)
	}
}

// TestPriorAssistantTextCompactedUsesReplacementHistoryOrder proves a
// CompactedOnlyInCompacted turn's PriorAssistantText comes from its OWN
// replacement_history array's order, not the outer file's record order --
// the compacted record here is logged well after an assistant reply that
// has nothing to do with the recovered history.
func TestPriorAssistantTextCompactedUsesReplacementHistoryOrder(t *testing.T) {
	path := writeFixture(t, []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: unrelated later reply, not the recovered turn's own predecessor"}]}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"compacted","payload":{"replacement_history":[` +
			`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: recovered predecessor reply"}]},` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: recovered-only turn"}]}` +
			`]}}`,
	})
	fa, err := AnalyzeFile(path)
	if err != nil {
		t.Fatalf("AnalyzeFile: %v", err)
	}
	if fa.CompactedOnlyInCompacted != 1 {
		t.Fatalf("CompactedOnlyInCompacted = %d, want 1", fa.CompactedOnlyInCompacted)
	}
	if len(fa.Turns) != 1 {
		t.Fatalf("Turns = %d, want 1", len(fa.Turns))
	}
	if got := fa.Turns[0].PriorAssistantText; got != "fixture: recovered predecessor reply" {
		t.Errorf("Turns[0].PriorAssistantText = %q, want the replacement_history's own preceding reply, not the outer file's", got)
	}
}

// TestPriorAssistantTextEmptyOutputTextLeavesStateUnchanged proves an
// assistant message with no output_text content parts does not reset
// lastAssistantText to "" -- it is simply skipped.
func TestPriorAssistantTextEmptyOutputTextLeavesStateUnchanged(t *testing.T) {
	path := writeFixture(t, []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn one"}]}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: real reply"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"refusal","text":"fixture: no output_text part at all"}]}}`,
		`{"timestamp":"2026-01-01T00:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: turn two"}]}}`,
	})
	fa, err := AnalyzeFile(path)
	if err != nil {
		t.Fatalf("AnalyzeFile: %v", err)
	}
	if len(fa.Turns) != 2 {
		t.Fatalf("Turns = %d, want 2", len(fa.Turns))
	}
	if got := fa.Turns[1].PriorAssistantText; got != "fixture: real reply" {
		t.Errorf("Turns[1].PriorAssistantText = %q, want the last message that actually had output_text content", got)
	}
}
