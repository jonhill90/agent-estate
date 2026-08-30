package navwalk

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ResolvedRow is one manifest entry paired with its latest Observation
// (or none, if nobody has ever measured it) -- the unit Render turns into
// one table row.
type ResolvedRow struct {
	Entry ManifestEntry
	Obs   Observation
	Known bool // false means "never measured" -- Obs is zero, not real
}

// Resolve reads every manifest entry's own observation file under dir
// (observations/<id>.jsonl) and keeps only its latest Observation --
// this is the ONE place file-per-route storage gets collapsed back into
// one row per destination, mechanically, from the parts on disk.
func Resolve(dir string, manifest []ManifestEntry) ([]ResolvedRow, error) {
	rows := make([]ResolvedRow, 0, len(manifest))
	for _, e := range manifest {
		path := filepath.Join(dir, "observations", e.ID+".jsonl")
		obs, err := ReadObservations(path)
		if err != nil {
			return nil, err
		}
		latest, ok := Latest(obs)
		rows = append(rows, ResolvedRow{Entry: e, Obs: latest, Known: ok})
	}
	return rows, nil
}

// Render builds the same human-readable Markdown table/summary shape
// every hand-maintained nav-walk report before this one used, mechanically
// from rows -- this is the "regenerating the summary is mechanical, not
// manual" half of agent-b3.md's own requirement. The generated file
// carries its own "do not hand-edit" header naming exactly where to
// append instead.
func Render(rows []ResolvedRow) string {
	var b strings.Builder
	b.WriteString("# Full nav-tree walk — honest render report\n\n")
	b.WriteString("**GENERATED. Do not hand-edit this file.** Rebuilt by " +
		"`go run ./cmd/navwalk` from `testdata/vhs/nav-walk/manifest.json` " +
		"(the destination list) and `testdata/vhs/nav-walk/observations/*.jsonl` " +
		"(one append-only file per destination, agent-b3.md's own structural " +
		"fix for a shared table that every nav-measuring lane used to conflict " +
		"on). To record a new measurement: append one `navwalk.Observation` to " +
		"that route's own `.jsonl` file (`navwalk.AppendObservation`), then " +
		"re-run the generator. A route with no observation file yet renders " +
		"as `could not measure` below, not a blank row.\n\n")

	b.WriteString("Legend: **RENDERS** real content, looks right · **STALE** " +
		"rendered real content known not to be current -- a fetch failed or " +
		"timed out and the pane fell back to its last good data instead of " +
		"blanking (agent-tui#176's designed failure mode), notes state the " +
		"age where the pane itself surfaces one · **EMPTY** renders but shows " +
		"nothing (correct or bug, stated in its own notes) · **STUB** still " +
		"the placeholder · **BROKEN** error/panic/garbage · **REMOVED** no " +
		"longer a destination at all · **could not measure** never recorded " +
		"or unreachable.\n\n")

	b.WriteString("| # | Destination | Result | Source | Notes |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for i, r := range rows {
		verdict := string(VerdictCouldNotMeasure)
		source := "—"
		notes := "never measured -- no observation recorded for this route yet."
		if r.Known {
			verdict = string(r.Obs.Verdict)
			source = fmt.Sprintf("%s (%s)", r.Obs.Source, r.Obs.Date)
			notes = r.Obs.Notes
		}
		b.WriteString(fmt.Sprintf("| %02d | %s | %s | %s | %s |\n",
			i, r.Entry.Label, verdict, source, notes))
	}

	b.WriteString("\n## Summary\n\n")
	counts := map[Verdict]int{}
	var neverMeasured []string
	for _, r := range rows {
		if !r.Known {
			neverMeasured = append(neverMeasured, r.Entry.Label)
			counts[VerdictCouldNotMeasure]++
			continue
		}
		counts[r.Obs.Verdict]++
	}
	order := []Verdict{VerdictRenders, VerdictStale, VerdictStub, VerdictEmpty, VerdictBroken, VerdictRemoved, VerdictCouldNotMeasure}
	for _, v := range order {
		if counts[v] == 0 && v != VerdictRenders {
			continue
		}
		b.WriteString(fmt.Sprintf("- **%s: %d**\n", v, counts[v]))
	}
	if len(neverMeasured) > 0 {
		b.WriteString("- Never measured: " + strings.Join(neverMeasured, ", ") + "\n")
	}

	return b.String()
}
