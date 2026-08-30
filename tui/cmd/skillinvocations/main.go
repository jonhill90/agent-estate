// Command skillinvocations builds the private, per-machine cache
// internal/skills.InvocationFetcher reads for the Skills pane's
// INVOCATIONS column (agent-tui#174, decided on agent-tui#164). It scans
// this machine's own `~/.claude/projects/**/*.jsonl` transcripts for
// `Skill` tool_use blocks and writes a Dir -> count rollup to
// internal/skills.DefaultInvocationCachePath() (or -out).
//
// Deliberately NOT run automatically by cmd/estate on every fetch: the
// corpus is several GB and scanning it takes real time (measured: ~10s
// for ~3,200 files on this machine). Re-run this by hand, on whatever
// cadence a human chooses -- the same relationship `scripts/eval_skill.py`
// has to `docs/eval-status.json` in jonhill90/skills, which cmd/estate
// also only ever reads, never rebuilds.
//
//	go run ./cmd/skillinvocations
//	go run ./cmd/skillinvocations -transcripts /path/to/.claude/projects -out /path/to/cache.json
//
// The written file is never inside a git checkout by default (see
// internal/skills.DefaultInvocationCacheDir's own doc comment for why:
// agent-tui#164's decision that this is private, per-machine, ephemeral
// cache, never evidence worth versioning in jonhill90/skills or
// agent-evals). Passing -out a path inside a checkout is possible but not
// the default, and doing so is the caller's own mistake to avoid, not
// something this command guards against -- the same posture
// resolveLedgerSource takes on -ledger in cmd/estate.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jonhill90/agent-estate/tui/internal/skills"
)

func main() {
	homeDir, homeErr := os.UserHomeDir()
	defaultTranscripts := ""
	if homeErr == nil {
		defaultTranscripts = filepath.Join(homeDir, ".claude", "projects")
	}

	transcripts := flag.String("transcripts", defaultTranscripts,
		"directory of *.jsonl transcript files to scan, at any depth (default: ~/.claude/projects)")
	out := flag.String("out", "", "cache file to write (default: internal/skills.DefaultInvocationCachePath())")
	flag.Parse()

	if *transcripts == "" {
		fmt.Fprintln(os.Stderr, "skillinvocations: -transcripts is required (could not resolve $HOME)")
		os.Exit(2)
	}

	outPath := *out
	if outPath == "" {
		p, err := skills.DefaultInvocationCachePath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "skillinvocations: -out not given and could not resolve a default: %v\n", err)
			os.Exit(2)
		}
		outPath = p
	}

	counts, err := skills.BuildInvocationCache(*transcripts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skillinvocations: scanning %s: %v\n", *transcripts, err)
		os.Exit(1)
	}

	builtAt := time.Now().UTC().Format(time.RFC3339)
	if err := skills.WriteInvocationCache(outPath, builtAt, counts); err != nil {
		fmt.Fprintf(os.Stderr, "skillinvocations: writing %s: %v\n", outPath, err)
		os.Exit(1)
	}

	total := 0
	for _, n := range counts {
		total += n
	}
	fmt.Printf("skillinvocations: %d distinct skills, %d total invocations, written to %s (built_at=%s)\n",
		len(counts), total, outPath, builtAt)
}
