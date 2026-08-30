package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSession writes name.jsonl under dir/project, seeding a REAL
// on-disk session transcript in this format's actual shape (captured from
// this box's own ~/.claude/projects, not invented) -- the same "seed a
// temp store, read it back, assert on the rows" discipline every other
// real-source test in this repo uses (board/ledger_test.go,
// cost/ccusage_test.go), never a mock asserting against itself.
func writeSession(t *testing.T, dir, project, sessionID string, lines []string) string {
	t.Helper()
	projectDir := filepath.Join(dir, project)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// A minimal but REAL conversation: a user prompt, an assistant tool_use,
// the matching tool_result, and an assistant text reply -- exactly the
// four envelope shapes ClaudeCodeSource's own doc comment says this
// format actually contains, captured from a real transcript on this box.
func sampleConversation(title string) []string {
	return []string{
		`{"type":"ai-title","aiTitle":"` + title + `","sessionId":"s1"}`,
		`{"type":"user","message":{"role":"user","content":"read the brief and act on it"},"timestamp":"2026-08-22T18:40:10.000Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/tmp/x"}}]},"timestamp":"2026-08-22T18:40:11.000Z"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file contents"}]},"timestamp":"2026-08-22T18:40:12.000Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done reading, here is my answer"}]},"timestamp":"2026-08-22T18:40:13.000Z"}`,
	}
}

func TestClaudeCodeSourceReadsARealSeededSession(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "-Users-jon-source-repos-Personal-agent-tui", "s1", sampleConversation("real seeded test session"))

	src := NewClaudeCodeSource(dir)
	threads, err := src.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("got %d threads, want 1", len(threads))
	}
	th := threads[0]
	if th.ID != "s1" {
		t.Errorf("ID = %q, want %q", th.ID, "s1")
	}
	if th.Title != "real seeded test session" {
		t.Errorf("Title = %q, want the ai-title line's own text", th.Title)
	}
	if th.Fixture {
		t.Error("a real ClaudeCodeSource thread must never be tagged Fixture")
	}
	if len(th.Messages) != 3 {
		t.Fatalf("got %d messages, want 3 (user text, tool call, agent text) -- got %+v", len(th.Messages), th.Messages)
	}

	if th.Messages[0].Kind != KindUserText || th.Messages[0].Text != "read the brief and act on it" {
		t.Errorf("message 0 = %+v, want the real user prompt", th.Messages[0])
	}
	if th.Messages[1].Kind != KindToolCall || th.Messages[1].ToolName != "Read" {
		t.Errorf("message 1 = %+v, want the real Read tool call", th.Messages[1])
	}
	if th.Messages[1].ToolStatus != ToolDone {
		t.Errorf("tool call status = %q, want %q -- its real tool_result (no is_error) arrived", th.Messages[1].ToolStatus, ToolDone)
	}
	if th.Messages[2].Kind != KindAgentText || th.Messages[2].Text != "done reading, here is my answer" {
		t.Errorf("message 2 = %+v, want the real agent reply", th.Messages[2])
	}

	wantLast := time.Date(2026, 8, 22, 18, 40, 13, 0, time.UTC)
	if !th.LastActivity.Equal(wantLast) {
		t.Errorf("LastActivity = %v, want %v", th.LastActivity, wantLast)
	}
}

func TestClaudeCodeSourceMarksAFailedToolCall(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"run it"},"timestamp":"2026-08-22T18:40:10.000Z"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_2","name":"Bash","input":{}}]},"timestamp":"2026-08-22T18:40:11.000Z"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"boom","is_error":true}]},"timestamp":"2026-08-22T18:40:12.000Z"}`,
	}
	writeSession(t, dir, "proj", "s2", lines)

	threads, err := NewClaudeCodeSource(dir).Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 || len(threads[0].Messages) != 2 {
		t.Fatalf("got %+v, want one thread with 2 messages", threads)
	}
	if got := threads[0].Messages[1].ToolStatus; got != ToolFailed {
		t.Errorf("ToolStatus = %q, want %q (is_error:true arrived)", got, ToolFailed)
	}
}

