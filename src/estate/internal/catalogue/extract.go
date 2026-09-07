package catalogue

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// observe computes extractionKind's current revision marker for
// locator, attempts whatever extraction that kind supports, and reports
// both plus where any extracted content was cached. It never mutates
// locator itself -- every read here is os.Stat, os.ReadFile, os.ReadDir,
// or (for pdf) a read-only pdftotext invocation that writes only to
// cacheDir, never back to locator.
//
// localPath is used only by ExtractionRepoPointer (P8) -- every other
// kind ignores it and returns checkoutState "" (not applicable to that
// kind, distinct from LocalCheckoutAbsent, which is repo-pointer-
// specific and means "applicable, but nothing found").
//
// An extractionKind not listed below still returns a value: extraction
// unavailable, stated as such, never a silent empty success --
// brief-lane-b.md deliverable 3's "visible honesty" requirement.
func observe(extractionKind ExtractionKind, locator, localPath, cacheDir string) (revision, extractionStatus, cachePath string, checkoutState LocalCheckoutState) {
	switch extractionKind {
	case ExtractionPDF:
		revision, extractionStatus, cachePath = observePDF(locator, cacheDir)
	case ExtractionConversation:
		revision, extractionStatus, cachePath = observeConversation(locator)
	case ExtractionRepoDocs:
		revision, extractionStatus, cachePath = observeRepoDocs(locator, cacheDir)
	case ExtractionRepoPointer:
		revision, extractionStatus, checkoutState = observeRepoPointer(localPath)
	default:
		extractionStatus = fmt.Sprintf("could not extract: extraction kind %q has no known extraction path -- registered honestly with health only", extractionKind)
	}
	return revision, extractionStatus, cachePath, checkoutState
}

// observePDF hashes locator's full bytes for drift detection (identical
// to BuildSeedPDFSource's own SHA-256 check in catalogue.go, applied to
// any PDF rather than just the two seeded ones) and, if pdftotext is on
// PATH, extracts its text into cacheDir. A missing pdftotext or a
// pdftotext failure is reported in extractionStatus, never silently
// treated as "nothing to extract."
func observePDF(locator, cacheDir string) (revision, extractionStatus, cachePath string) {
	data, err := os.ReadFile(locator)
	if err != nil {
		return "", fmt.Sprintf("could not extract: reading %s: %v", locator, err), ""
	}
	sum := sha256.Sum256(data)
	revision = hex.EncodeToString(sum[:])

	ptt, err := exec.LookPath("pdftotext")
	if err != nil {
		return revision, "could not extract: pdftotext not found on PATH", ""
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return revision, fmt.Sprintf("could not extract: creating cache dir: %v", err), ""
	}
	out := filepath.Join(cacheDir, "extracted.txt")
	cmd := exec.Command(ptt, locator, out)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return revision, fmt.Sprintf("could not extract: pdftotext failed: %v: %s", err, combined), ""
	}
	return revision, "extracted: pdftotext text layer written to cache", out
}

// observeConversation reports a conversation-kind source's health via
// the same read-only routines catalogue.go's Build already uses --
// BuildCodexSource for a Codex root, BuildClaudeSource for a Claude one,
// distinguished by which root actually resolves (a conversation
// locator is always a directory; the two roots' own shapes -- Codex's
// *.jsonl siblings vs. Claude's per-project subdirectories -- are
// distinguishable by BuildCodexSource/BuildClaudeSource's own read, so
// this tries the Codex reading first and falls back to Claude's only if
// it finds nothing, rather than requiring a caller to say which).
//
// Revision is the observed unit count alone, never a timestamp: refresh
// calls made moments apart against an unchanged live log must produce
// the SAME revision, or every conversation source would flip to
// StatusNeedsReview on every single refresh regardless of real drift.
// Extraction is explicitly not attempted -- a live, append-only
// transcript log is catalogued via health/unit-count only this run, per
// the brief's "other kinds may register with extraction unavailable."
func observeConversation(locator string) (revision, extractionStatus, cachePath string) {
	codex := BuildCodexSource(locator)
	if codex.Health == HealthPopulated || codex.Health == HealthEmpty {
		return strconv.Itoa(codex.UnitCount), conversationExtractionStatus(codex.Health), ""
	}
	claude := BuildClaudeSource(locator)
	return strconv.Itoa(claude.UnitCount), conversationExtractionStatus(claude.Health), ""
}

