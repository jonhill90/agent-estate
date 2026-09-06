// Command provenancebackfill attributes EXISTING Claude rows in a copy of
// the corpus's prompts table with internal/provenance's UnitProvenance
// identity (harness, source file, session id, record index, content hash).
// See extract.go and attribute.go for the read and write halves; this file
// is flag handling, the watermark contract, and the report.
//
// # REVISED ACCEPTANCE: one fixed watermark, no live-drift tolerance
//
//  1. -record-watermark prints ONE timestamp and exits, touching nothing
//     else. That printed value is what every later -dry-run/-apply call on
//     this same backfill attempt must be given via -watermark.
//  2. -dry-run and -apply both take that SAME -watermark value and use it
//     to decide file eligibility ONCE, at the start of the run -- neither
//     mode re-stats a file after classifying it.
//  3. A file whose mtime is after -watermark is excluded and every prompts
//     row that names it is skipped with outcomeExcludedWmark, listed by
//     path in the report.
//  4. -apply's claude_provenance row count must equal the run's own
//     attributed count exactly (checked and reported, not merely trusted).
//  5. Re-running -apply at the SAME -watermark against the SAME db inserts
//     zero new rows -- every candidate is already present under its
//     identity id and is reported outcomeAlready.
//
// # Never the live corpus, unless explicitly authorized
//
// refuseLivePath below is an in-process backstop on top of the
// ledger-write-guard hook: BOTH -dry-run and -apply refuse outright if -db
// resolves to the live corpus path or the retired agent-dotfiles-supervisor
// location. -dry-run is gated identically to -apply here, not because
// buildReport's own writes are conditioned on -apply (they are -- see
// attribute.go's ensureAttributionTable/attributionTableExists split) but
// because this guard's job is refusing an unauthorized live path, full stop
// -- it must not depend on knowing which call downstream would have written.
// This tool's own acceptance evidence is produced entirely against a `cp`'d
// copy (see the task brief); backfilling the live corpus is a separate,
// later, explicitly authorized step -- gated by -authorized-live-write, see
// run() and its doc comment there, so this refusal is not permanently
// absolute, only absolute by default.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/livepath"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("provenancebackfill", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to a SQLite corpus COPY (never the live corpus)")
	watermarkStr := fs.String("watermark", "", "RFC3339 timestamp recorded by -record-watermark; frozen for the whole run")
	dryRun := fs.Bool("dry-run", false, "compute and print the attribution plan; write nothing")
	apply := fs.Bool("apply", false, "write claude_provenance rows for the plan computed under -watermark")
	recordWatermark := fs.Bool("record-watermark", false, "print the current time as RFC3339 and exit; touches nothing else")
	claudeRoot := fs.String("claude-root", "", "root of Claude transcript JSONL files (default ~/.claude/projects)")
	authorizedLiveWrite := fs.Bool("authorized-live-write", false,
		"explicit human authorization to run -apply against the live corpus. Default false, never inferable from "+
			"any other flag or environment variable. Only takes effect when -db ALSO explicitly names the live "+
			"path -- it never causes a default or inferred path to be treated as live-authorized.")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *recordWatermark {
		fmt.Fprintln(stdout, time.Now().UTC().Format(time.RFC3339))
		return 0
	}

	if *dbPath == "" {
		fmt.Fprintln(stderr, "provenancebackfill: -db is required")
		return 2
	}
	if *watermarkStr == "" {
		fmt.Fprintln(stderr, "provenancebackfill: -watermark is required (run -record-watermark once, then pass its exact output here)")
		return 2
	}
	watermark, err := time.Parse(time.RFC3339, *watermarkStr)
	if err != nil {
		fmt.Fprintf(stderr, "provenancebackfill: -watermark %q is not RFC3339: %v\n", *watermarkStr, err)
		return 2
	}
	if !*dryRun && !*apply {
		fmt.Fprintln(stderr, "provenancebackfill: pass -dry-run or -apply")
		return 2
	}
	root := *claudeRoot
	if root == "" {
		root, err = defaultClaudeRoot()
		if err != nil {
			fmt.Fprintf(stderr, "provenancebackfill: %v\n", err)
			return 1
		}
	}

	// This guard runs for -dry-run exactly as it does for -apply
	// (agent-estate#1237 review): buildReport's own writes are now gated on
	// apply (see attribute.go/attributionTableExists), but the guard itself
	// must not assume that -- an ungated write anywhere downstream of an
	// unauthorized live path is exactly the defect this closes, and gating
	// only "the write call we know about today" is how that defect happens
	// again tomorrow. Mode is checked only to pick the word in the message.
	if reason, live := refuseLivePath(*dbPath); live {
		if !*authorizedLiveWrite {
			mode := "-dry-run"
			if *apply {
				mode = "-apply"
			}
			fmt.Fprintf(stderr, "provenancebackfill: refusing %s against %s: %s\n", mode, *dbPath, reason)
			return 1
		}
		// -authorized-live-write permits this ONLY because -db also
		// explicitly names the live path (refuseLivePath just confirmed that
		// identity) -- never a default or inferred path. The banner names the
		// RESOLVED candidate refuseLivePath actually compared (symlinks
		// followed, case-folded), not filepath.Abs(*dbPath) -- printing the
		// unresolved spelling while writing through a resolved one would
		// announce the wrong target (agent-estate#1237 review).
		resolved, resolveErr := resolveForCompare(*dbPath)
		display := *dbPath
		if resolveErr == nil {
			display = resolved.clean
		}
		if *apply {
			// Compute the plan read-only first so the banner states the exact
			// row count before a single row is written -- this call is now
			// genuinely zero-write regardless of path (apply=false), not
			// merely zero-write because it happened to target a copy.
			planned, err := buildReport(*dbPath, root, watermark, false)
			if err != nil {
				fmt.Fprintf(stderr, "provenancebackfill: computing authorized live-write plan: %v\n", err)
				return 1
			}
			fmt.Fprintln(stderr, "================================================================================")
			fmt.Fprintln(stderr, "AUTHORIZED LIVE-CORPUS WRITE -- -authorized-live-write was passed explicitly")
			fmt.Fprintf(stderr, "  path:      %s\n", display)
			fmt.Fprintf(stderr, "  reason:    %s\n", reason)
			fmt.Fprintf(stderr, "  rows to write: %d\n", planned.AttributedCount)
			fmt.Fprintf(stderr, "  watermark: %s\n", watermark.Format(time.RFC3339))
			fmt.Fprintln(stderr, "================================================================================")
		}
	}

	report, err := buildReport(*dbPath, root, watermark, *apply)
	if err != nil {
		fmt.Fprintf(stderr, "provenancebackfill: %v\n", err)
		return 1
	}
	printReport(stdout, report, *apply)
	return 0
}

