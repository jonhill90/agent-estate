package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/jonhill90/agent-estate/estate/internal/candidates"
	"github.com/jonhill90/agent-estate/estate/internal/livepath"
)

// runCandidatesRegisterSource is `estate candidates register-source`: the
// catalogue-sourced counterpart to bare `estate candidates` (which derives
// conversation-sourced candidates from codex_provenance in bulk). One
// invocation registers ONE catalogue source citation.
//
// Lane B's frozen catalogue API is not yet available to this command (see
// run/source-api.md once it exists -- this task's brief: "code against the
// DOCUMENT; stub B's API until its PR merges, then integrate"). Until then,
// the three fields a citation needs (id, locator, content hash) are
// supplied directly by the operator/caller as flags, describing a source
// that already exists in Lane B's catalogue -- never inventing one. Once
// B's exported lookup lands, this is the ONLY call site that changes: it
// will populate candidates.CatalogueSource from B's own return value
// instead of these flags, and internal/candidates itself does not move.
func runCandidatesRegisterSource(args []string) {
	fs := flag.NewFlagSet("candidates register-source", flag.ExitOnError)
	dbPath := fs.String("db", "", "path to a corpus copy or the live corpus (default: the live corpus)")
	id := fs.String("id", "", "stable id this source is known by in Lane B's catalogue")
	locator := fs.String("locator", "", "where the original actually is (path, URL, or owner/repo) -- contract `locator`")
	hash := fs.String("hash", "", "content hash / revision marker of the cited material -- contract `revision`/`hash`")
	apply := fs.Bool("apply", false, "write the registration; default is a zero-write dry run")
	authorizedLiveWrite := fs.Bool("authorized-live-write", false,
		"explicit human authorization to run -apply against the live corpus. Default false, never inferable from "+
			"any other flag or environment variable. Only takes effect when -db ALSO explicitly names the live "+
			"path -- it never causes a default or inferred path to be treated as live-authorized.")
	fs.Parse(args)
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "estate: unrecognised argument %q for candidates register-source\n", fs.Arg(0))
		os.Exit(2)
	}
	resolvedDB := resolveCandidatesDBPath(*dbPath)

	liveReason, live := livepath.RefuseLivePath(resolvedDB)
	if live && !*authorizedLiveWrite {
		mode := "dry-run"
		if *apply {
			mode = "-apply"
		}
		fmt.Fprintf(os.Stderr, "estate candidates register-source: refusing %s against %s: %s\n", mode, resolvedDB, liveReason)
		os.Exit(1)
	}
	if live && *authorizedLiveWrite && *apply {
		fmt.Fprintln(os.Stderr, "================================================================================")
		fmt.Fprintln(os.Stderr, "AUTHORIZED LIVE-CORPUS WRITE -- -authorized-live-write was passed explicitly")
		fmt.Fprintf(os.Stderr, "  path:   %s\n", resolvedDB)
		fmt.Fprintf(os.Stderr, "  reason: %s\n", liveReason)
		fmt.Fprintln(os.Stderr, "================================================================================")
	}

	res, err := candidates.RegisterCatalogueSource(resolvedDB, candidates.CatalogueSource{
		ID:          *id,
		Locator:     *locator,
		ContentHash: *hash,
	}, *apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "estate:", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(res); err != nil {
		fmt.Fprintln(os.Stderr, "estate: encode json:", err)
		os.Exit(2)
	}
}
