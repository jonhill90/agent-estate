// Package ciflake measures whether this repository's CI verdicts can be
// trusted, which is a different question from whether CI is green.
//
// agent-supervisor#461 reported that `shell-suites` shard 4 "fails a
// different suite each run on the same commit" and proposed a shard-level
// resource problem as the cause. The premise was measurable and had not
// been measured: the issue reasons from one commit's reruns. This package
// is the instrument that measures it over any window, so a claim about
// flakiness -- including the claim that a later fix helped -- has a number
// behind it and the same counting method on both sides of the change.
//
// It computes two figures that are routinely conflated:
//
//   - FAILURE RATE per shard: how often a shard job failed. High is not
//     the same as flaky -- a branch with a real defect fails its shard
//     every attempt, and that is CI working.
//
//   - AMBIGUITY RATE: of the (commit, shard) cells that were executed more
//     than once, how many produced DISAGREEING verdicts. This is the
//     number #461 is actually about: a cell that says both pass and fail
//     for one unmodified commit makes every verdict from that job cost an
//     investigation, because a regression and an artifact look identical.
//
// A shard can top the first table and be absent from the second (every
// failure reproducible), which is exactly what this repository's own data
// shows for shard 3. Reporting only the first is how "flaky" becomes an
// excuse a real regression can hide behind.
package ciflake

import (
	"regexp"
	"strconv"
	"time"
)

// Run is one workflow run, as `gh run list --json` reports it.
type Run struct {
	DatabaseID int64  `json:"databaseId"`
	HeadSha    string `json:"headSha"`
	HeadBranch string `json:"headBranch"`
	Conclusion string `json:"conclusion"`
	CreatedAt  string `json:"createdAt"`
	Event      string `json:"event"`
	URL        string `json:"url"`
}

// Job is one job inside a run, as the REST jobs endpoint reports it. The
// endpoint must be queried with filter=all: its default returns only the
// LATEST attempt, which silently drops every rerun -- and a rerun is the
// only place a disagreeing verdict can be seen. Measuring flakiness off
// the default view reports a repository as stabler than it is, and this
// package's whole subject is reruns.
type Job struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Conclusion  string   `json:"conclusion"`
	Status      string   `json:"status"`
	StartedAt   string   `json:"started_at"`
	CompletedAt string   `json:"completed_at"`
	RunAttempt  int      `json:"run_attempt"`
	RunnerName  string   `json:"runner_name"`
	Labels      []string `json:"labels"`
	HTMLURL     string   `json:"html_url"`
}

// Execution is one job joined to the run it belongs to -- the unit every
// figure in this package is computed over.
type Execution struct {
	Run   Run
	Job   Job
	Shard int
}

// shardName matches the matrix job's rendered name, e.g.
// "shell-suites (shard 4)". The shard number is parsed from the NAME
// rather than from the matrix input because that is all the jobs endpoint
// reports; if the workflow's `name:` changes, this stops matching and the
// tool reports zero executions rather than silently mis-grouping them --
// visible, which a wrong grouping would not be.
var shardName = regexp.MustCompile(`^shell-suites \(shard (\d+)\)$`)

// ShardOf returns the shard number for a job name and whether it is a
// shard job at all.
func ShardOf(jobName string) (int, bool) {
	m := shardName.FindStringSubmatch(jobName)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// Duration is the job's wall-clock time, or 0 when either timestamp is
// missing (a job that never started, or is still running).
func (e Execution) Duration() time.Duration {
	const layout = "2006-01-02T15:04:05Z"
	start, err1 := time.Parse(layout, e.Job.StartedAt)
	end, err2 := time.Parse(layout, e.Job.CompletedAt)
	if err1 != nil || err2 != nil {
		return 0
	}
	return end.Sub(start)
}

// Failed reports whether this execution is a red verdict. Only "failure"
// counts: a cancelled or skipped job is not evidence about the code, and
// counting it as failure would inflate exactly the rate this package
// exists to state honestly.
func (e Execution) Failed() bool { return e.Job.Conclusion == "failure" }

// Decided reports whether this execution produced a verdict at all --
// success or failure. Anything else (cancelled, skipped, still running)
// is excluded from both tables rather than guessed at.
func (e Execution) Decided() bool {
	return e.Job.Conclusion == "success" || e.Job.Conclusion == "failure"
}

// Executions flattens runs and their jobs into shard executions, dropping
// every job that is not a shard job.
func Executions(runs []Run, jobsOf map[int64][]Job) []Execution {
	var out []Execution
	for _, r := range runs {
		for _, j := range jobsOf[r.DatabaseID] {
			shard, ok := ShardOf(j.Name)
			if !ok {
				continue
			}
			out = append(out, Execution{Run: r, Job: j, Shard: shard})
		}
	}
	return out
}
