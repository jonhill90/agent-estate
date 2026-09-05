// This file is agent-estate#1139 slice 3's own acceptance criterion 4: the
// existing capturehealth output expressed in terms of the generic
// provenance.SourceHealth contract (internal/provenance), with every
// pre-existing Report field computed exactly as before. BuildSourceHealth is
// additive on top of buildReport -- it does not change what buildReport
// itself computes, only classifies the SAME walk into the shape a second
// source (Claude transcripts, ...) will also produce. Prove this by running
// `capturehealth -json` before and after this file existed and diffing: the
// diff is limited to the new source_health field appearing; no pre-existing
// count moves.
package main

import (
	"os"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/provenance"
)

// These two constants are this file's only Codex-specific knowledge --
// everything else here is generic contract plumbing. A second source's own
// conversion code states its own SourceName/Harness the same way.
const (
	sourceNameCodexRollout = "codex-rollout"
	harnessCodex           = "codex"
)

// BuildSourceHealth is capturehealth's own entry point for slice 3: it
// classifies root's reachability first (SourceStateMissing /
// SourceStateUnreadable), then runs the existing read-only buildReport walk
// and expresses its result as a provenance.SourceHealth. The returned
// Report is exactly what buildReport would have produced on its own,
// with SourceHealth attached -- see toSourceHealth below for the field
// mapping.
func BuildSourceHealth(root string) (Report, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return Report{
				Root: root,
				SourceHealth: provenance.SourceHealth{
					SourceName: sourceNameCodexRollout,
					Harness:    harnessCodex,
					Root:       root,
					State:      provenance.SourceStateMissing,
				},
			}, nil
		}
		// The root exists but stat itself failed (permission denied on a
		// parent directory, for example) -- unreadable, not missing: the
		// source is known to exist, this reader just could not see into it.
		return Report{
			Root: root,
			SourceHealth: provenance.SourceHealth{
				SourceName: sourceNameCodexRollout,
				Harness:    harnessCodex,
				Root:       root,
				State:      provenance.SourceStateUnreadable,
			},
		}, nil
	}

	report, err := buildReport(root)
	if err != nil {
		// buildReport itself only fails on a walk error after root was
		// confirmed to exist (e.g. a subdirectory with no read permission) --
		// unreadable, same as the stat-failure case above, not a fatal
		// process error. Unlike a per-file unparseable entry (which does not
		// fail buildReport at all -- see analyzeFile's own doc comment),
		// this is a directory-listing failure, which buildReport has no way
		// to attribute to one file.
		return Report{
			Root: root,
			SourceHealth: provenance.SourceHealth{
				SourceName:       sourceNameCodexRollout,
				Harness:          harnessCodex,
				Root:             root,
				State:            provenance.SourceStateUnreadable,
				FilesUnparseable: []provenance.ParseFailure{{Path: root, Reason: err.Error()}},
			},
		}, nil
	}

	report.SourceHealth = toSourceHealth(report)
	return report, nil
}

// toSourceHealth is the field-by-field mapping this criterion requires:
// every value on the right already existed in Report before slice 3: this
// function only re-labels them into the generic contract shape, it computes
// nothing new. The one genuinely new measurement -- Report.NewestTimestamp
// -- was added to health.go as a purely additive field (see that file's own
// comments) specifically so this function would have something to build
// Freshness from without inventing a second timestamp scan.
func toSourceHealth(report Report) provenance.SourceHealth {
	state := provenance.SourceStatePopulated
	if report.FilesTotal == 0 || report.OperatorTurnsTotal == 0 {
		state = provenance.SourceStateEmpty
	}

	failures := make([]provenance.ParseFailure, 0, len(report.FilesUnparseable))
	for _, f := range report.FilesUnparseable {
		failures = append(failures, provenance.ParseFailure{Path: f.Path, Reason: f.Reason})
	}

	return provenance.SourceHealth{
		SourceName:       sourceNameCodexRollout,
		Harness:          harnessCodex,
		Root:             report.Root,
		State:            state,
		FilesTotal:       report.FilesTotal,
		FilesParsed:      report.FilesParsed,
		FilesUnparseable: failures,
		UnitsExtracted:   report.OperatorTurnsTotal,
		Freshness:        freshnessFrom(report.NewestTimestamp),
	}
}

// freshnessFrom converts Report.NewestTimestamp (a raw RFC3339 string, or ""
// if no record anywhere carried a parsable timestamp) into the typed-absence
// provenance.Freshness shape. "now" is measured once, at report-build time,
// not re-derived by a later reader.
func freshnessFrom(newestTimestamp string) provenance.Freshness {
	if newestTimestamp == "" {
		return provenance.Freshness{Known: false}
	}
	t, err := time.Parse(time.RFC3339Nano, newestTimestamp)
	if err != nil {
		return provenance.Freshness{Known: false}
	}
	return provenance.Freshness{
		Known:               true,
		NewestCapturedAt:    newestTimestamp,
		SecondsSinceCapture: int64(time.Since(t).Seconds()),
	}
}
