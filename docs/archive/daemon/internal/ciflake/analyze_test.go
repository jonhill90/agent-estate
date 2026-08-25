package ciflake

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func job(id int64, shard int, conclusion string, attempt int) Job {
	return Job{
		ID:          id,
		Name:        fmt.Sprintf("shell-suites (shard %d)", shard),
		Conclusion:  conclusion,
		Status:      "completed",
		StartedAt:   "2026-08-22T20:00:00Z",
		CompletedAt: "2026-08-22T20:05:00Z",
		RunAttempt:  attempt,
		HTMLURL:     fmt.Sprintf("https://example.invalid/job/%d", id),
	}
}

func TestShardOfIgnoresEveryOtherJob(t *testing.T) {
	if s, ok := ShardOf("shell-suites (shard 4)"); !ok || s != 4 {
		t.Errorf("ShardOf(shard 4) = %d, %v", s, ok)
	}
	for _, name := range []string{"unit-tests", "plan-shell-shards", "shell-suites", "ui-evidence"} {
		if _, ok := ShardOf(name); ok {
			t.Errorf("ShardOf(%q) matched a non-shard job", name)
		}
	}
}

// TestFailureRateCountsOnlyDecidedJobs: a cancelled job says nothing
// about the code, and counting it as a failure would inflate the very
// rate this package exists to state honestly.
func TestFailureRateCountsOnlyDecidedJobs(t *testing.T) {
	runs := []Run{{DatabaseID: 1, HeadSha: "aaa"}}
	jobs := map[int64][]Job{1: {
		job(10, 0, "success", 1),
		job(11, 0, "failure", 1),
		job(12, 0, "cancelled", 1),
		job(13, 0, "skipped", 1),
	}}
	stats := ShardStats(Executions(runs, jobs))
	if len(stats) != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats[0].Executions != 2 || stats[0].Failures != 1 {
		t.Errorf("stats = %+v, want 2 executions / 1 failure", stats[0])
	}
	if got := stats[0].Rate(); got != 0.5 {
		t.Errorf("Rate() = %v, want 0.5", got)
	}
}

// TestAmbiguityNeedsARepeat is the distinction the whole package turns
// on: a cell that failed once and was never re-run is a red verdict, not
// an ambiguous one, and must not be counted in either column.
func TestAmbiguityNeedsARepeat(t *testing.T) {
	runs := []Run{{DatabaseID: 1, HeadSha: "aaa", HeadBranch: "b"}}
	jobs := map[int64][]Job{1: {job(10, 0, "failure", 1)}}
	amb := Ambiguity(Cells(Executions(runs, jobs)))
	if len(amb) != 0 {
		t.Fatalf("a single unrepeated failure produced an ambiguity row: %+v", amb)
	}
}

// TestAmbiguityIsDisagreementNotFailure: a cell that failed twice is
// unambiguous -- it reproduced. That is CI working, and calling it flaky
// is how a real regression gets waved through.
func TestAmbiguityIsDisagreementNotFailure(t *testing.T) {
	runs := []Run{{DatabaseID: 1, HeadSha: "aaa", HeadBranch: "b"}}
	jobs := map[int64][]Job{1: {job(10, 3, "failure", 1), job(11, 3, "failure", 2)}}
	amb := Ambiguity(Cells(Executions(runs, jobs)))
	if len(amb) != 1 || amb[0].Repeated != 1 || amb[0].Disagree != 0 {
		t.Fatalf("amb = %+v, want 1 repeated cell and 0 disagreements", amb)
	}
	if got := amb[0].Rate(); got != 0 {
		t.Errorf("Rate() = %v, want 0", got)
	}
}

func TestAmbiguityCatchesADisagreeingCell(t *testing.T) {
	runs := []Run{{DatabaseID: 1, HeadSha: "aaa", HeadBranch: "b"}}
	jobs := map[int64][]Job{1: {job(10, 4, "failure", 1), job(11, 4, "success", 2)}}
	cells := Cells(Executions(runs, jobs))
	if len(cells) != 1 || !cells[0].Ambiguous() {
		t.Fatalf("cells = %+v, want one ambiguous cell", cells)
	}
	amb := Ambiguity(cells)
	if amb[0].Disagree != 1 || amb[0].Rate() != 1 {
		t.Errorf("amb = %+v", amb[0])
	}
}

