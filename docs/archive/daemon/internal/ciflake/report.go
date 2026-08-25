package ciflake

import (
	"fmt"
	"sort"
	"strings"
)

// Report is everything one measurement window produced. It is rendered as
// Markdown so the output can go straight into a PR body or an issue
// comment -- the artifact agent-supervisor#461 needs is a table someone
// else can check, not a number in a terminal that has to be retyped.
type Report struct {
	Repo     string
	Workflow string
	Runs     int
	From     string
	To       string

	Shards    []ShardStat
	Ambiguity []AmbiguityStat
	Cells     []Cell
	// Suites is failing-suite counts, populated only when logs were
	// fetched. Empty means "not measured", which the renderer says
	// explicitly rather than printing an empty table that reads as "no
	// suite ever failed".
	Suites      map[string]int
	SuiteShards map[string]map[int]int
	LogsRead    int
}

func pct(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

// Markdown renders the whole report.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## `%s` shell-suites flakiness — %d runs\n\n", r.Repo, r.Runs)
	fmt.Fprintf(&b, "Workflow `%s`, window %s → %s.\n\n", r.Workflow, r.From, r.To)

	b.WriteString("### Failure rate per shard\n\n")
	b.WriteString("A high rate here is not the same as flaky: a branch with a real defect fails its shard every attempt.\n\n")
	b.WriteString("| shard | executions | failures | rate | median | max |\n|---|---|---|---|---|---|\n")
	totalExec, totalFail := 0, 0
	for _, s := range r.Shards {
		totalExec += s.Executions
		totalFail += s.Failures
		fmt.Fprintf(&b, "| %d | %d | %d | %s | %s | %s |\n",
			s.Shard, s.Executions, s.Failures, pct(s.Rate()),
			s.MedianTime.Round(1e9), s.MaxTime.Round(1e9))
	}
	rate := 0.0
	if totalExec > 0 {
		rate = float64(totalFail) / float64(totalExec)
	}
	fmt.Fprintf(&b, "| **all** | **%d** | **%d** | **%s** | | |\n\n", totalExec, totalFail, pct(rate))

	b.WriteString("### Verdict stability — the figure #461 is actually about\n\n")
	b.WriteString("Of the (commit, shard) cells executed more than once, how many returned DISAGREEING verdicts for the same unmodified tree.\n\n")
	b.WriteString("| shard | cells run ≥2× | disagreeing | ambiguity |\n|---|---|---|---|\n")
	totalRep, totalDis := 0, 0
	for _, a := range r.Ambiguity {
		totalRep += a.Repeated
		totalDis += a.Disagree
		note := pct(a.Rate())
		if a.Repeated == 0 {
			note = "no repeats — not measured"
		}
		fmt.Fprintf(&b, "| %d | %d | %d | %s |\n", a.Shard, a.Repeated, a.Disagree, note)
	}
	arate := 0.0
	if totalRep > 0 {
		arate = float64(totalDis) / float64(totalRep)
	}
	fmt.Fprintf(&b, "| **all** | **%d** | **%d** | **%s** |\n\n", totalRep, totalDis, pct(arate))

	if amb := r.AmbiguousCells(); len(amb) > 0 {
		b.WriteString("#### Every ambiguous cell\n\n")
		b.WriteString("| shard | commit | branch | verdicts |\n|---|---|---|---|\n")
		for _, c := range amb {
			fmt.Fprintf(&b, "| %d | `%s` | %s | %s |\n",
				c.Shard, short(c.Sha), c.Branch, strings.Join(c.Verdicts, " → "))
		}
		b.WriteString("\n")
	}

	b.WriteString("### Which suites fail\n\n")
	if len(r.Suites) == 0 {
		b.WriteString("Not measured — this report was produced without reading job logs (`-logs=false`).\n\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Read from %d failing job logs.\n\n", r.LogsRead)
	b.WriteString("| suite | failures | shards |\n|---|---|---|\n")
	type row struct {
		suite string
		n     int
	}
	var rows []row
	for s, n := range r.Suites {
		rows = append(rows, row{s, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].suite < rows[j].suite
	})
	for _, x := range rows {
		var shards []int
		for s := range r.SuiteShards[x.suite] {
			shards = append(shards, s)
		}
		sort.Ints(shards)
		fmt.Fprintf(&b, "| `%s` | %d | %v |\n", x.suite, x.n, shards)
	}
	b.WriteString("\n")
	return b.String()
}

// AmbiguousCells returns every cell whose repeated executions disagreed,
// which is the evidence list behind the stability table.
func (r Report) AmbiguousCells() []Cell {
	var out []Cell
	for _, c := range r.Cells {
		if c.Ambiguous() {
			out = append(out, c)
		}
	}
	return out
}

func short(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}
