package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	asJSON := flag.Bool("json", false, "emit the machine-readable report instead of the human summary")
	verbose := flag.Bool("verbose", false, "include the per-file record breakdown in the human summary")
	flag.Parse()

	if *root == "" {
		fmt.Fprintln(os.Stderr, "capturehealth: could not resolve a default root (no home dir); pass -root explicitly")
		os.Exit(2)
	}

	// BuildSourceHealth (contract.go) runs the same walk buildReport always
	// ran and additionally classifies it through the slice 3 per-source
	// contract (internal/provenance.SourceHealth) -- see that file's doc
	// comment. Unlike the plain buildReport this replaced, an unreachable or
	// unreadable root is no longer a fatal process error: it is now a typed
	// SourceState the report itself carries, matching the contract's own
	// "missing and unreadable reported separately from empty" requirement
	// (agent-estate#1139). No pre-existing count changes for a reachable
	// root -- see contract_test.go for the before/after proof this doesn't
	// move anything buildReport already computed.
	report, err := BuildSourceHealth(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capturehealth: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "capturehealth: encoding json: %v\n", err)
			os.Exit(1)
		}
		return
	}

	printHuman(report, *verbose)
}

func printHuman(r Report, verbose bool) {
	fmt.Printf("root: %s\n", r.Root)
	fmt.Printf("rollout files seen: %d (parsed %d, unparseable %d)\n", r.FilesTotal, r.FilesParsed, len(r.FilesUnparseable))
	fmt.Printf("genuine operator turns (role=user AND content[0].type=input_text): %d\n", r.OperatorTurnsTotal)

	fmt.Printf("session_meta records: %d raw, %d files carry more than one DISTINCT session id\n",
		r.SessionMetaTotal, r.FilesWithMultipleSessions)

	fmt.Printf("compacted records: %d (raw replacement_history user turns: %d, distinct: %d, overlap-with-response_item: %d, only-in-compacted: %d)\n",
		r.CompactedRecordsTotal, r.CompactedUserTurnsRawTotal, r.CompactedUserTurnsDistinctTotal,
		r.CompactedOverlapWithResponseItemTotal, r.CompactedOnlyInCompactedTotal)

	fmt.Println("record type counts:")
	for _, k := range sortedKeys(r.RecordTypeCounts) {
		fmt.Printf("  %-32s %d\n", k, r.RecordTypeCounts[k])
	}

	fmt.Println("response_item role counts:")
	for _, k := range sortedKeys(r.RoleCounts) {
		fmt.Printf("  %-32s %d\n", k, r.RoleCounts[k])
	}

	fmt.Println("user-role content[0].type counts:")
	for _, k := range sortedKeys(r.UserContentTypeCounts) {
		fmt.Printf("  %-32s %d\n", k, r.UserContentTypeCounts[k])
	}

	if len(r.FilesUnparseable) > 0 {
		fmt.Println("unparseable files:")
		for _, f := range r.FilesUnparseable {
			fmt.Printf("  %s: %s\n", f.Path, f.Reason)
		}
	} else {
		fmt.Println("unparseable files: none")
	}

	// Appended after every pre-existing line, never inserted among them, so
	// a before/after diff against slice 2's output shows only this new
	// block added -- see agent-estate#1139 slice 3 acceptance criterion 4.
	h := r.SourceHealth
	fmt.Println("source health (contract):")
	fmt.Printf("  source: %s  harness: %s  state: %s\n", h.SourceName, h.Harness, h.State)
	if h.Freshness.Known {
		fmt.Printf("  newest capture: %s (%ds ago)\n", h.Freshness.NewestCapturedAt, h.Freshness.SecondsSinceCapture)
	} else {
		fmt.Println("  newest capture: unknown")
	}

	if verbose {
		fmt.Println("per-file breakdown:")
		for _, fr := range r.Files {
			fmt.Printf("  %s\n", fr.Path)
			fmt.Printf("    session ids: %v\n", fr.SessionIDs)
			fmt.Printf("    operator turns: %d\n", fr.OperatorTurns)
			for _, s := range fr.Sessions {
				fmt.Printf("      %s: %d turns\n", s.SessionID, s.OperatorTurns)
			}
			if fr.CompactedRecords > 0 {
				fmt.Printf("    compacted: %d records, %d distinct turns (%d overlap, %d only-in-compacted)\n",
					fr.CompactedRecords, fr.CompactedUserTurnsDistinct, fr.CompactedOverlapWithResponseItem, fr.CompactedOnlyInCompacted)
			}
			for _, k := range sortedKeys(fr.RecordTypeCounts) {
				fmt.Printf("    %-30s %d\n", k, fr.RecordTypeCounts[k])
			}
		}
	}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
