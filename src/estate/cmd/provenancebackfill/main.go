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

// refuseLivePath reports whether dbPath names the live corpus.
//
// This is the third revision of this guard, and each of the first two
// shipped a defect of the same shape: comparing PATH STRINGS, then adding a
// special case each time a reviewer found a shape whose string didn't match
// but whose target did (agent-estate#1139, PR #1232's two reviews, then
// PR #1233's review). Three rounds of "handle this shape too" is what
// happens when the wrong question is asked -- "does this string look like
// the live path" can never be complete, because a filesystem has more ways
// to name one file than any string comparison enumerates (a symlink at any
// depth, a case-varied spelling, a hardlink under an unrelated name...).
//
// This revision asks a different question: does dbPath, however it is
// spelled or reached, lead to the SAME FILE as internal/corpus.Path()? Two
// mechanisms answer that, and BOTH must actively clear a path before it is
// permitted -- uncertainty from either one refuses:
//
//  1. resolveForCompare resolves dbPath as far as the filesystem will allow,
//     component by component, including every parent and grandparent
//     directory -- not just the leaf. If ANY existing component is a
//     symlink that cannot be resolved (dangling target, ELOOP, or an
//     Lstat failure that isn't plain "doesn't exist yet" -- permission
//     denied, for instance), resolution stops with an error and
//     refuseLivePath refuses outright. It never falls back to comparing
//     what's left of the string in that case.
//  2. Where the resolved candidate exists on disk, it is compared against
//     the resolved live path by os.SameFile (device+inode identity), not
//     by string equality. This is what a string comparison structurally
//     cannot do: a hardlink to the live file under an unrelated name in an
//     unrelated directory has no symlink for EvalSymlinks to follow and no
//     matching path string, but it IS the live file, and SameFile sees
//     that.
//
// The resolved, case-folded path string is kept as an ADDITIONAL refusal
// condition (it still catches the not-yet-existing-leaf case, where there is
// no inode yet for SameFile to compare) -- never as the sole basis to
// PERMIT. Permit requires resolution to have succeeded cleanly with no
// identity or string match found; any component that could not be resolved,
// or any stat that could not be performed, refuses instead of guessing.
//
// Known limitation, out of scope for this guard: TOCTOU. This function is
// called once, in run(), before *dbPath is reopened by path many times over
// (ensureAttributionTable, countAttributionRows, alreadyAttributedIDs,
// fetchPromptsForFile per basename, insertAttribution per row), each a fresh
// `sqlite3` subprocess. A symlink swapped into place after this check and
// before one of those later opens would write through undetected. Closing
// that window means re-checking identity at every reopen (or holding an
// open, unretargetable file descriptor across the whole run) -- a
// structural change to how this tool opens the database, not a fix this
// guard can make on its own. Tracked, not attempted here.
func refuseLivePath(dbPath string) (string, bool) {
	candidate, err := resolveForCompare(dbPath)
	if err != nil {
		return fmt.Sprintf("cannot resolve %s: %v -- refusing rather than guessing whether it names the live corpus", dbPath, err), true
	}

	if livePath, err := corpus.Path(); err == nil {
		if live, err := resolveForCompare(livePath); err == nil {
			if candidate.info != nil && live.info != nil && os.SameFile(candidate.info, live.info) {
				return fmt.Sprintf("is the same file as the live corpus (%s), by device+inode identity", livePath), true
			}
			if candidate.clean == live.clean {
				return fmt.Sprintf("matches the live corpus path (%s)", livePath), true
			}
		}
	}
	// Fallback in case corpus.Path() errored (e.g. HOME unset): the
	// well-known suffix, still resolved and case-folded the same way. The
	// literal "corpus/ledger.sqlite3" fragment being compared here never
	// itself exists as a standalone path, so it always resolves cleanly.
	if fallbackSuffix, err := resolveForCompare(filepath.Join("corpus", "ledger.sqlite3")); err == nil &&
		fallbackSuffix.clean != "" && strings.HasSuffix(candidate.clean, fallbackSuffix.clean) {
		return "matches the live corpus path (~/corpus/ledger.sqlite3)", true
	}
	if strings.Contains(candidate.clean, strings.ToLower("agent-dotfiles-supervisor")) {
		return "matches the retired agent-dotfiles-supervisor ledger location", true
	}
	return "", false
}