// refuseLivePath, resolvedPath and resolveForCompare are thin wrappers over
// internal/livepath -- the guard itself (agent-estate#1139) now lives there,
// shared verbatim with cmd/codexingest, rather than forked a second time.
// See internal/livepath's own doc comment for the guard's full rationale and
// revision history. resolvedPath keeps this package's original (unexported)
// field names so this file's other call sites and this package's own tests
// need no further changes.
func refuseLivePath(dbPath string) (string, bool) {
	return livepath.RefuseLivePath(dbPath)
}

type resolvedPath struct {
	clean string
	info  os.FileInfo
}

func resolveForCompare(p string) (resolvedPath, error) {
	rp, err := livepath.ResolveForCompare(p)
	if err != nil {
		return resolvedPath{}, err
	}
	return resolvedPath{clean: rp.Clean, info: rp.Info}, nil
}

// Report is the whole run's evidence: never a bare count, always the
// decisions and reasons behind it.
type Report struct {
	Watermark        time.Time
	ClaudeRoot       string
	FilesEligible    int
	FilesExcluded    []string
	FilesCollisions  []string
	FilesStatFailed  []ParseIssue
	FilesUnparseable []ParseIssue
	RowsExamined     int
	Decisions        []Decision
	AttributedCount  int
	TableCountBefore int
	TableCountAfter  int
	Applied          bool
}

