package ciflake

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// GH runs `gh` with args and returns its stdout. It is the one adapter
// seam this package has: every test supplies a fake, and nothing else in
// the package knows a subprocess exists. `gh` rather than a hand-rolled
// HTTP client because it is already the authenticated GitHub path
// everywhere in this repository (ci_gate.py, verdict.py, dispatch.sh) --
// this tool inherits whatever auth those use rather than inventing a
// second token story.
type GH func(ctx context.Context, args ...string) ([]byte, error)

// ExecGH is the real GH.
func ExecGH() GH {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		out, err := exec.CommandContext(ctx, "gh", args...).Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
				return nil, fmt.Errorf("gh %v: %v: %s", args, err, ee.Stderr)
			}
			return nil, fmt.Errorf("gh %v: %w", args, err)
		}
		return out, nil
	}
}

// Client pulls the run/job data the tables are computed from.
type Client struct {
	GH       GH
	Repo     string // OWNER/NAME
	Workflow string // workflow file name, e.g. validate.yml
}

// Runs returns the most recent limit workflow runs.
func (c Client) Runs(ctx context.Context, limit int) ([]Run, error) {
	out, err := c.GH(ctx, "run", "list",
		"--repo", c.Repo,
		"--workflow", c.Workflow,
		"-L", strconv.Itoa(limit),
		"--json", "databaseId,headSha,headBranch,conclusion,createdAt,event,url")
	if err != nil {
		return nil, err
	}
	var runs []Run
	if err := json.Unmarshal(out, &runs); err != nil {
		return nil, fmt.Errorf("decode run list: %w", err)
	}
	return runs, nil
}

// Jobs returns every job of a run INCLUDING previous attempts
// (filter=all). The default view returns only the latest attempt, which
// would hide every rerun -- see Job's own doc comment for why that would
// silently understate the one figure this package exists to report.
func (c Client) Jobs(ctx context.Context, runID int64) ([]Job, error) {
	out, err := c.GH(ctx, "api",
		fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100&filter=all", c.Repo, runID))
	if err != nil {
		return nil, err
	}
	var body struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return nil, fmt.Errorf("decode jobs for run %d: %w", runID, err)
	}
	return body.Jobs, nil
}

// JobLog returns one job's log text.
func (c Client) JobLog(ctx context.Context, jobID int64) (string, error) {
	out, err := c.GH(ctx, "api", fmt.Sprintf("repos/%s/actions/jobs/%d/logs", c.Repo, jobID))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Collect pulls runs and their jobs and returns the flattened executions.
func (c Client) Collect(ctx context.Context, limit int) ([]Run, []Execution, error) {
	runs, err := c.Runs(ctx, limit)
	if err != nil {
		return nil, nil, err
	}
	jobsOf := map[int64][]Job{}
	for _, r := range runs {
		jobs, err := c.Jobs(ctx, r.DatabaseID)
		if err != nil {
			return nil, nil, err
		}
		jobsOf[r.DatabaseID] = jobs
	}
	return runs, Executions(runs, jobsOf), nil
}
