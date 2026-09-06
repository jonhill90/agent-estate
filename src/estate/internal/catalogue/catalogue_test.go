package catalogue

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
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
	if !names["codex-rollouts"] || !names["claude-transcripts"] ||
		!names["seed-pdf-continual-harness"] || !names["seed-pdf-agentic-engineering-google"] {
		t.Fatalf("Sources = %v, want codex-rollouts, claude-transcripts, seed-pdf-continual-harness and seed-pdf-agentic-engineering-google", cat.Sources)
	}
}

// writeFixturePDF writes a minimal but structurally valid PDF containing n
// page objects plus one page-tree root, so countPDFPageObjects has real
// /Type/Page and /Type/Pages markers to distinguish -- never real document
// content, per agent-estate#1139's constraint on this package's own tests.
func writeFixturePDF(t *testing.T, path string, n int) []byte {
	t.Helper()
	var kids strings.Builder
	var pages strings.Builder
	for i := 1; i <= n; i++ {
		if i > 1 {
			kids.WriteString(" ")
		}
		kids.WriteString(strconv.Itoa(i+1) + " 0 R")
		pages.WriteString(strconv.Itoa(i+1) + " 0 obj\n<< /Type /Page /Parent 1 0 R >>\nendobj\n")
	}
	content := "%PDF-1.4\n1 0 obj\n<< /Type /Pages /Kids [" + kids.String() +
		"] /Count " + strconv.Itoa(n) + " >>\nendobj\n" + pages.String()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture pdf: %v", err)
	}
	return []byte(content)
}

// TestCountPDFPageObjects checks the marker-counting heuristic against a
// fixture with a known page count, and specifically that the page-tree
// root's own /Type /Pages entry is excluded rather than double-counted.
func TestCountPDFPageObjects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.pdf")
	data := writeFixturePDF(t, path, 5)
	if got := countPDFPageObjects(data); got != 5 {
		t.Fatalf("countPDFPageObjects = %d, want 5", got)
	}
}

// TestBuildSeedPDFSource_Missing covers a descriptor whose path does not
// exist on this machine -- the case every non-operator machine (e.g. CI)
// hits, since these two files live only under the operator's own $HOME.
func TestBuildSeedPDFSource_Missing(t *testing.T) {
	dir := t.TempDir()
	d := SeedPDFDescriptor{
		Name:           "fixture-missing",
		Path:           filepath.Join(dir, "does-not-exist.pdf"),
		RecordedSHA256: "deadbeef",
	}
	src := BuildSeedPDFSource(d)
	if src.Health != HealthMissing {
		t.Fatalf("Health = %v, want HealthMissing", src.Health)
	}
	if src.Harness != "pdf" {
		t.Fatalf("Harness = %q, want \"pdf\"", src.Harness)
	}
	if !src.ObservedAt.IsZero() {
		t.Fatalf("ObservedAt = %v, want zero value for an unread source", src.ObservedAt)
	}
}

// TestBuildSeedPDFSource_HashMatch covers the case both real seed PDFs are
// in: a live file whose computed SHA-256 matches RecordedSHA256. Detail must
// say so and UnitCount must reflect the live page count, not a hardcoded one.
func TestBuildSeedPDFSource_HashMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.pdf")
	data := writeFixturePDF(t, path, 3)
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	d := SeedPDFDescriptor{Name: "fixture-match", Path: path, RecordedSHA256: want}
	src := BuildSeedPDFSource(d)

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("mtime changed: before %v, after %v", before.ModTime(), after.ModTime())
	}

	if src.Health != HealthPopulated {
		t.Fatalf("Health = %v, want HealthPopulated", src.Health)
	}
	if src.UnitCount != 3 {
		t.Fatalf("UnitCount = %d, want 3", src.UnitCount)
	}
	if src.ObservedSHA256 != want {
		t.Fatalf("ObservedSHA256 = %q, want %q", src.ObservedSHA256, want)
	}
	if src.RecordedSHA256 != want {
		t.Fatalf("RecordedSHA256 = %q, want %q", src.RecordedSHA256, want)
	}
	if !strings.Contains(src.Detail, "verified") || strings.Contains(src.Detail, "MISMATCH") {
		t.Fatalf("Detail = %q, want it to say the hash verified, not mismatched", src.Detail)
	}
}

// TestBuildSeedPDFSource_HashMismatch covers the case RecordedSHA256 no
// longer matches the live file: this must be reported as a mismatch, never
// silently corrected or silently trusted.
func TestBuildSeedPDFSource_HashMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.pdf")
	writeFixturePDF(t, path, 2)

	d := SeedPDFDescriptor{Name: "fixture-mismatch", Path: path, RecordedSHA256: "0000000000000000000000000000000000000000000000000000000000000000"}
	src := BuildSeedPDFSource(d)

	if src.Health != HealthPopulated {
		t.Fatalf("Health = %v, want HealthPopulated (file is readable; the mismatch is a Detail finding, not an unreadable source)", src.Health)
	}
	if !strings.Contains(src.Detail, "MISMATCH") {
		t.Fatalf("Detail = %q, want it to flag the SHA-256 mismatch", src.Detail)
	}
	if src.ObservedSHA256 == src.RecordedSHA256 {
		t.Fatalf("ObservedSHA256 (%q) unexpectedly equals RecordedSHA256 -- fixture is supposed to mismatch", src.ObservedSHA256)
	}
}
