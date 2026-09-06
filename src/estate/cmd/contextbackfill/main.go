// Command contextbackfill is agent-estate#1139's second slice: it derives
// prior-assistant context for prompts rows cmd/codexingest already wrote
// with context left as "" (4,360 of them, measured 2026-09-06). It reads
// nothing new from ~/.codex/sessions that codexingest did not already name --
// every source file this command opens is the SAME source_file
// codex_provenance already recorded for that row, addressed by the SAME
// three fields (source_file, session_id, record_index) codexingest's own
// insertUnit wrote. This command never re-scans or re-orders the corpus to
// guess at adjacency; it re-parses one already-named file per candidate row.
//
// # The one rule that must not bend
//
// Assistant text is evidence, never instruction. What this command writes to
// prompts.context and the new prompt_context table always self-describes its
// own authorship (role: "assistant" or "none") and its own derivation state,
// so nothing downstream can mistake it for an operator directive by
// inferring authorship from position. It is never written to items, and
// internal/corpus's Hard()/Grounding() never read prompts.context at all --
// see internal/corpus's own context_leak_test.go for the regression guard.
//
// # Four typed absence states, never a bare empty string
//
//   - "derived"            -- the preceding assistant turn's text was found
//     and (possibly) capped; prompts.context carries it, tagged
//     role="assistant".
//   - "no_prior_turn"       -- the source file re-parsed cleanly and the
//     candidate's own turn resolved, but no assistant turn precedes it (it
//     opens its session, or its recovered history).
//   - "source_unreadable"   -- the named source_file could not be opened
//     right now (moved, deleted, permission denied).
//   - "record_unresolved"   -- the source_file opened, but the position
//     codex_provenance recorded no longer resolves to the same unit
//     (session id or content hash mismatch, or the index is out of range)
//     -- the file changed shape since codexingest ran.
//
// Every one of these is WRITTEN (not merely reported) so a rerun is
// idempotent: prompts.context is NOT NULL and starts as "", and every write
// here replaces "" with a non-empty, state-tagged JSON envelope -- the
// selection query below (`context = ”`) is what makes a second -apply
// against an already-processed row select zero candidates, without a
// separate existing-ids lookup (though one is still kept, defensively; see
// db.go's alreadyProcessedPromptIDs).
//
// # Bounded, and the bound is stated
//
// maxContextRunes (derive.go) caps how much of a preceding assistant turn is
// kept; truncation is recorded as its own boolean, never silently dropped --
// truncated and absent are different states, per this task's own
// requirement.
//
// # Watermark-pinned, exactly as codexingest was -- but two-phase, because
// there is no manifest here
//
// codexingest's watermark came from a manifest cmd/corpusextract built
// earlier; this command has no such manifest (it re-derives context for rows
// codexingest ALREADY wrote, it does not discover new ones), so it uses
// -record-watermark / -watermark exactly as cmd/provenancebackfill does:
// -record-watermark prints one RFC3339Nano timestamp and exits, touching
// nothing; that value is then passed to every -dry-run/-apply call in the
// same attempt. A source file whose mtime is after -watermark is excluded
// from this run and listed by path -- not derived, not marked
// source_unreadable, simply deferred to a later run once its mtime is
// covered by a later watermark.
//
// # Never the live corpus, unless explicitly authorized
//
// -db is refused outright if it resolves to the live corpus
// (internal/corpus.Path()) via internal/livepath.RefuseLivePath -- the SAME
// guard cmd/codexingest and cmd/provenancebackfill both reuse, never a third
// copy of it. -authorized-live-write lifts that refusal only when -db ALSO
// explicitly names the live path.
//
// # Sources are read-only, including touch
//
// Every source file this command opens is opened via rollout.AnalyzeFile,
// which never writes, truncates, or touches a source path. -sessions-root
// (default ~/.codex/sessions) is stat'd -- never walked to decide anything
// -- once before and once after the run purely as evidence nothing under it
// moved.
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
	fs := flag.NewFlagSet("contextbackfill", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to a SQLite corpus COPY (never the live corpus)")
	recordWatermark := fs.Bool("record-watermark", false, "print the current time as RFC3339Nano and exit; touches nothing else")
	watermarkStr := fs.String("watermark", "", "RFC3339Nano timestamp recorded by -record-watermark; frozen for the whole run")
	apply := fs.Bool("apply", false, "write prompts.context and prompt_context rows for the plan; default is a zero-write dry run")
	sessionsRoot := fs.String("sessions-root", defaultSessionsRoot(), "root of Codex rollout JSONL files, stat'd (never walked or opened) before and after as read-only evidence")
	authorizedLiveWrite := fs.Bool("authorized-live-write", false,
		"explicit human authorization to run -apply against the live corpus. Default false, never inferable from "+
			"any other flag or environment variable. Only takes effect when -db ALSO explicitly names the live "+
			"path -- it never causes a default or inferred path to be treated as live-authorized.")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *recordWatermark {
		fmt.Fprintln(stdout, time.Now().UTC().Format(time.RFC3339Nano))
		return 0
	}

	if *dbPath == "" {
		fmt.Fprintln(stderr, "contextbackfill: -db is required")
		return 2
	}
	if *watermarkStr == "" {
		fmt.Fprintln(stderr, "contextbackfill: -watermark is required (run -record-watermark once, then pass its exact output here)")
		return 2
	}
	watermark, err := time.Parse(time.RFC3339Nano, *watermarkStr)
	if err != nil {
		fmt.Fprintf(stderr, "contextbackfill: -watermark %q is not RFC3339Nano: %v\n", *watermarkStr, err)
		return 2
	}

	liveReason, live := livepath.RefuseLivePath(*dbPath)
	if live && !*authorizedLiveWrite {
		mode := "dry-run"
		if *apply {
			mode = "-apply"
		}
		fmt.Fprintf(stderr, "contextbackfill: refusing %s against %s: %s\n", mode, *dbPath, liveReason)
		return 1
	}

	if live && *authorizedLiveWrite && *apply {
		planned, err := buildAndApply(*dbPath, watermark, false)
		if err != nil {
			fmt.Fprintf(stderr, "contextbackfill: computing authorized live-write plan: %v\n", err)
			return 1
		}
		fmt.Fprintln(stderr, "================================================================================")
		fmt.Fprintln(stderr, "AUTHORIZED LIVE-CORPUS WRITE -- -authorized-live-write was passed explicitly")
		fmt.Fprintf(stderr, "  path:      %s\n", *dbPath)
		fmt.Fprintf(stderr, "  reason:    %s\n", liveReason)
		fmt.Fprintf(stderr, "  candidate rows: %d\n", planned.TotalCandidates)
		fmt.Fprintf(stderr, "  watermark: %s\n", watermark.Format(time.RFC3339Nano))
		fmt.Fprintln(stderr, "================================================================================")
	}

	beforeMtime, beforeErr := latestSourceMtime(*sessionsRoot)

	report, err := buildAndApply(*dbPath, watermark, *apply)
	if err != nil {
		fmt.Fprintf(stderr, "contextbackfill: %v\n", err)
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

// latestSourceMtime is read-only evidence only -- see cmd/codexingest's own
// identical helper doc comment; it plays no role in deciding what this
// command derives.
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

func printReport(w *os.File, r Report, applied bool, sessionsRoot string, before time.Time, beforeErr error, after time.Time, afterErr error) {
	fmt.Fprintf(w, "watermark: %s\n", r.Watermark.Format(time.RFC3339Nano))
	fmt.Fprintf(w, "mode: %s\n", map[bool]string{true: "apply", false: "dry-run"}[applied])
	fmt.Fprintf(w, "candidates (prompts with codex_provenance and context=''): %d\n", r.TotalCandidates)
	fmt.Fprintf(w, "derived: %d\n", r.Derived)
	fmt.Fprintf(w, "no_prior_turn: %d\n", r.NoPriorTurn)
	fmt.Fprintf(w, "source_unreadable: %d\n", r.SourceUnreadable)
	fmt.Fprintf(w, "record_unresolved: %d\n", r.RecordUnresolved)
	fmt.Fprintf(w, "already processed (idempotent skip): %d\n", r.AlreadyProcessed)
	fmt.Fprintf(w, "excluded, source changed after watermark: %d\n", r.ExcludedWatermarkRows)
	for _, f := range r.ExcludedWatermarkFiles {
		fmt.Fprintf(w, "  excluded: %s\n", f)
	}

	accounted := r.Derived + r.NoPriorTurn + r.SourceUnreadable + r.RecordUnresolved + r.AlreadyProcessed + r.ExcludedWatermarkRows
	if accounted != r.TotalCandidates {
		fmt.Fprintf(w, "MISMATCH: %d candidates accounted for, want exactly %d -- do not trust this report\n", accounted, r.TotalCandidates)
	} else {
		fmt.Fprintf(w, "accounting: %d candidates in, %d decided (derived + no_prior_turn + source_unreadable + record_unresolved + already_processed + excluded_watermark) -- exact\n", r.TotalCandidates, accounted)
	}

	fmt.Fprintf(w, "prompt_context rows before: %d\n", r.PromptContextBefore)
	fmt.Fprintf(w, "prompt_context rows after: %d\n", r.PromptContextAfter)
	if applied {
		wrote := r.Derived + r.NoPriorTurn + r.SourceUnreadable + r.RecordUnresolved
		if r.PromptContextAfter-r.PromptContextBefore != wrote {
			fmt.Fprintf(w, "MISMATCH: table grew by %d but this run decided %d rows to write -- do not trust this apply\n",
				r.PromptContextAfter-r.PromptContextBefore, wrote)
		} else {
			fmt.Fprintf(w, "table growth matches this run's written count exactly (%d)\n", wrote)
		}
	}

	fmt.Fprintf(w, "sessions root (read-only evidence, never walked to decide anything): %s\n", sessionsRoot)
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
