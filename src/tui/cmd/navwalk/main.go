// Command navwalk is internal/navwalk's own CLI -- the two-command
// workflow agent-b3.md's structural fix expects every future nav-walk
// lane to use, in place of hand-editing
// testdata/vhs/full-nav-walk-report.md directly:
//
//  1. Record what you observed driving one route with testdata/vhs, by
//     appending one line to that route's own file:
//
//     go run ./cmd/navwalk -record -id chat -date 2026-08-23 \
//     -source "PR agent-tui#101 (my-brief.md)" -verdict RENDERS \
//     -notes "real thread titles, no fixture banner"
//
//  2. Regenerate the human-readable table from every route's own latest
//     observation (mechanical, not manual):
//
//     go run ./cmd/navwalk
//
// Both commands touch only testdata/vhs/nav-walk/observations/<id>.jsonl
// (step 1) and testdata/vhs/full-nav-walk-report.md (step 2, always
// rewritten whole) -- never any OTHER route's own observation file, which
// is the property that stops two lanes measuring two different
// destinations from conflicting with each other (internal/navwalk's own
// package doc comment).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jonhill90/agent-estate/src/tui/internal/navwalk"
)

func main() {
	var (
		dir = flag.String("dir", "testdata/vhs/nav-walk",
			"directory holding manifest.json and observations/*.jsonl")
		out = flag.String("out", "testdata/vhs/full-nav-walk-report.md",
			"generated report path -- always rewritten whole, never hand-edited")

		record  = flag.Bool("record", false, "append one observation instead of regenerating the report")
		id      = flag.String("id", "", "-record: the nav route id (internal/nav.Item.ID, e.g. \"chat\")")
		date    = flag.String("date", "", "-record: YYYY-MM-DD the observation was made")
		source  = flag.String("source", "", "-record: what produced this observation, e.g. \"PR agent-tui#101 (my-brief.md)\"")
		verdict = flag.String("verdict", "", "-record: RENDERS | STALE | STUB | EMPTY | BROKEN | REMOVED | \"could not measure\"")
		notes   = flag.String("notes", "", "-record: what was actually seen, specific enough for a stranger to check")
	)
	flag.Parse()

	if *record {
		if err := runRecord(*dir, *id, *date, *source, *verdict, *notes); err != nil {
			fmt.Fprintln(os.Stderr, "navwalk:", err)
			os.Exit(1)
		}
		return
	}
	if err := runGenerate(*dir, *out); err != nil {
		fmt.Fprintln(os.Stderr, "navwalk:", err)
		os.Exit(1)
	}
}

func runRecord(dir, id, date, source, verdict, notes string) error {
	if id == "" || date == "" || source == "" || verdict == "" {
		return fmt.Errorf("-record requires -id, -date, -source and -verdict (got id=%q date=%q source=%q verdict=%q)", id, date, source, verdict)
	}
	path := dir + "/observations/" + id + ".jsonl"
	obs := navwalk.Observation{Date: date, Source: source, Verdict: navwalk.Verdict(verdict), Notes: notes}
	if err := navwalk.AppendObservation(path, obs); err != nil {
		return err
	}
	fmt.Printf("recorded %s -> %s\n", id, path)
	return nil
}

func runGenerate(dir, out string) error {
	manifest, err := navwalk.ReadManifest(dir + "/manifest.json")
	if err != nil {
		return err
	}
	rows, err := navwalk.Resolve(dir, manifest)
	if err != nil {
		return err
	}
	report := navwalk.Render(rows)
	if err := os.WriteFile(out, []byte(report), 0o644); err != nil {
		return fmt.Errorf("navwalk: write %s: %w", out, err)
	}
	fmt.Printf("wrote %s from %d destinations\n", out, len(rows))
	return nil
}
