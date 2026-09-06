// Command codexingest is agent-estate#1139's actual finish line: the write
// step that consumes a cmd/corpusextract manifest and inserts genuine
// operator turns into a SQLite corpus COPY's prompts table.
//
// cmd/provenancebackfill attributes EXISTING Claude rows already in the
// corpus; it ingests nothing. cmd/corpusextract produces a dry-run manifest
// and writes nothing, by its own design. Neither is the write step. This is.
//
// # Watermark-pinned end to end
//
// Every source file this command reads is named by the manifest itself
// (ingest.go's buildPlan), never rediscovered by walking
// ~/.codex/sessions again -- the manifest's own watermark is what pinned
// which files and which turns were eligible, and that decision is not
// re-made here. What IS re-checked here, per unit, is whether the named
// file's content at the manifest's own recorded position still matches what
// the manifest recorded (see ingest.go's doc comment) -- that is the "reject
// source change" requirement, not a second watermark pass.
//
// # Never the live corpus, unless explicitly authorized
//
// -db is refused outright if it resolves to the live corpus
// (internal/corpus.Path()) via internal/livepath.RefuseLivePath -- the SAME
// guard cmd/provenancebackfill uses, not a second copy of it (this task's
// own brief: "reuse the merged live-path guard ... rather than writing a
// second one"). -authorized-live-write lifts that refusal only when -db ALSO
// explicitly names the live path, exactly as cmd/provenancebackfill's own
// flag does; this command's own acceptance evidence is produced entirely
// against a `.backup`'d copy.
//
// # Sources are read-only, including touch
//
// Every source file this command opens is opened via rollout.AnalyzeFile,
// which never writes, truncates, or touches a source path. -sessions-root
// (default ~/.codex/sessions) is stat'd -- never walked -- once before and
// once after the run purely as evidence that nothing under it moved; it
// plays no role in deciding which files are read (the manifest does that).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/livepath"
	"github.com/jonhill90/agent-estate/estate/internal/rollout"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("codexingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to a SQLite corpus COPY (never the live corpus)")
	manifestPath := fs.String("manifest", "", "path to a cmd/corpusextract JSON manifest (its own -out file)")
	apply := fs.Bool("apply", false, "write prompts/codex_provenance rows for the plan; default is a zero-write dry run")
	sessionsRoot := fs.String("sessions-root", defaultSessionsRoot(), "root of Codex rollout JSONL files, stat'd (never walked or opened) before and after as read-only evidence")
	authorizedLiveWrite := fs.Bool("authorized-live-write", false,
		"explicit human authorization to run -apply against the live corpus. Default false, never inferable from "+
			"any other flag or environment variable. Only takes effect when -db ALSO explicitly names the live "+
			"path -- it never causes a default or inferred path to be treated as live-authorized.")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *dbPath == "" {
		fmt.Fprintln(stderr, "codexingest: -db is required")
		return 2
	}
	if *manifestPath == "" {
		fmt.Fprintln(stderr, "codexingest: -manifest is required (the -out file from a cmd/corpusextract run)")
		return 2
	}

	liveReason, live := livepath.RefuseLivePath(*dbPath)
	if live && !*authorizedLiveWrite {
		mode := "dry-run"
		if *apply {
			mode = "-apply"
		}
		fmt.Fprintf(stderr, "codexingest: refusing %s against %s: %s\n", mode, *dbPath, liveReason)
		return 1
	}

	m, err := loadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "codexingest: %v\n", err)
		return 1
	}
	watermark, err := time.Parse(time.RFC3339Nano, m.Watermark)
	if err != nil {
		fmt.Fprintf(stderr, "codexingest: manifest watermark %q is not RFC3339Nano: %v\n", m.Watermark, err)
		return 1
	}

	if live && *authorizedLiveWrite && *apply {
		// -authorized-live-write permits this ONLY because -db also
		// explicitly names the live path (RefuseLivePath just confirmed
		// that identity) -- never a default or inferred path. Mirrors
		// cmd/provenancebackfill's own banner: state the target and the
		// manifest's own entry count BEFORE a single row is written.
		fmt.Fprintln(stderr, "================================================================================")
		fmt.Fprintln(stderr, "AUTHORIZED LIVE-CORPUS WRITE -- -authorized-live-write was passed explicitly")
		fmt.Fprintf(stderr, "  path:              %s\n", *dbPath)
		fmt.Fprintf(stderr, "  reason:            %s\n", liveReason)
		fmt.Fprintf(stderr, "  manifest entries:  %d\n", len(m.Entries))
		fmt.Fprintf(stderr, "  manifest watermark: %s\n", watermark.Format(time.RFC3339Nano))
		fmt.Fprintln(stderr, "================================================================================")
	}

	beforeMtime, beforeErr := latestSourceMtime(*sessionsRoot)

	report, err := buildAndApply(*dbPath, m, watermark, *apply)
	if err != nil {
		fmt.Fprintf(stderr, "codexingest: %v\n", err)
		return 1
	}

	afterMtime, afterErr := latestSourceMtime(*sessionsRoot)

	printReport(stdout, report, *apply, *sessionsRoot, beforeMtime, beforeErr, afterMtime, afterErr)
	return 0
}

func defaultSessionsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

// latestSourceMtime is a read-only stat sweep (os.Stat only, via
// rollout.WalkRolloutFiles + os.Stat, exactly like cmd/corpusextract's own
// listRolloutFilesWithMTimes) used ONLY as before/after evidence that this
// command touched no source file's mtime -- it plays no role in deciding
// what gets ingested, which is entirely the manifest's job.
func latestSourceMtime(root string) (time.Time, error) {
	if root == "" {
		return time.Time{}, fmt.Errorf("no sessions root to stat")
	}
	paths, err := rollout.WalkRolloutFiles(root)
	if err != nil {
		return time.Time{}, err
	}
	var max time.Time
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return time.Time{}, err
		}
		if info.ModTime().After(max) {
			max = info.ModTime()
		}
	}
	return max, nil
}

// Report is this run's whole evidence -- counts that must add up exactly,
// never a bare "N inserted" with skipped units left unaccounted for.
type Report struct {
	Watermark             time.Time
	TotalEntries          int
	Inserted              int
	AlreadyIngested       int
	SourceChanged         []UnitPlan
	FilesUnreadable       []UnitPlan
	CodexProvenanceBefore int
	CodexProvenanceAfter  int
	Applied               bool
}

// buildAndApply runs buildPlan, then decides each Pending plan's final
// outcome against -db: already present (by identity id) is skipped with
// zero writes attempted, and everything else is inserted only when apply is
// true. A dry run (apply=false) performs the identical decision-making with
// zero calls to insertUnit or ensureCodexProvenanceTable -- the "would
// insert N" count in a dry-run report is exactly what -apply would insert
// next, not an estimate.
func buildAndApply(dbPath string, m manifest, watermark time.Time, apply bool) (Report, error) {
	r := Report{Watermark: watermark, TotalEntries: len(m.Entries), Applied: apply}

	plans := buildPlan(m.Entries)

	// A dry run must never write to dbPath, live or not -- ensureCodexProvenanceTable's
	// CREATE TABLE only runs under apply. Before that table exists, "before"
	// is legitimately 0: a plain count against a table that isn't there yet
	// is an error, not a zero, so this checks existence first rather than
	// letting countCodexProvenanceRows's own SELECT fail (mirrors
	// cmd/provenancebackfill's attributionTableExists/countAttributionRows
	// split for the identical reason).
	if apply {
		if err := ensureCodexProvenanceTable(dbPath); err != nil {
			return r, fmt.Errorf("ensuring codex_provenance table: %w", err)
		}
	}
	exists, err := codexProvenanceTableExists(dbPath)
	if err != nil {
		return r, fmt.Errorf("checking codex_provenance table: %w", err)
	}
	before := 0
	if exists {
		before, err = countCodexProvenanceRows(dbPath)
		if err != nil {
			return r, fmt.Errorf("counting existing codex_provenance rows: %w", err)
		}
	}
	r.CodexProvenanceBefore = before

	existing, err := existingCodexProvenanceIDs(dbPath)
	if err != nil {
		return r, fmt.Errorf("reading existing codex_provenance ids: %w", err)
	}

	for i := range plans {
		p := &plans[i]
		switch p.Outcome {
		case outcomeSourceChanged:
			r.SourceChanged = append(r.SourceChanged, *p)
			continue
		case outcomeFileUnreadable:
			r.FilesUnreadable = append(r.FilesUnreadable, *p)
			continue
		}

		id := p.Provenance.ID()
		if existing[id] {
			p.Outcome = outcomeAlreadyIngested
			r.AlreadyIngested++
			continue
		}

		if apply {
			at := atFor(*p, watermark)
			if err := insertUnit(dbPath, id, p.Text, at,
				p.Provenance.SourceName, p.Provenance.Harness, p.Provenance.SourceFile,
				p.Provenance.SessionID, p.Provenance.RecordIndex, p.Provenance.ContentHash,
				watermark.Format(time.RFC3339Nano)); err != nil {
				return r, fmt.Errorf("inserting unit %s: %w", id, err)
			}
			existing[id] = true
		}
		p.Outcome = outcomeInserted
		r.Inserted++
	}

	after := before
	if apply {
		after, err = countCodexProvenanceRows(dbPath)
		if err != nil {
			return r, fmt.Errorf("counting codex_provenance rows after run: %w", err)
		}
	}
	r.CodexProvenanceAfter = after
	return r, nil
}