// TestCellsJoinSeparateRunsOfOneCommit: this repo's Validate workflow
// fires on both push and pull_request, so one commit is commonly measured
// by two runs. Those two verdicts are as comparable as two attempts of
// one run -- same tree, measured twice -- and grouping only by run would
// miss the disagreement entirely.
func TestCellsJoinSeparateRunsOfOneCommit(t *testing.T) {
	runs := []Run{
		{DatabaseID: 1, HeadSha: "aaa", HeadBranch: "b", Event: "push"},
		{DatabaseID: 2, HeadSha: "aaa", HeadBranch: "b", Event: "pull_request"},
	}
	jobs := map[int64][]Job{
		1: {job(10, 4, "success", 1)},
		2: {job(20, 4, "failure", 1)},
	}
	cells := Cells(Executions(runs, jobs))
	if len(cells) != 1 {
		t.Fatalf("cells = %+v, want the two runs joined into one cell", cells)
	}
	if !cells[0].Ambiguous() {
		t.Errorf("cell across two runs of one commit not reported ambiguous: %+v", cells[0])
	}
}

// TestDifferentCommitsNeverShareACell: two commits disagreeing is just
// two commits. Only an unmodified tree measured twice can be ambiguous.
func TestDifferentCommitsNeverShareACell(t *testing.T) {
	runs := []Run{
		{DatabaseID: 1, HeadSha: "aaa"},
		{DatabaseID: 2, HeadSha: "bbb"},
	}
	jobs := map[int64][]Job{1: {job(10, 4, "success", 1)}, 2: {job(20, 4, "failure", 1)}}
	for _, c := range Cells(Executions(runs, jobs)) {
		if c.Ambiguous() {
			t.Errorf("two different commits were treated as one cell: %+v", c)
		}
	}
}

const failLog = `2026-08-22T20:43:14.2113382Z   test_shell_suites_pass (tests.supervisor.test_shell_suites.ShellSuites.test_shell_suites_pass) (suite='test_watchdog_poller_copy.sh') ... FAIL
2026-08-22T20:44:45.1514292Z FAIL: test_shell_suites_pass (tests.supervisor.test_shell_suites.ShellSuites.test_shell_suites_pass) (suite='test_watchdog_poller_copy.sh')
2026-08-22T20:44:45.1522406Z AssertionError: 1 != 0 : test_watchdog_poller_copy.sh failed:
2026-08-22T20:44:45.1536585Z   FAIL watchdog.sh's copy path relaunches the poller and a different live pid appears within seconds
2026-08-22T20:44:45.1548658Z   FAIL exactly one live poller and one inbox-poll window after the relaunch
2026-08-22T20:44:45.1555988Z FAILED (failures=1)
`

func TestParseLogNamesTheSuiteAndItsChecks(t *testing.T) {
	got := ParseLog(failLog)
	if len(got) != 1 || got[0].Suite != "test_watchdog_poller_copy.sh" {
		t.Fatalf("ParseLog = %+v", got)
	}
	if len(got[0].Checks) != 2 {
		t.Fatalf("checks = %v, want the two FAIL lines", got[0].Checks)
	}
	if !strings.Contains(got[0].Checks[0], "relaunches the poller") {
		t.Errorf("first check = %q", got[0].Checks[0])
	}
}

// TestParseLogWorksWithoutActionsTimestamps: the same parser has to work
// on a log pasted into an issue by hand, which is how most of this
// evidence gets quoted.
func TestParseLogWorksWithoutActionsTimestamps(t *testing.T) {
	plain := "FAIL: x (suite='test_dispatch.sh')\n  FAIL blind Enter commits the pending option\n"
	got := ParseLog(plain)
	if len(got) != 1 || got[0].Suite != "test_dispatch.sh" || len(got[0].Checks) != 1 {
		t.Fatalf("ParseLog(plain) = %+v", got)
	}
}

