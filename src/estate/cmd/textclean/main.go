// Command textclean backfills prompts.text_clean for the source prompts
// behind every live parameter (agent-estate#1394): CLAUDE.local.md forbids
// publishing text_raw and requires text_clean to exist before a prompt is
// quoted, and 900 of the 970 prompts a live parameter points at (measured
// 2026-09-11) have never had it made. See internal/corpus/textclean.go for
// the generator/verifier this command drives -- a closed whitelist of
// single-word substitutions, never a rewriter, gated by an independent
// meaning-preservation check on every proposed write.
//
// # Report is the default, apply is explicit
//
// With neither -db nor -apply, this reads the LIVE corpus read-only
// (internal/corpus.Path()) and prints what WOULD change -- a diff per row,
// never a write. -apply requires -db to name a path that is NOT the live
// corpus (internal/livepath.RefuseLivePath, the same guard
// cmd/provenancebackfill uses -- no override flag exists here at all: this
// task's own brief is "do not apply to the live corpus," a harder line than
// provenancebackfill's own -authorized-live-write escape hatch) and a
// -backup-manifest naming a real, already-verified backup
// (scripts/evidence/backup_and_restore_test.py's own manifest.json) --
// "refuses without a verified backup" made into an enforced precondition,
// not a comment asking a caller to have remembered one.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/jonhill90/agent-estate/estate/internal/corpus"
	"github.com/jonhill90/agent-estate/estate/internal/livepath"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("textclean", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "corpus path to read (default: the live corpus, read-only). Required, and must NOT be the live corpus, when -apply is given.")
	apply := fs.Bool("apply", false, "write proposed text_clean values to -db. Refused against the live corpus and without -backup-manifest.")
	backupManifest := fs.String("backup-manifest", "", "path to a manifest.json from scripts/evidence/backup_and_restore_test.py, proving a verified backup exists. Required with -apply.")
	limit := fs.Int("limit", 0, "show at most N rows in the per-row report (0 = show all)")
	showRefused := fs.Bool("show-refused", true, "include refused rows in the report, with their reason")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	live, err := corpus.Path()
	if err != nil {
		fmt.Fprintln(stderr, "textclean:", err)
		return 1
	}

	readPath := *dbPath
	if readPath == "" {
		readPath = live
	}

	if *apply {
		if *dbPath == "" {
			fmt.Fprintln(stderr, "textclean: -apply requires -db naming a scratch copy -- refusing to guess a target")
			return 2
		}
		if reason, isLive := livepath.RefuseLivePath(*dbPath); isLive {
			fmt.Fprintf(stderr, "textclean: refusing -apply against %s: %s\n", *dbPath, reason)
			return 1
		}
		if *backupManifest == "" {
			fmt.Fprintln(stderr, "textclean: -apply requires -backup-manifest (a verified backup from scripts/evidence/backup_and_restore_test.py) -- refusing to write without one")
			return 2
		}
		if err := verifyBackupManifest(*backupManifest); err != nil {
			fmt.Fprintf(stderr, "textclean: -backup-manifest %s did not verify: %v\n", *backupManifest, err)
			return 1
		}
	}

	candidates, err := corpus.TextCleanCandidates(readPath)
	if err != nil {
		fmt.Fprintln(stderr, "textclean:", err)
		return 1
	}
	if len(candidates) == 0 {
		fmt.Fprintln(stdout, "textclean: zero candidates -- every prompt behind a live parameter already has text_clean")
		return 0
	}

	var proposals []corpus.CleanProposal
	for _, c := range candidates {
		proposals = append(proposals, corpus.ProposeClean(c.PromptID, c.Raw))
	}
	sort.Slice(proposals, func(i, j int) bool { return proposals[i].PromptID < proposals[j].PromptID })

	printReport(stdout, readPath, proposals, *limit, *showRefused, *apply)

	if *apply {
		before, err := corpus.CountChangedRows(*dbPath)
		if err != nil {
			fmt.Fprintln(stderr, "textclean:", err)
			return 1
		}
		written, err := corpus.ApplyTextClean(*dbPath, proposals)
		if err != nil {
			fmt.Fprintln(stderr, "textclean: apply failed partway through:", err)
			return 1
		}
		after, err := corpus.CountChangedRows(*dbPath)
		if err != nil {
			fmt.Fprintln(stderr, "textclean:", err)
			return 1
		}
		fmt.Fprintf(stdout, "\n%d row(s) written to %s\n", written, *dbPath)
		fmt.Fprintf(stdout, "prompts.text_clean populated: %d before, %d after (delta %d)\n", before, after, after-before)
	}
	return 0
}

