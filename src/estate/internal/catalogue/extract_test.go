package catalogue

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// minimalValidPDF is a hand-built, structurally complete one-page PDF
// (proper xref table and trailer) containing the literal text "fixture
// pdf text" -- unlike writeFixturePDF (catalogue_test.go), which only
// needs to satisfy a byte-level /Type/Page regex, pdftotext needs a real
// xref table and trailer to parse the file at all.
func minimalValidPDF() []byte {
	var objs []string
	objs = append(objs, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	objs = append(objs, "2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	objs = append(objs, "3 0 obj\n<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 4 0 R >> >> /MediaBox [0 0 200 200] /Contents 5 0 R >>\nendobj\n")
	objs = append(objs, "4 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")
	content := "BT /F1 12 Tf 20 100 Td (fixture pdf text) Tj ET"
	objs = append(objs, "5 0 obj\n<< /Length "+itoa(len(content))+" >>\nstream\n"+content+"\nendstream\nendobj\n")

	header := "%PDF-1.4\n"
	var buf strings.Builder
	buf.WriteString(header)
	offsets := make([]int, len(objs)+1)
	pos := len(header)
	for i, o := range objs {
		offsets[i+1] = pos
		buf.WriteString(o)
		pos += len(o)
	}
	xrefStart := pos
	buf.WriteString("xref\n0 " + itoa(len(objs)+1) + "\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		buf.WriteString(pad10(offsets[i]) + " 00000 n \n")
	}
	buf.WriteString("trailer\n<< /Size " + itoa(len(objs)+1) + " /Root 1 0 R >>\nstartxref\n" + itoa(xrefStart) + "\n%%EOF")
	return []byte(buf.String())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func pad10(n int) string {
	s := itoa(n)
	for len(s) < 10 {
		s = "0" + s
	}
	return s
}

func TestObservePDF_UsesPdftotextWhenPresent(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed on this machine")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.pdf")
	if err := os.WriteFile(path, minimalValidPDF(), 0o644); err != nil {
		t.Fatal(err)
	}

	revision, status, cachePath := observePDF(path, filepath.Join(dir, "cache"))
	if revision == "" {
		t.Fatalf("revision is empty")
	}
	if !strings.Contains(status, "extracted") {
		t.Fatalf("status = %q, want it to say extracted", status)
	}
	if cachePath == "" {
		t.Fatalf("cachePath is empty despite a claimed extraction")
	}
	got, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	if !strings.Contains(string(got), "fixture pdf text") {
		t.Fatalf("extracted text = %q, want it to contain the fixture's own text", got)
	}
}

func TestObservePDF_MissingFile(t *testing.T) {
	dir := t.TempDir()
	revision, status, cachePath := observePDF(filepath.Join(dir, "absent.pdf"), filepath.Join(dir, "cache"))
	if revision != "" {
		t.Fatalf("revision = %q, want empty for a missing file", revision)
	}
	if !strings.Contains(status, "could not extract") {
		t.Fatalf("status = %q, want it to say could not extract", status)
	}
	if cachePath != "" {
		t.Fatalf("cachePath = %q, want empty", cachePath)
	}
}

func TestObserveConversation_MissingRootIsNotApplicable(t *testing.T) {
	dir := t.TempDir()
	revision, status, cachePath := observeConversation(filepath.Join(dir, "does-not-exist"))
	if revision != "0" {
		t.Fatalf("revision = %q, want %q for an absent root (zero units)", revision, "0")
	}
	if !strings.Contains(status, "not applicable") {
		t.Fatalf("status = %q, want it to say not applicable", status)
	}
	if cachePath != "" {
		t.Fatalf("cachePath = %q, want empty -- extraction is never attempted for conversation sources", cachePath)
	}
}

func TestObserveConversation_RevisionStableAcrossRepeatedObservation(t *testing.T) {
	dir := t.TempDir()
	fixture := `{"timestamp":"2026-01-01T00:00:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture turn"}]}}
`
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	r1, _, _ := observeConversation(dir)
	r2, _, _ := observeConversation(dir)
	if r1 != r2 {
		t.Fatalf("revision changed across two observations of an unchanged root: %q vs %q -- would falsely flip drift on every refresh", r1, r2)
	}
}

func TestObserveRepoDocs_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	os.WriteFile(path, []byte("# hello\n"), 0o644)
	revision, status, cachePath := observeRepoDocs(path, filepath.Join(dir, "cache"))
	if revision == "" {
		t.Fatalf("revision is empty")
	}
	if !strings.Contains(status, "extracted") {
		t.Fatalf("status = %q, want extracted", status)
	}
	got, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("reading cache: %v", err)
	}
	if string(got) != "# hello\n" {
		t.Fatalf("cache content = %q, want file content verbatim", got)
	}
}

func TestObserveRepoDocs_DirectoryRevisionChangesOnEdit(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "docs")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "a.md"), []byte("v1"), 0o644)

	r1, status, _ := observeRepoDocs(sub, filepath.Join(dir, "cache"))
	if !strings.Contains(status, "extracted") {
		t.Fatalf("status = %q, want extracted", status)
	}

	os.WriteFile(filepath.Join(sub, "a.md"), []byte("v2 changed"), 0o644)
	r2, _, _ := observeRepoDocs(sub, filepath.Join(dir, "cache"))
	if r1 == r2 {
		t.Fatalf("directory revision unchanged after editing a file inside it")
	}
}