func printReport(w *os.File, r Report, applied bool, sessionsRoot string, before time.Time, beforeErr error, after time.Time, afterErr error) {
	fmt.Fprintf(w, "watermark: %s\n", r.Watermark.Format(time.RFC3339Nano))
	fmt.Fprintf(w, "mode: %s\n", map[bool]string{true: "apply", false: "dry-run"}[applied])
	fmt.Fprintf(w, "manifest entries: %d\n", r.TotalEntries)
	fmt.Fprintf(w, "inserted: %d\n", r.Inserted)
	fmt.Fprintf(w, "already ingested (idempotent skip): %d\n", r.AlreadyIngested)
	fmt.Fprintf(w, "refused, source changed since manifest: %d\n", len(r.SourceChanged))
	for _, p := range r.SourceChanged {
		fmt.Fprintf(w, "  %s (line %d, %s): %s\n", p.Entry.File, p.Entry.RecordIndex, p.Entry.Source, p.Reason)
	}
	fmt.Fprintf(w, "refused, source file unreadable: %d\n", len(r.FilesUnreadable))
	for _, p := range r.FilesUnreadable {
		fmt.Fprintf(w, "  %s: %s\n", p.Entry.File, p.Reason)
	}

	accounted := r.Inserted + r.AlreadyIngested + len(r.SourceChanged) + len(r.FilesUnreadable)
	if accounted != r.TotalEntries {
		fmt.Fprintf(w, "MISMATCH: %d manifest entries accounted for, want exactly %d -- do not trust this report\n", accounted, r.TotalEntries)
	} else {
		fmt.Fprintf(w, "accounting: %d entries in, %d entries decided (inserted + already_ingested + source_changed + unreadable) -- exact\n", r.TotalEntries, accounted)
	}

	fmt.Fprintf(w, "codex_provenance rows before: %d\n", r.CodexProvenanceBefore)
	fmt.Fprintf(w, "codex_provenance rows after: %d\n", r.CodexProvenanceAfter)
	if applied {
		if r.CodexProvenanceAfter-r.CodexProvenanceBefore != r.Inserted {
			fmt.Fprintf(w, "MISMATCH: table grew by %d but this run inserted %d -- do not trust this apply\n",
				r.CodexProvenanceAfter-r.CodexProvenanceBefore, r.Inserted)
		} else {
			fmt.Fprintf(w, "table growth matches this run's inserted count exactly (%d)\n", r.Inserted)
		}
	}

	fmt.Fprintf(w, "sessions root (read-only evidence, never walked to decide ingestion): %s\n", sessionsRoot)
	fmt.Fprintf(w, "  latest source mtime before run: %s\n", mtimeString(before, beforeErr))
	fmt.Fprintf(w, "  latest source mtime after run:  %s\n", mtimeString(after, afterErr))
	if beforeErr == nil && afterErr == nil {
		if before.Equal(after) {
			fmt.Fprintln(w, "  unchanged -- no source file was touched by this run")
		} else {
			fmt.Fprintln(w, "  CHANGED -- a source file's mtime moved during this run; investigate before trusting this report")
		}
	}
}

func mtimeString(t time.Time, err error) string {
	if err != nil {
		return fmt.Sprintf("could not stat: %v", err)
	}
	if t.IsZero() {
		return "(no rollout files found)"
	}
	return t.UTC().Format(time.RFC3339Nano)
}
