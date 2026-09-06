package catalogue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHealthStateString(t *testing.T) {
	cases := map[HealthState]string{
		HealthMissing:    "Missing",
		HealthUnreadable: "Unreadable",
		HealthEmpty:      "Empty",
		HealthPopulated:  "Populated",
		HealthState(99):  "Unknown",
	}
	for h, want := range cases {
		if got := h.String(); got != want {
			t.Errorf("HealthState(%d).String() = %q, want %q", h, got, want)
		}
	}
}

func TestHealthStateMarshalJSON(t *testing.T) {
	b, err := HealthPopulated.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(b) != `"Populated"` {
		t.Fatalf("MarshalJSON = %s, want \"Populated\"", b)
	}
}

// TestBuildCodexSource_Missing covers a root that does not exist at all --
// recorded as Missing with the path looked for, never omitted.
func TestBuildCodexSource_Missing(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "does-not-exist")
	src := BuildCodexSource(root)
	if src.Health != HealthMissing {
		t.Fatalf("Health = %v, want HealthMissing", src.Health)
	}
	if src.RootPath != root {
		t.Fatalf("RootPath = %q, want %q", src.RootPath, root)
	}
	if !src.ObservedAt.IsZero() {
		t.Fatalf("ObservedAt = %v, want zero value for an unread source", src.ObservedAt)
	}
}

// TestBuildCodexSource_Empty covers a root that exists but holds no *.jsonl
// files.
func TestBuildCodexSource_Empty(t *testing.T) {
	dir := t.TempDir()
	src := BuildCodexSource(dir)
	if src.Health != HealthEmpty {
		t.Fatalf("Health = %v, want HealthEmpty", src.Health)
	}
	if src.UnitCount != 0 {
		t.Fatalf("UnitCount = %d, want 0", src.UnitCount)
	}
}

// TestBuildCodexSource_Populated covers a root with one genuine operator
// turn -- the fixture text below is invented, never a real prompt
// (agent-estate#1139's "never put raw operator prompts into source
// control").
func TestBuildCodexSource_Populated(t *testing.T) {
	dir := t.TempDir()
	fixture := `{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-session-aaa"}}
{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: a fabricated operator turn"}]}}
{"timestamp":"2026-01-01T00:00:02.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: a fabricated assistant reply"}]}}
`
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(fixture), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	before := time.Now()
	src := BuildCodexSource(dir)
	after := time.Now()

	if src.Health != HealthPopulated {
		t.Fatalf("Health = %v, want HealthPopulated", src.Health)
	}
	if src.UnitCount != 1 {
		t.Fatalf("UnitCount = %d, want 1 (one genuine operator turn; the assistant reply must not count)", src.UnitCount)
	}
	if src.ObservedAt.Before(before) || src.ObservedAt.After(after) {
		t.Fatalf("ObservedAt = %v, want between %v and %v", src.ObservedAt, before, after)
	}
}

// TestBuildCodexSource_MtimeUnchanged proves this package never writes to
// or touches a source file -- required by agent-estate#1139's read-only
// constraint.
func TestBuildCodexSource_MtimeUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	fixture := `{"timestamp":"2026-01-01T00:00:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: a fabricated operator turn"}]}}
`
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	BuildCodexSource(dir)

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("mtime changed: before %v, after %v", before.ModTime(), after.ModTime())
	}
}

// TestBuildClaudeSource_Missing mirrors the codex case for the other source.
func TestBuildClaudeSource_Missing(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "does-not-exist")
	src := BuildClaudeSource(root)
	if src.Health != HealthMissing {
		t.Fatalf("Health = %v, want HealthMissing", src.Health)
	}
}

// TestBuildClaudeSource_Populated covers a root shaped like the live tree:
// one project subdirectory, one *.jsonl session file inside it.
func TestBuildClaudeSource_Populated(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "-private-tmp-fixture")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fixture := `{"type":"custom-title","customTitle":"fixture","sessionId":"fixture-session-bbb"}
{"parentUuid":null,"type":"user","message":{"role":"user","content":"fixture: a fabricated message"},"sessionId":"fixture-session-bbb"}
`
	if err := os.WriteFile(filepath.Join(projectDir, "fixture-session-bbb.jsonl"), []byte(fixture), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	src := BuildClaudeSource(dir)
	if src.Health != HealthPopulated {
		t.Fatalf("Health = %v, want HealthPopulated", src.Health)
	}
	if src.UnitCount != 1 {
		t.Fatalf("UnitCount = %d, want 1 session file", src.UnitCount)
	}
	if src.ObservedAt.IsZero() {
		t.Fatalf("ObservedAt is zero, want a measured instant")
	}
}

// TestBuildClaudeSource_Empty covers a root that exists, has a project
// subdirectory, but no *.jsonl files in it.
func TestBuildClaudeSource_Empty(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "empty-project"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := BuildClaudeSource(dir)
	if src.Health != HealthEmpty {
		t.Fatalf("Health = %v, want HealthEmpty", src.Health)
	}
}

// TestBuild_ReturnsAllFourSources is the seeding contract: exactly the two
// real transcript sources plus the two seed-PDF records, never invented
// ones, regardless of what this machine happens to have on disk.
func TestBuild_ReturnsAllFourSources(t *testing.T) {
	cat := Build()
	if len(cat.Sources) != 4 {
		t.Fatalf("len(Sources) = %d, want 4", len(cat.Sources))
	}
	names := map[string]bool{}
	for _, s := range cat.Sources {
		names[s.Name] = true
	}
	if !names["codex-rollouts"] || !names["claude-transcripts"] || !names["seed-pdf-a"] || !names["seed-pdf-b"] {
		t.Fatalf("Sources = %v, want codex-rollouts, claude-transcripts, seed-pdf-a and seed-pdf-b", cat.Sources)
	}
}

// TestBuildUnresolvedPDFSource_MissingWithSearchEvidence is the contract for
// the seed-PDF case specifically: HealthMissing, a real measured ObservedAt
// (the search itself ran at a real instant, even though it found nothing --
// zero would be indistinguishable from "never looked"), no RootPath
// guessed, and a Detail that both names the requesting issue and cites
// where this package looked -- never a filename invented to fill the gap.
func TestBuildUnresolvedPDFSource_MissingWithSearchEvidence(t *testing.T) {
	before := time.Now()
	src := BuildUnresolvedPDFSource("seed-pdf-a")
	after := time.Now()
	if src.Health != HealthMissing {
		t.Fatalf("Health = %v, want HealthMissing", src.Health)
	}
	if src.Harness != "pdf" {
		t.Fatalf("Harness = %q, want \"pdf\"", src.Harness)
	}
	if src.RootPath != "" {
		t.Fatalf("RootPath = %q, want empty -- no candidate path was ever identified", src.RootPath)
	}
	if src.ObservedAt.Before(before) || src.ObservedAt.After(after) {
		t.Fatalf("ObservedAt = %v, want between %v and %v -- the search ran at a real instant, not the zero value", src.ObservedAt, before, after)
	}
	if src.UnitCount != 0 {
		t.Fatalf("UnitCount = %d, want 0", src.UnitCount)
	}
	if !strings.Contains(src.Detail, "referent unresolved") {
		t.Fatalf("Detail = %q, want it to say the referent is unresolved", src.Detail)
	}
	for _, loc := range PDFSearchLocations {
		if !strings.Contains(src.Detail, loc) {
			t.Fatalf("Detail does not contain searched location %q", loc)
		}
	}
}