func TestParseLogOnAPassingLogFindsNothing(t *testing.T) {
	if got := ParseLog("2026-08-22T20:00:00.0000000Z OK\n2026-08-22T20:00:01.0000000Z ok 89 suites\n"); len(got) != 0 {
		t.Fatalf("ParseLog(passing) = %+v, want nothing", got)
	}
}

// TestJobsRequestsEveryAttempt is the one API detail this measurement
// cannot get wrong: the jobs endpoint defaults to the LATEST attempt
// only. Measuring off that default hides every rerun, and a rerun is the
// only place a disagreeing verdict can appear -- the tool would report a
// repository as perfectly stable precisely because it is flaky enough to
// be re-run. Pinned here rather than trusted to a comment.
func TestJobsRequestsEveryAttempt(t *testing.T) {
	var seen []string
	c := Client{
		Repo:     "o/r",
		Workflow: "validate.yml",
		GH: func(_ context.Context, args ...string) ([]byte, error) {
			seen = append(seen, strings.Join(args, " "))
			return json.Marshal(map[string]any{"jobs": []Job{job(1, 0, "success", 1)}})
		},
	}
	if _, err := c.Jobs(context.Background(), 42); err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "filter=all") {
		t.Fatalf("jobs call was %q, want filter=all so reruns are included", seen)
	}
	if !strings.Contains(seen[0], "repos/o/r/actions/runs/42/jobs") {
		t.Errorf("jobs call addressed the wrong endpoint: %q", seen[0])
	}
}

func TestCollectFlattensRunsAndJobs(t *testing.T) {
	runs := []Run{{DatabaseID: 7, HeadSha: "aaa", HeadBranch: "b"}}
	c := Client{
		Repo:     "o/r",
		Workflow: "validate.yml",
		GH: func(_ context.Context, args ...string) ([]byte, error) {
			if args[0] == "run" {
				return json.Marshal(runs)
			}
			return json.Marshal(map[string]any{"jobs": []Job{
				job(1, 0, "success", 1),
				{ID: 2, Name: "unit-tests", Conclusion: "success", Status: "completed"},
			}})
		},
	}
	gotRuns, execs, err := c.Collect(context.Background(), 10)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(gotRuns) != 1 {
		t.Fatalf("runs = %+v", gotRuns)
	}
	if len(execs) != 1 || execs[0].Shard != 0 {
		t.Fatalf("execs = %+v, want just the shard job", execs)
	}
}

// TestMarkdownSaysWhenSuitesWereNotMeasured: an empty suite table must
// not read as "no suite ever failed" -- the failure mode this repository
// produces most (an instrument that cannot see a thing looks exactly like
// the thing being absent).
func TestMarkdownSaysWhenSuitesWereNotMeasured(t *testing.T) {
	runs := []Run{{DatabaseID: 1, HeadSha: "aaa", HeadBranch: "b"}}
	execs := Executions(runs, map[int64][]Job{1: {job(10, 4, "failure", 1), job(11, 4, "success", 2)}})
	cells := Cells(execs)
	out := Report{
		Repo: "o/r", Workflow: "validate.yml", Runs: 1,
		Shards: ShardStats(execs), Ambiguity: Ambiguity(cells), Cells: cells,
	}.Markdown()

	if !strings.Contains(out, "Not measured") {
		t.Errorf("Markdown() prints an empty suite table with no caveat:\n%s", out)
	}
	if !strings.Contains(out, "failure → success") {
		t.Errorf("Markdown() does not list the ambiguous cell:\n%s", out)
	}
	if !strings.Contains(out, "| 4 | 1 | 1 | 100.0% |") {
		t.Errorf("Markdown() stability row missing:\n%s", out)
	}
}

// TestMarkdownDistinguishesNoRepeatsFromStable: a shard nobody re-ran has
// no evidence either way, and printing 0.0% for it would be a confident
// claim built on nothing.
func TestMarkdownDistinguishesNoRepeatsFromStable(t *testing.T) {
	out := Report{Repo: "o/r", Ambiguity: []AmbiguityStat{{Shard: 1, Repeated: 0}}}.Markdown()
	if !strings.Contains(out, "no repeats — not measured") {
		t.Errorf("Markdown() reports an unmeasured shard as stable:\n%s", out)
	}
}