// resolvedPath is what resolveForCompare produces: a string form for the
// (necessarily incomplete) additional string-equality refusal, and, when the
// resolved path actually exists, the os.Stat result refuseLivePath uses for
// identity comparison via os.SameFile. info is nil exactly when nothing
// exists at the resolved path yet (e.g. a -db leaf that hasn't been created)
// -- callers must not treat a nil info as a mismatch, only as "no identity
// signal available here."
type resolvedPath struct {
	clean string
	info  os.FileInfo
}

// resolveForCompare resolves p as far as the filesystem allows and reports
// both a comparable string form and, if the resolved path exists, its
// os.Stat identity. It expands a leading "~" against $HOME (flag.String
// never does), makes the result absolute, and case-folds the string form
// since this tool runs on case-insensitive filesystems (APFS default) where
// two differently-spelled strings can name the same file.
//
// Resolution walks the cleaned path one component at a time, from the root
// down, through resolveComponentsStrict -- see that function's doc comment
// for why every component (not just the leaf) must be checked, and why an
// unresolvable component anywhere in the chain returns an error rather than
// a partial answer.
func resolveForCompare(p string) (resolvedPath, error) {
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

	resolved, err := resolveComponentsStrict(clean)
	if err != nil {
		return resolvedPath{}, err
	}

	var info os.FileInfo
	if fi, statErr := os.Stat(resolved); statErr == nil {
		info = fi
	}
	return resolvedPath{clean: strings.ToLower(resolved), info: info}, nil
}

// resolveComponentsStrict walks clean (already absolute) one path component
// at a time, resolving every symlink it finds along the way -- a parent
// directory's symlink, a grandparent's, or the leaf's, all treated
// identically, unlike the single leaf-only os.Lstat this guard used to run.
//
// Three outcomes, and only one of them returns a usable path:
//
//   - A component does not exist (os.IsNotExist): nothing under it can
//     exist either, so nothing under it can be a symlink. The remaining
//     components are appended literally and the walk stops. This is the
//     ordinary "-db names a file that hasn't been created yet" case, and it
//     is the ONLY case where "doesn't fully resolve" is treated as safe to
//     continue with -- because there is provably nothing left to resolve.
//   - A component IS a symlink: filepath.EvalSymlinks must resolve it (to
//     however many hops that takes). Failure here -- a dangling target, an
//     ELOOP cycle -- returns an error immediately. This is what closes the
//     symlinked-parent-directory bypass PR #1233's review found: a parent
//     component being unresolvable is no longer silently skipped just
//     because the walk was checking the leaf.
//   - Any other Lstat failure on an existing-or-uncertain component
//     (permission denied, for example) also returns an error. Fail-closed
//     means an inspection that could not be performed is never treated as
//     evidence of safety.
func resolveComponentsStrict(clean string) (string, error) {
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean[len(volume):], string(filepath.Separator))
	if rest == "" {
		return clean, nil
	}
	parts := strings.Split(rest, string(filepath.Separator))

	resolved := volume + string(filepath.Separator)
	for i, part := range parts {
		if part == "" {
			continue
		}
		next := filepath.Join(resolved, part)
		info, err := os.Lstat(next)
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.Join(append([]string{resolved}, parts[i:]...)...), nil
			}
			return "", fmt.Errorf("cannot stat %s: %w", next, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(next)
			if err != nil {
				return "", fmt.Errorf("symlink %s does not resolve to an existing target: %w", next, err)
			}
			resolved = target
			continue
		}
		resolved = next
	}
	return resolved, nil
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
