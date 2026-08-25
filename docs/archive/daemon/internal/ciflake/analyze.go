package ciflake

import (
	"regexp"
	"sort"
	"time"
)

// ShardStat is one row of the failure-rate table.
type ShardStat struct {
	Shard      int
	Executions int
	Failures   int
	MedianTime time.Duration
	MaxTime    time.Duration
}

// Rate is failures as a fraction of decided executions, 0 when nothing
// was decided (never a divide by zero, and never a fabricated 0% for a
// shard that simply has no data -- Executions == 0 is the signal for
// that, and the report prints it).
func (s ShardStat) Rate() float64 {
	if s.Executions == 0 {
		return 0
	}
	return float64(s.Failures) / float64(s.Executions)
}

// ShardStats computes the failure-rate table, one row per shard seen,
// ordered by shard number.
func ShardStats(execs []Execution) []ShardStat {
	byShard := map[int]*ShardStat{}
	durs := map[int][]time.Duration{}
	for _, e := range execs {
		if !e.Decided() {
			continue
		}
		s, ok := byShard[e.Shard]
		if !ok {
			s = &ShardStat{Shard: e.Shard}
			byShard[e.Shard] = s
		}
		s.Executions++
		if e.Failed() {
			s.Failures++
		}
		if d := e.Duration(); d > 0 {
			durs[e.Shard] = append(durs[e.Shard], d)
		}
	}
	var out []ShardStat
	for shard, s := range byShard {
		ds := durs[shard]
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		if len(ds) > 0 {
			s.MedianTime = ds[len(ds)/2]
			s.MaxTime = ds[len(ds)-1]
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Shard < out[j].Shard })
	return out
}

// Cell is one (commit, shard) pair and every verdict recorded for it.
// Executions of the same commit from several runs (a branch push and its
// pull_request run both fire here) count alongside rerun attempts of one
// run: both are the same unmodified tree measured twice, which is the
// only condition under which two verdicts may be compared at all.
type Cell struct {
	Sha        string
	Shard      int
	Branch     string
	Verdicts   []string
	JobURLs    []string
	Executions int
}

// Ambiguous reports whether this cell produced more than one distinct
// verdict for one unmodified commit -- the defect agent-supervisor#461 is
// about. A cell executed once is never ambiguous; it is simply unrepeated,
// and is excluded from the denominator rather than counted as agreement.
func (c Cell) Ambiguous() bool {
	if len(c.Verdicts) < 2 {
		return false
	}
	first := c.Verdicts[0]
	for _, v := range c.Verdicts[1:] {
		if v != first {
			return true
		}
	}
	return false
}

// Cells groups decided executions by (commit, shard).
func Cells(execs []Execution) []Cell {
	type key struct {
		sha   string
		shard int
	}
	byKey := map[key]*Cell{}
	for _, e := range execs {
		if !e.Decided() {
			continue
		}
		k := key{e.Run.HeadSha, e.Shard}
		c, ok := byKey[k]
		if !ok {
			c = &Cell{Sha: e.Run.HeadSha, Shard: e.Shard, Branch: e.Run.HeadBranch}
			byKey[k] = c
		}
		c.Verdicts = append(c.Verdicts, e.Job.Conclusion)
		c.JobURLs = append(c.JobURLs, e.Job.HTMLURL)
		c.Executions++
	}
	var out []Cell
	for _, c := range byKey {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Shard != out[j].Shard {
			return out[i].Shard < out[j].Shard
		}
		return out[i].Sha < out[j].Sha
	})
	return out
}

// AmbiguityStat is one row of the verdict-stability table: of the cells
// for this shard that were executed more than once, how many disagreed.
type AmbiguityStat struct {
	Shard    int
	Repeated int
	Disagree int
}

// Rate is disagreeing cells over repeated cells; 0 when nothing was
// repeated. Repeated == 0 means "no evidence either way", NOT "stable",
// and the report says so rather than printing a reassuring 0%.
func (a AmbiguityStat) Rate() float64 {
	if a.Repeated == 0 {
		return 0
	}
	return float64(a.Disagree) / float64(a.Repeated)
}

// Ambiguity computes the verdict-stability table from cells.
func Ambiguity(cells []Cell) []AmbiguityStat {
	byShard := map[int]*AmbiguityStat{}
	for _, c := range cells {
		if c.Executions < 2 {
			continue
		}
		a, ok := byShard[c.Shard]
		if !ok {
			a = &AmbiguityStat{Shard: c.Shard}
			byShard[c.Shard] = a
		}
		a.Repeated++
		if c.Ambiguous() {
			a.Disagree++
		}
	}
	var out []AmbiguityStat
	for _, a := range byShard {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Shard < out[j].Shard })
	return out
}

// suiteLine matches the unittest subtest header the shell-suite harness
// emits for a failing suite, e.g.
//
//	FAIL: test_shell_suites_pass (...) (suite='test_dispatch.sh')
//
// Parsed from the job log because nothing else in the API says WHICH of a
// shard's ~18 suites failed, and "which suite" is the question that
// separates a shard-level problem from a suite-level one.
var suiteLine = regexp.MustCompile(`^FAIL: .*\(suite='([^']+)'\)`)

// checkLine matches one failing assertion inside a suite -- the shell
// harness's own "  FAIL <what>" line. Two failures of the same suite for
// the same reason and two for different reasons are different findings,
// and only this distinguishes them.
var checkLine = regexp.MustCompile(`^  FAIL (.+)$`)

// SuiteFailure is what one failing job's log says failed.
type SuiteFailure struct {
	Suite  string
	Checks []string
}

// ParseLog extracts the failing suites and their failing checks from one
// job log. Actions log lines are timestamp-prefixed; the timestamp is
// stripped before matching so the patterns above describe what the
// harness prints, not what Actions wraps it in.
func ParseLog(log string) []SuiteFailure {
	var out []SuiteFailure
	bySuite := map[string]int{}
	var current string
	for _, raw := range splitLines(log) {
		line := stripTimestamp(raw)
		if m := suiteLine.FindStringSubmatch(line); m != nil {
			current = m[1]
			if _, seen := bySuite[current]; !seen {
				bySuite[current] = len(out)
				out = append(out, SuiteFailure{Suite: current})
			}
			continue
		}
		if m := checkLine.FindStringSubmatch(line); m != nil && current != "" {
			i := bySuite[current]
			out[i].Checks = append(out[i].Checks, m[1])
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if n := len(line); n > 0 && line[n-1] == '\r' {
				line = line[:n-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// stripTimestamp removes Actions' leading RFC3339 timestamp and the one
// space after it. A line without one is returned unchanged, so the same
// parser works on a log captured by hand or pasted into an issue.
func stripTimestamp(line string) string {
	if len(line) < 20 || line[4] != '-' || line[7] != '-' || line[10] != 'T' {
		return line
	}
	for i := 19; i < len(line) && i < 40; i++ {
		if line[i] == 'Z' {
			if i+1 < len(line) && line[i+1] == ' ' {
				return line[i+2:]
			}
			return line[i+1:]
		}
	}
	return line
}