func TestClaudeCodeSourceLeavesAnUnansweredToolCallPending(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_3","name":"Bash","input":{}}]},"timestamp":"2026-08-22T18:40:11.000Z"}`,
	}
	writeSession(t, dir, "proj", "s3", lines)

	threads, err := NewClaudeCodeSource(dir).Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 || len(threads[0].Messages) != 1 {
		t.Fatalf("got %+v, want one thread with 1 message", threads)
	}
	// The session file ends before any tool_result arrives -- honest
	// "still pending," never guessed as done (agent-b3.md: never let real
	// absence look like a real answer).
	if got := threads[0].Messages[0].ToolStatus; got != ToolPending {
		t.Errorf("ToolStatus = %q, want %q (no tool_result ever arrived)", got, ToolPending)
	}
}

func TestClaudeCodeSourcePicksEachProjectsNewestSessionOnly(t *testing.T) {
	dir := t.TempDir()
	oldPath := writeSession(t, dir, "proj", "old", []string{
		`{"type":"user","message":{"role":"user","content":"old"},"timestamp":"2026-08-01T00:00:00.000Z"}`,
	})
	newPath := writeSession(t, dir, "proj", "new", []string{
		`{"type":"user","message":{"role":"user","content":"new"},"timestamp":"2026-08-22T00:00:00.000Z"}`,
	})
	now := time.Now()
	if err := os.Chtimes(oldPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if err := os.Chtimes(newPath, now, now); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	threads, err := NewClaudeCodeSource(dir).Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("got %d threads, want 1 (one project directory, its newest session only): %+v", len(threads), threads)
	}
	if threads[0].ID != "new" {
		t.Errorf("ID = %q, want the newer session %q", threads[0].ID, "new")
	}
}

func TestClaudeCodeSourceTruncatesToMaxMessagesPerThread(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < maxMessagesPerThread+20; i++ {
		lines = append(lines, `{"type":"user","message":{"role":"user","content":"msg"},"timestamp":"2026-08-22T00:00:`+twoDigit(i%60)+`.000Z"}`)
	}
	writeSession(t, dir, "proj", "s4", lines)

	threads, err := NewClaudeCodeSource(dir).Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("got %d threads, want 1", len(threads))
	}
	if len(threads[0].Messages) != maxMessagesPerThread {
		t.Errorf("got %d messages, want the truncated cap %d", len(threads[0].Messages), maxMessagesPerThread)
	}
}

func twoDigit(n int) string {
	s := "0123456789"
	return string([]byte{s[n/10], s[n%10]})
}

func TestClaudeCodeSourceEmptyProjectsDirIsNotConfigured(t *testing.T) {
	_, err := NewClaudeCodeSource("").Threads()
	if err != ErrNoProjectDir {
		t.Fatalf("error = %v, want ErrNoProjectDir", err)
	}
}

func TestClaudeCodeSourceMissingProjectsDirIsNotConfigured(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")
	_, err := NewClaudeCodeSource(missing).Threads()
	if err != ErrNoProjectDir {
		t.Fatalf("error = %v, want ErrNoProjectDir", err)
	}
}

func TestClaudeCodeSourceConfiguredButNoSessionsIsRealEmptyNotAnError(t *testing.T) {
	dir := t.TempDir()
	// A project directory that exists but holds no .jsonl file at all --
	// a real, configured directory with genuinely nothing in it, distinct
	// from ErrNoProjectDir.
	if err := os.MkdirAll(filepath.Join(dir, "empty-project"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	threads, err := NewClaudeCodeSource(dir).Threads()
	if err != nil {
		t.Fatalf("error = %v, want nil (configured, genuinely zero sessions)", err)
	}
	if len(threads) != 0 {
		t.Fatalf("got %d threads, want 0", len(threads))
	}
}
