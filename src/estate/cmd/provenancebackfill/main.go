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
// # Never the live corpus
//
// refuseLivePath below is an in-process backstop on top of the
// ledger-write-guard hook: -apply refuses outright if -db resolves to the
// live corpus path or the retired agent-dotfiles-supervisor location. This
// tool's own acceptance evidence is produced entirely against a `cp`'d copy
// (see the task brief); backfilling the live corpus is a separate, later,
// explicitly authorized step this tool does not take.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/corpus"
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
	if *apply {
		if reason, live := refuseLivePath(*dbPath); live {
			fmt.Fprintf(stderr, "provenancebackfill: refusing -apply against %s: %s\n", *dbPath, reason)
			return 1
		}
	}

	root := *claudeRoot
	if root == "" {
		root, err = defaultClaudeRoot()
		if err != nil {
			fmt.Fprintf(stderr, "provenancebackfill: %v\n", err)
			return 1
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

// refuseLivePath reports whether dbPath names the live corpus. A prior
// version of this check compared literal, case-sensitive path strings and
// never resolved symlinks -- a symlink to ~/corpus/ledger.sqlite3, or a
// case-varied spelling on this filesystem's case-insensitive default,
// silently passed it straight into a real write (agent-estate#1139 PR #1232
// review). resolveForCompare below normalizes both the candidate and the
// known live locations the same way before comparing, so those bypasses no
// longer differ from the exact-spelling case this was always meant to catch.
//
// The live corpus's own canonical path comes from internal/corpus.Path()
// rather than being reimplemented here as a second literal string, per that
// review's second recommendation -- one source of truth for "what is the
// live corpus."
//
// A second review (same PR, second REQUEST CHANGES) found that a DANGLING
// symlink at the guarded name bypassed this entirely: resolveForCompare used
// to fall back to the unresolved path whenever filepath.EvalSymlinks failed,
// and EvalSymlinks fails with ENOENT on a symlink whose target does not
// exist -- exactly the case a real bypass needs, since the fallback then
// compares the *link's own name* rather than what it points at. That
// fallback is correct for a path that plainly doesn't exist and never was a
// symlink (nothing else to compare there); it is wrong for a symlink whose
// target can't be resolved, because the one signal that path carries -- the
// resolvable-or-not state of its target -- is exactly what gets thrown away.
// resolveForCompare now returns an error in that specific case, and
// refuseLivePath treats "cannot resolve what this symlink names" as a
// refusal in its own right, never as evidence of safety.
func refuseLivePath(dbPath string) (string, bool) {
	candidate, err := resolveForCompare(dbPath)
	if err != nil {
		return fmt.Sprintf("cannot resolve %s: %v -- refusing rather than guessing whether it names the live corpus", dbPath, err), true
	}

	if livePath, err := corpus.Path(); err == nil {
		if liveResolved, err := resolveForCompare(livePath); err == nil && candidate == liveResolved {
			return fmt.Sprintf("matches the live corpus path (%s)", livePath), true
		}
	}
	// Fallback in case corpus.Path() errored (e.g. HOME unset): the
	// well-known suffix, still resolved and case-folded the same way. The
	// literal "corpus/ledger.sqlite3" fragment being compared here never
	// itself exists as a standalone path, so it always resolves cleanly.
	fallbackSuffix, _ := resolveForCompare(filepath.Join("corpus", "ledger.sqlite3"))
	if fallbackSuffix != "" && strings.HasSuffix(candidate, fallbackSuffix) {
		return "matches the live corpus path (~/corpus/ledger.sqlite3)", true
	}
	if strings.Contains(candidate, strings.ToLower("agent-dotfiles-supervisor")) {
		return "matches the retired agent-dotfiles-supervisor ledger location", true
	}
	return "", false
}

// resolveForCompare turns a path into the form refuseLivePath compares:
// absolute, cleaned, symlinks resolved when the target exists, tilde-expanded
// against $HOME, and case-folded since this tool runs on case-insensitive
// filesystems (APFS default) where two differently-spelled strings can name
// the same file.
//
// Two distinct "doesn't fully resolve" cases must NOT be treated the same:
//
//   - The cleaned path is not a symlink at all (os.Lstat finds nothing, or
//     finds a plain file/dir) -- there is nothing further to resolve, so
//     comparing the cleaned-but-unresolved form is the correct and only
//     answer. This covers a -db path that hasn't been created yet.
//   - The cleaned path (or something in its chain) IS a symlink, and
//     filepath.EvalSymlinks fails to resolve it (e.g. ENOENT on a dangling
//     target). Here the unresolved path is the *link's own name*, not what
//     it points at -- silently comparing that throws away the one thing the
//     symlink actually carries, and a dangling link placed at the guarded
//     name would then compare as "not live" while writing straight through
//     it. This case returns an error; refuseLivePath treats that as a
//     refusal, never as evidence the path is safe.
func resolveForCompare(p string) (string, error) {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			p = home
		}
	} else if strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}

	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	clean := filepath.Clean(abs)

	info, lstatErr := os.Lstat(clean)
	if lstatErr != nil {
		// Nothing exists at this name at all -- not a symlink, dangling or
		// otherwise, just absent. Safe to compare the cleaned literal form.
		return strings.ToLower(clean), nil
	}

	if info.Mode()&os.ModeSymlink == 0 {
		// Exists and is not itself a symlink. EvalSymlinks may still resolve
		// a symlinked parent directory in the chain; if it fails here (rare
		// -- e.g. a parent removed between the Lstat and this call) fall
		// back to the cleaned form, since the leaf itself carries no
		// unresolved symlink signal to discard.
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			clean = resolved
		}
		return strings.ToLower(clean), nil
	}

	// The leaf itself is a symlink (or the first hop of a chain). Its target
	// must resolve, however many hops that takes -- a dangling link anywhere
	// in the chain is exactly the bypass this guards against.
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("symlink %s does not resolve to an existing target: %w", clean, err)
	}
	return strings.ToLower(resolved), nil
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

	if err := ensureAttributionTable(dbPath); err != nil {
		return r, fmt.Errorf("ensuring claude_provenance table: %w", err)
	}
	before, err := countAttributionRows(dbPath)
	if err != nil {
		return r, fmt.Errorf("counting existing claude_provenance rows: %w", err)
	}
	r.TableCountBefore = before

	already, err := alreadyAttributedIDs(dbPath)
	if err != nil {
		return r, fmt.Errorf("reading existing claude_provenance ids: %w", err)
	}

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

	after, err := countAttributionRows(dbPath)
	if err != nil {
		return r, fmt.Errorf("counting claude_provenance rows after run: %w", err)
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
