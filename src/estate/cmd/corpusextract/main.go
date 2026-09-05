package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func defaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

func main() {
	root := flag.String("root", defaultRoot(), "root directory of Codex rollout JSONL files (read-only)")
	asJSON := flag.Bool("json", false, "emit the full manifest as JSON instead of the human summary")
	out := flag.String("out", "", "write the JSON manifest to this path instead of stdout (summary still prints to stdout unless -json)")

	// watermark pins the run to an explicit point in time (RFC3339 or
	// RFC3339Nano) rather than the default -- the highest mtime this run's
	// own single listing pass observes. Every file whose mtime is after the
	// watermark is excluded and named in the manifest's
	// excluded_after_watermark list (agent-estate#1139 B1 acceptance
	// criteria 1 and 4). Two runs given the SAME -watermark against an
	// unchanged source tree produce an identical manifest (criterion 5).
	watermarkFlag := flag.String("watermark", "", "pin the run to this RFC3339/RFC3339Nano timestamp instead of auto-deriving one from this run's own listing pass; files modified after it are excluded and listed")

	// slice2* default to the figures agent-estate#1139 slice 2 measured
	// against the live tree (PR #1226) -- see cmd/capturehealth's own doc
	// comment. Overridable so this binary keeps working if a future
	// measurement supersedes them; the acceptance criterion is that THIS
	// run states whether it agrees, not that the defaults never change.
	slice2Distinct := flag.Int("slice2-distinct", 1579, "slice 2's measured CompactedUserTurnsDistinctTotal, for reconciliation")
	slice2Overlap := flag.Int("slice2-overlap", 1535, "slice 2's measured CompactedOverlapWithResponseItemTotal, for reconciliation")
	slice2Only := flag.Int("slice2-only", 44, "slice 2's measured CompactedOnlyInCompactedTotal, for reconciliation")
	flag.Parse()

	if *root == "" {
		fmt.Fprintln(os.Stderr, "corpusextract: could not resolve a default root (no home dir); pass -root explicitly")
		os.Exit(2)
	}

	var manifest Manifest
	var err error
	if *watermarkFlag == "" {
		manifest, err = buildManifest(*root)
	} else {
		var wm time.Time
		wm, err = time.Parse(time.RFC3339Nano, *watermarkFlag)
		if err != nil {
			wm, err = time.Parse(time.RFC3339, *watermarkFlag)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "corpusextract: -watermark %q: not a valid RFC3339/RFC3339Nano timestamp: %v\n", *watermarkFlag, err)
			os.Exit(2)
		}
		manifest, err = buildManifestAtWatermark(*root, wm)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "corpusextract: %v\n", err)
		os.Exit(1)
	}

	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "corpusextract: creating -out file: %v\n", err)
			os.Exit(1)
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		encErr := enc.Encode(manifest)
		closeErr := f.Close()
		if encErr != nil {
			fmt.Fprintf(os.Stderr, "corpusextract: writing manifest: %v\n", encErr)
			os.Exit(1)
		}
		if closeErr != nil {
			fmt.Fprintf(os.Stderr, "corpusextract: closing -out file: %v\n", closeErr)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "corpusextract: wrote manifest to %s\n", *out)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(manifest); err != nil {
			fmt.Fprintf(os.Stderr, "corpusextract: encoding json: %v\n", err)
			os.Exit(1)
		}
		return
	}

	PrintSummary(os.Stdout, manifest, *slice2Distinct, *slice2Overlap, *slice2Only)
}