func printReport(w *os.File, dbPath string, proposals []corpus.CleanProposal, limit int, showRefused, applying bool) {
	var clean, identity, refuse int
	for _, p := range proposals {
		switch p.Action {
		case corpus.ActionClean:
			clean++
		case corpus.ActionIdentity:
			identity++
		case corpus.ActionRefuse:
			refuse++
		}
	}
	mode := "REPORT (read-only, nothing written)"
	if applying {
		mode = "APPLY"
	}
	fmt.Fprintf(w, "# text_clean backfill -- %s\n\n", mode)
	fmt.Fprintf(w, "source: %s\n", dbPath)
	fmt.Fprintf(w, "%d candidate prompt(s): %d would be cleaned (a whitelisted fix applied and verified), "+
		"%d already read cleanly (copied verbatim, nothing in the whitelist matched), %d refused (a fix was "+
		"attempted but failed the meaning-preservation check, or nothing safe could be done -- left NULL).\n\n",
		len(proposals), clean, identity, refuse)

	shown := 0
	for _, p := range proposals {
		if limit > 0 && shown >= limit {
			fmt.Fprintf(w, "... %d more not shown (-limit %d)\n", len(proposals)-shown, limit)
			break
		}
		switch p.Action {
		case corpus.ActionClean:
			fmt.Fprintf(w, "[%s] CLEAN\n  raw:   %s\n  clean: %s\n", p.PromptID, p.Raw, p.Clean)
			for _, c := range p.Changes {
				fmt.Fprintf(w, "    - %s\n", c)
			}
			shown++
		case corpus.ActionIdentity:
			// Identity rows are real (a copy, populating a NULL column) but
			// not interesting to read one by one in the default report --
			// counted above, not printed per-row, unless nothing else is
			// happening for this id.
		case corpus.ActionRefuse:
			if showRefused {
				fmt.Fprintf(w, "[%s] REFUSED: %s\n  raw: %s\n", p.PromptID, p.Reason, p.Raw)
				shown++
			}
		}
	}
}

// backupManifest mirrors the fields backup_and_restore_test.py's own
// manifest.json writes for the corpus-sqlite3 source -- only the fields
// this command actually checks, not the whole schema.
type backupManifestFile struct {
	Results []struct {
		Name          string `json:"name"`
		Status        string `json:"status"`
		LiveUntouched bool   `json:"live_untouched"`
		RestoreTest   struct {
			ByteIdentical bool `json:"byte_identical"`
		} `json:"restore_test"`
	} `json:"results"`
}

// verifyBackupManifest reads a manifest.json and confirms it records an OK,
// live-untouched, byte-identical-restore backup of the corpus specifically
// -- not merely that some file exists at the path given. This is what
// makes "refuses without a verified backup" an enforced precondition.
func verifyBackupManifest(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m backupManifestFile
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("not a valid manifest.json: %w", err)
	}
	for _, r := range m.Results {
		if r.Name != "corpus-sqlite3" {
			continue
		}
		if r.Status != "ok" {
			return fmt.Errorf("corpus-sqlite3 backup status is %q, not ok", r.Status)
		}
		if !r.LiveUntouched {
			return fmt.Errorf("corpus-sqlite3 backup did not confirm the live corpus was untouched")
		}
		if !r.RestoreTest.ByteIdentical {
			return fmt.Errorf("corpus-sqlite3 backup's own restore test did not confirm byte-identical restore")
		}
		return nil
	}
	return fmt.Errorf("manifest has no corpus-sqlite3 entry")
}