func conversationExtractionStatus(h HealthState) string {
	switch h {
	case HealthMissing:
		return "not applicable: root does not exist -- no content to extract"
	case HealthUnreadable:
		return "could not extract: root exists but could not be read"
	default:
		return "not applicable: live append-only conversation log; catalogued via health/unit-count only, no text extracted this run"
	}
}

// observeRepoDocs handles the third proven kind: a single file or a
// directory of Markdown under this repository. A file's revision is its
// own SHA-256; a directory's is a manifest hash over every regular
// file's relative path, size, and mtime -- so adding, removing, or
// editing any file inside it changes the revision, and an unchanged
// directory tree (even one merely re-listed) does not.
func observeRepoDocs(locator, cacheDir string) (revision, extractionStatus, cachePath string) {
	info, err := os.Stat(locator)
	if err != nil {
		return "", fmt.Sprintf("could not extract: stat %s: %v", locator, err), ""
	}

	if !info.IsDir() {
		data, err := os.ReadFile(locator)
		if err != nil {
			return "", fmt.Sprintf("could not extract: reading %s: %v", locator, err), ""
		}
		sum := sha256.Sum256(data)
		revision = hex.EncodeToString(sum[:])
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return revision, fmt.Sprintf("could not extract: creating cache dir: %v", err), ""
		}
		out := filepath.Join(cacheDir, "extracted.txt")
		if err := os.WriteFile(out, data, 0o600); err != nil {
			return revision, fmt.Sprintf("could not extract: writing cache: %v", err), ""
		}
		return revision, "extracted: file content copied to cache", out
	}

	var manifest []string
	err = filepath.Walk(locator, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(locator, path)
		if relErr != nil {
			return relErr
		}
		manifest = append(manifest, fmt.Sprintf("%s:%d:%d", rel, fi.Size(), fi.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		return "", fmt.Sprintf("could not extract: walking %s: %v", locator, err), ""
	}
	sort.Strings(manifest)
	h := sha256.New()
	for _, line := range manifest {
		h.Write([]byte(line))
		h.Write([]byte{'\n'})
	}
	revision = hex.EncodeToString(h.Sum(nil))

	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return revision, fmt.Sprintf("could not extract: creating cache dir: %v", err), ""
	}
	out := filepath.Join(cacheDir, "manifest.txt")
	body := ""
	for _, line := range manifest {
		body += line + "\n"
	}
	if err := os.WriteFile(out, []byte(body), 0o600); err != nil {
		return revision, fmt.Sprintf("could not extract: writing manifest cache: %v", err), ""
	}
	return revision, fmt.Sprintf("extracted: file listing (%d files) written to cache", len(manifest)), out
}

// observeRepoPointer handles P8's repo-pointer kind: it never reads or
// extracts any file content -- its only observation is the local
// checkout's current commit, when localPath resolves to a real directory
// on this machine. A blank or nonexistent localPath is
// LocalCheckoutAbsent, a recorded state, never an error and never a
// reason to fall back to guessing a path from the record's own
// RemoteURL (P8's own binding rule).
//
// Revision is `git rev-parse HEAD`'s own output -- a single read-only
// git invocation, the same shell-out-to-a-real-CLI convention
// internal/corpus already uses for sqlite3. A checkout present but not a
// git repository, or a rev-parse failure, is reported in
// extractionStatus as "could not extract", never silently treated as
// LocalCheckoutAbsent -- those are two different facts (no checkout at
// all vs. a checkout that could not be read).
func observeRepoPointer(localPath string) (revision, extractionStatus string, checkoutState LocalCheckoutState) {
	if localPath == "" {
		return "", "not applicable: no local_path recorded -- remote_url is on record but no local checkout to observe", LocalCheckoutAbsent
	}
	info, err := os.Stat(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Sprintf("not applicable: no local checkout found at %s -- remote_url is on record but nothing was read locally", localPath), LocalCheckoutAbsent
		}
		return "", fmt.Sprintf("could not extract: stat %s: %v", localPath, err), LocalCheckoutAbsent
	}
	if !info.IsDir() {
		return "", fmt.Sprintf("could not extract: %s exists but is not a directory", localPath), LocalCheckoutAbsent
	}

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = localPath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Sprintf("could not extract: local checkout present at %s but `git rev-parse HEAD` failed: %v", localPath, err), LocalCheckoutPresent
	}
	revision = strings.TrimSpace(string(out))
	return revision, "not applicable: repo-pointer records the checkout's current commit only, via read-only `git rev-parse HEAD`; no file content extracted", LocalCheckoutPresent
}