func buildReport(dbPath, root string, watermark time.Time, apply bool) (Report, error) {
	r := Report{Watermark: watermark, ClaudeRoot: root, Applied: apply}

	byBase, collisions, err := FindClaudeFiles(root)
	if err != nil {
		return r, fmt.Errorf("walking claude root %s: %w", root, err)
	}
	r.FilesCollisions = collisions

	plan := PlanWatermark(byBase, collisions, watermark)
	r.FilesExcluded = plan.Excluded
	r.FilesStatFailed = plan.StatFailures
	r.FilesEligible = len(plan.Eligible)

	// A dry run must never write to dbPath, live corpus or not (agent-estate#1237
	// review) -- ensureAttributionTable's CREATE TABLE only runs under -apply.
	// A dry run instead asks attributionTableExists, a plain SELECT, and treats
	// "table not there yet" as before=0/already-empty: the same numbers a
	// genuinely-untouched table would report, without ever creating one.
	//
	// "A plain SELECT" was not, on its own, enough to make that true: the
	// bare sqlite3 CLI opens read-write by default and creates its target
	// even for a SELECT, so against a -db path that had never been `cp`'d
	// into place, this SELECT alone still left an empty, no-schema file
	// behind (agent-estate#1237, PR #1237's second review -- the fourth
	// overclaiming comment in this family before this one). Every read-only
	// call site reachable from this function -- attributionTableExists,
	// countAttributionRows, alreadyAttributedIDs, fetchPromptsForFile -- now
	// opens via runSQLiteReadOnly (`sqlite3 -readonly`), which fails to open
	// rather than creating a file when nothing is there yet; see attribute.go's
	// dbFileMissing for how each caller tells that expected case apart from a
	// real error.
	if apply {
		if err := ensureAttributionTable(dbPath); err != nil {
			return r, fmt.Errorf("ensuring claude_provenance table: %w", err)
		}
	}
	exists, err := attributionTableExists(dbPath)
	if err != nil {
		return r, fmt.Errorf("checking claude_provenance table: %w", err)
	}

	before := 0
	already := map[string]bool{}
	if exists {
		before, err = countAttributionRows(dbPath)
		if err != nil {
			return r, fmt.Errorf("counting existing claude_provenance rows: %w", err)
		}
		already, err = alreadyAttributedIDs(dbPath)
		if err != nil {
			return r, fmt.Errorf("reading existing claude_provenance ids: %w", err)
		}
	}
	r.TableCountBefore = before

	// Excluded-by-collision basenames: report their prompts rows too, so
	// "rows examined" covers every candidate this run could see, not just
	// the ones it happened to be able to read.
	excludedBasenames := map[string]bool{}
	for _, base := range collisions {
		excludedBasenames[base] = true
	}
	excludedByWatermark := map[string]bool{}
	for _, path := range plan.Excluded {
		excludedByWatermark[filepath.Base(path)] = true
	}

	// Every distinct basename this run needs a decision for: eligible files,
	// watermark-excluded files, and collision basenames. A basename never
	// walked at all (no file under root matches it) is not a Claude
	// candidate by this tool's identification rule and is out of scope --
	// see extract.go's doc comment.
	allBases := map[string]bool{}
	for b := range plan.Eligible {
		allBases[b] = true
	}
	for b := range excludedByWatermark {
		allBases[b] = true
	}
	for b := range excludedBasenames {
		allBases[b] = true
	}
	bases := make([]string, 0, len(allBases))
	for b := range allBases {
		bases = append(bases, b)
	}
	sort.Strings(bases)

	for _, base := range bases {
		rows, err := fetchPromptsForFile(dbPath, base)
		if err != nil {
			return r, fmt.Errorf("fetching prompts for %s: %w", base, err)
		}
		if len(rows) == 0 {
			continue
		}
		r.RowsExamined += len(rows)

		if excludedBasenames[base] {
			for _, row := range rows {
				r.Decisions = append(r.Decisions, Decision{PromptID: row.ID, Basename: base,
					Outcome: outcomeCollision, Reason: "basename resolves to more than one real file under the claude root"})
			}
			continue
		}
		if excludedByWatermark[base] {
			for _, row := range rows {
				r.Decisions = append(r.Decisions, Decision{PromptID: row.ID, Basename: base,
					Outcome: outcomeExcludedWmark, Reason: "source file's mtime is after the recorded watermark"})
			}
			continue
		}

		path := plan.Eligible[base]
		units, malformed, err := ExtractFile(path)
		if err != nil {
			r.FilesUnparseable = append(r.FilesUnparseable, ParseIssue{Path: path, Reason: err.Error()})
			for _, row := range rows {
				r.Decisions = append(r.Decisions, Decision{PromptID: row.ID, Basename: base,
					Outcome: outcomeUnparseableFile, Reason: err.Error()})
			}
			continue
		}
		_ = malformed // surfaced via FilesUnparseable only on a hard read error; malformed lines are just excluded turns.

		decisions, _ := pairFileRows(rows, units)
		for i := range decisions {
			decisions[i].Basename = base
			d := &decisions[i]
			if d.Outcome != outcomeAttributed {
				continue
			}
			id := d.Unit.Provenance.ID()
			if already[id] {
				d.Outcome = outcomeAlready
				d.Reason = "identity id already present in claude_provenance from a prior run"
				continue
			}
			if apply {
				if err := insertAttribution(dbPath, *d.Unit, d.PromptID, watermark.Format(time.RFC3339)); err != nil {
					return r, fmt.Errorf("inserting attribution for prompt %s: %w", d.PromptID, err)
				}
				already[id] = true
			}
			r.AttributedCount++
		}
		r.Decisions = append(r.Decisions, decisions...)
	}

	// apply implies ensureAttributionTable ran above, so the table exists to
	// recount. A dry run never wrote anything, so its "after" is exactly its
	// "before" -- recounting would either hit the same never-created table
	// (a pointless SELECT) or, if it already existed, correctly report no
	// change, which "after = before" already states without another query.
	after := before
	if apply {
		after, err = countAttributionRows(dbPath)
		if err != nil {
			return r, fmt.Errorf("counting claude_provenance rows after run: %w", err)
		}
	}
	r.TableCountAfter = after
	return r, nil
}

