// Command ciflake measures how often this repository's shell-suites
// shards fail, and -- separately -- how often they return DISAGREEING
// verdicts for one unmodified commit. See internal/ciflake's package doc
// comment for why those are different questions and why reporting only
// the first is what let agent-supervisor#461 be argued from one commit's
// reruns.
//
// Output is Markdown, so a measurement can be pasted into an issue or a
// PR body unchanged:
//
//	go run ./cmd/ciflake -runs 120 > /tmp/ci.md
//	go run ./cmd/ciflake -runs 40 -logs=false      # skip log reads (fast)
//
// Reading logs costs one API call per FAILING job, not per job.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"agent-supervisor/daemon/internal/ciflake"
)

func main() {
	var (
		repo     = flag.String("repo", "jonhill90/agent-supervisor", "OWNER/NAME to measure")
		workflow = flag.String("workflow", "validate.yml", "workflow file the shards live in")
		runs     = flag.Int("runs", 60, "how many recent runs to pull")
		logs     = flag.Bool("logs", true, "read failing jobs' logs to attribute failures to suites")
		timeout  = flag.Duration("timeout", 15*time.Minute, "overall deadline")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	c := ciflake.Client{GH: ciflake.ExecGH(), Repo: *repo, Workflow: *workflow}
	runList, execs, err := c.Collect(ctx, *runs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciflake:", err)
		os.Exit(1)
	}
	if len(execs) == 0 {
		// Not "CI is clean" -- nothing matched the shard job name at all.
		// Said out loud, because an instrument that cannot see a thing
		// looks exactly like the thing being absent.
		fmt.Fprintf(os.Stderr, "ciflake: no shell-suites shard jobs found in %d runs of %s -- "+
			"has the job's name: changed?\n", len(runList), *workflow)
		os.Exit(2)
	}

	cells := ciflake.Cells(execs)
	rep := ciflake.Report{
		Repo:      *repo,
		Workflow:  *workflow,
		Runs:      len(runList),
		Shards:    ciflake.ShardStats(execs),
		Ambiguity: ciflake.Ambiguity(cells),
		Cells:     cells,
	}
	created := make([]string, 0, len(runList))
	for _, r := range runList {
		created = append(created, r.CreatedAt)
	}
	sort.Strings(created)
	if len(created) > 0 {
		rep.From, rep.To = created[0], created[len(created)-1]
	}

	if *logs {
		rep.Suites = map[string]int{}
		rep.SuiteShards = map[string]map[int]int{}
		for _, e := range execs {
			if !e.Failed() {
				continue
			}
			log, err := c.JobLog(ctx, e.Job.ID)
			if err != nil {
				// A log that expired or could not be read is reported, not
				// counted as "no suite failed" -- the count below would
				// otherwise quietly understate the suite it belonged to.
				fmt.Fprintf(os.Stderr, "ciflake: job %d log unavailable: %v\n", e.Job.ID, err)
				continue
			}
			rep.LogsRead++
			for _, sf := range ciflake.ParseLog(log) {
				rep.Suites[sf.Suite]++
				if rep.SuiteShards[sf.Suite] == nil {
					rep.SuiteShards[sf.Suite] = map[int]int{}
				}
				rep.SuiteShards[sf.Suite][e.Shard]++
			}
		}
	}

	fmt.Print(rep.Markdown())
}