func printReport(w *os.File, r Report, applied bool) {
	fmt.Fprintf(w, "watermark: %s\n", r.Watermark.Format(time.RFC3339))
	fmt.Fprintf(w, "claude root: %s\n", r.ClaudeRoot)
	fmt.Fprintf(w, "mode: %s\n", map[bool]string{true: "apply", false: "dry-run"}[applied])
	fmt.Fprintf(w, "files eligible (mtime <= watermark): %d\n", r.FilesEligible)
	fmt.Fprintf(w, "files excluded (changed after watermark): %d\n", len(r.FilesExcluded))
	for _, p := range r.FilesExcluded {
		fmt.Fprintf(w, "  excluded: %s\n", p)
	}
	if len(r.FilesCollisions) > 0 {
		fmt.Fprintf(w, "basenames excluded as ambiguous collisions: %v\n", r.FilesCollisions)
	}
	if len(r.FilesStatFailed) > 0 {
		fmt.Fprintln(w, "files that could not be stat'd:")
		for _, f := range r.FilesStatFailed {
			fmt.Fprintf(w, "  %s: %s\n", f.Path, f.Reason)
		}
	}
	if len(r.FilesUnparseable) > 0 {
		fmt.Fprintln(w, "files that could not be parsed:")
		for _, f := range r.FilesUnparseable {
			fmt.Fprintf(w, "  %s: %s\n", f.Path, f.Reason)
		}
	}

	fmt.Fprintf(w, "rows examined: %d\n", r.RowsExamined)
	fmt.Fprintf(w, "rows attributed: %d\n", r.AttributedCount)

	counts := map[string]int{}
	for _, d := range r.Decisions {
		if d.Outcome == outcomeAttributed {
			continue
		}
		counts[d.Outcome]++
	}
	skippedTotal := 0
	for _, n := range counts {
		skippedTotal += n
	}
	fmt.Fprintf(w, "rows skipped: %d\n", skippedTotal)
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Fprintf(w, "  %s: %d\n", reason, counts[reason])
	}
	if skippedTotal == 0 && r.RowsExamined > 0 {
		fmt.Fprintln(w, "  (zero skipped is a real count here: every examined row paired to a unit or was already attributed)")
	}

	fmt.Fprintf(w, "claude_provenance rows before: %d\n", r.TableCountBefore)
	fmt.Fprintf(w, "claude_provenance rows after: %d\n", r.TableCountAfter)
	if applied {
		if r.TableCountAfter-r.TableCountBefore != r.AttributedCount {
			fmt.Fprintf(w, "MISMATCH: table grew by %d but this run attributed %d -- do not trust this apply\n",
				r.TableCountAfter-r.TableCountBefore, r.AttributedCount)
		} else {
			fmt.Fprintf(w, "table growth matches this run's attributed count exactly (%d)\n", r.AttributedCount)
		}
	}
}
