// Package mergepr is the merge-time gate for jonhill90/agent-tui,
// modelled directly on agent-supervisor's own working pattern
// (scripts/supervisor/merge-pr.sh + scripts/supervisor/ci_gate.py) per
// agent-tui#109's brief: two confirmed instances of a comment-verdict gate
// merged by its own author, unreviewed, within minutes (skills#255,
// agent-tui#107) mean the gate has to be enforced by the merge path
// itself, not documented as a convention a caller might skip.
//
// This package chains two gates, both fail-closed, before allowing a
// merge:
//
//  1. EvaluateCI -- CI must be green at the PR's CURRENT head SHA, ported
//     from ci_gate.py's own evaluate(): re-fetch headRefOid itself rather
//     than trust a caller-supplied SHA (a stale snapshot would let a since
//     -invalidated green reading through), read SHA-scoped check-runs and
//     commit-status endpoints rather than gh pr view's statusCheckRollup
//     (which carries no SHA a caller can compare), collapse re-runs to
//     latest-per-name the same way gh pr checks and GitHub's own PR UI do,
//     and refuse outright -- never "pass" -- when zero checks are reported
//     at all (absent is not pending).
//  2. internal/prverdict.Resolve -- an independent, current-head,
//     cross-lane APPROVE must be on record as a PR comment (this repo has
//     no ledger to resolve lane identity against tmux, so it trusts the
//     Author-Lane:/Review-Lane: trailers the same way internal/prverdict's
//     own doc comment explains).
//
// Merge (mergepr.go) is the only place that chains the two and calls
// `gh pr merge` -- exactly the role merge-pr.sh plays for agent-supervisor,
// stated the same way in this repo's own AGENTS.md: this is now THE way to
// merge a PR here, not a fourth optional tool alongside `gh pr merge` run
// by hand.
package mergepr

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jonhill90/agent-tui/internal/prverdict"
)

// Runner is the same seam shape as prverdict.Runner and internal/board's
// GitHubRunner -- kept as an alias, not a fresh named type, because this
// package always calls `gh` through the exact same execution path
// prverdict.ExecRunner already provides; a second Runner type here would
// buy nothing but an extra conversion at every call site.
type Runner = prverdict.Runner

// greenConclusions mirrors ci_gate.py's GREEN_CONCLUSIONS exactly:
// "neutral" and "skipped" both count as passing the same way GitHub's own
// PR UI treats them, not just "success".
var greenConclusions = map[string]bool{
	"success": true,
	"neutral": true,
	"skipped": true,
}

// CIDecision is EvaluateCI's whole answer -- mirrors prverdict.Decision's
// own pattern of a typed, exhaustive result rather than a bare bool, so a
// caller can log WHY as easily as WHETHER.
type CIDecision string

const (
	// CIAllow: every check reported for the current head SHA is green.
	CIAllow CIDecision = "allow"
	// CIRefuse: red, pending, missing entirely, or unreadable -- all the
	// same decision, same as ci_gate.py's own "absent is not pending".
	CIRefuse CIDecision = "refuse"
)

// CIResult is EvaluateCI's return value -- SHA is always populated once
// the head commit was successfully resolved, even on a refuse, so a
// caller (Merge) can still report which commit it evaluated.
type CIResult struct {
	Decision CIDecision
	SHA      string
	Reason   string
}

type prHeadView struct {
	HeadRefOid string `json:"headRefOid"`
}

type checkRun struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	HeadSHA     string `json:"head_sha"`
	CompletedAt string `json:"completed_at"`
	StartedAt   string `json:"started_at"`
}

type checkRunsResponse struct {
	CheckRuns []checkRun `json:"check_runs"`
}

type commitStatus struct {
	Context string `json:"context"`
	State   string `json:"state"`
}

type commitStatusResponse struct {
	Statuses []commitStatus `json:"statuses"`
}

// EvaluateCI is the CI gate: green or refuse, for repo#number's CURRENT
// head SHA, re-fetched here rather than accepted as a parameter -- see
// this file's own doc comment for why a caller-supplied SHA would
// reintroduce the bug ci_gate.py's own doc comment names (check green for
// an older commit than head, because a push raced past a stale snapshot).
func EvaluateCI(run Runner, repo string, number int) CIResult {
	sha, err := headSHA(run, repo, number)
	if err != nil {
		return CIResult{Decision: CIRefuse, Reason: fmt.Sprintf("could not read PR head: %s", err)}
	}

	runs, err := fetchCheckRuns(run, repo, sha)
	if err != nil {
		return CIResult{Decision: CIRefuse, SHA: sha, Reason: fmt.Sprintf("could not read checks for %s: %s", sha, err)}
	}
	statuses, err := fetchCommitStatus(run, repo, sha)
	if err != nil {
		return CIResult{Decision: CIRefuse, SHA: sha, Reason: fmt.Sprintf("could not read checks for %s: %s", sha, err)}
	}

	for _, r := range runs {
		if r.HeadSHA != sha {
			return CIResult{
				Decision: CIRefuse,
				SHA:      sha,
				Reason:   fmt.Sprintf("check run %s reports head_sha %q, not PR head %s", r.Name, r.HeadSHA, sha),
			}
		}
	}

	if len(runs) == 0 && len(statuses) == 0 {
		return CIResult{Decision: CIRefuse, SHA: sha, Reason: fmt.Sprintf("no checks reported for %s", sha)}
	}

	var failing []string
	for _, r := range latestPerName(runs) {
		if !(r.Status == "completed" && greenConclusions[r.Conclusion]) {
			failing = append(failing, describeFailingRun(r))
		}
	}
	for _, s := range statuses {
		if s.State != "success" {
			name := s.Context
			if name == "" {
				name = "?"
			}
			failing = append(failing, name)
		}
	}

	if len(failing) > 0 {
		return CIResult{
			Decision: CIRefuse,
			SHA:      sha,
			Reason:   fmt.Sprintf("check(s) not green at %s: %s", sha, joinComma(failing)),
		}
	}

	return CIResult{Decision: CIAllow, SHA: sha, Reason: fmt.Sprintf("all checks green at %s", sha)}
}

func headSHA(run Runner, repo string, number int) (string, error) {
	out, err := run([]string{
		"pr", "view", fmt.Sprintf("%d", number),
		"--repo", repo,
		"--json", "headRefOid",
	})
	if err != nil {
		return "", err
	}
	var view prHeadView
	if err := json.Unmarshal(out, &view); err != nil {
		return "", fmt.Errorf("decode gh pr view: %w", err)
	}
	if view.HeadRefOid == "" {
		return "", fmt.Errorf("gh did not return a head SHA for this PR")
	}
	return view.HeadRefOid, nil
}

func fetchCheckRuns(run Runner, repo, sha string) ([]checkRun, error) {
	out, err := run([]string{
		"api", fmt.Sprintf("repos/%s/commits/%s/check-runs", repo, sha),
	})
	if err != nil {
		return nil, err
	}
	var resp checkRunsResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("decode check-runs: %w", err)
	}
	return resp.CheckRuns, nil
}

func fetchCommitStatus(run Runner, repo, sha string) ([]commitStatus, error) {
	out, err := run([]string{
		"api", fmt.Sprintf("repos/%s/commits/%s/status", repo, sha),
	})
	if err != nil {
		return nil, err
	}
	var resp commitStatusResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("decode commit status: %w", err)
	}
	return resp.Statuses, nil
}

// latestPerName collapses re-runs the same way gh pr checks and GitHub's
// own PR UI do: a re-run of `test` after a flaky failure leaves both the
// old failing run and the new passing one in the API response, and only
// the newest run of each check name counts. Sort key mirrors ci_gate.py's
// _run_sort_key: completed_at, falling back to started_at for a run still
// in flight -- both are ISO-8601 UTC ("...Z"), so lexical order matches
// chronological order without parsing timestamps.
func latestPerName(runs []checkRun) []checkRun {
	latest := map[string]checkRun{}
	for _, r := range runs {
		current, ok := latest[r.Name]
		if !ok || sortKey(r) >= sortKey(current) {
			latest[r.Name] = r
		}
	}
	names := make([]string, 0, len(latest))
	for name := range latest {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]checkRun, 0, len(names))
	for _, name := range names {
		out = append(out, latest[name])
	}
	return out
}

func sortKey(r checkRun) string {
	if r.CompletedAt != "" {
		return r.CompletedAt
	}
	return r.StartedAt
}

// describeFailingRun mirrors ci_gate.py's _describe_failing_run: a merge
// gate that only ever printed "not green" makes `cancelled` (will never go
// green without a re-run) read identically to `in_progress` (may still go
// green on its own) -- whoever reads this refusal needs that distinction
// spelled out even though the decision itself is the same either way.
func describeFailingRun(r checkRun) string {
	if r.Status == "completed" && r.Conclusion == "cancelled" {
		return fmt.Sprintf("%s (cancelled -- will not go green without a re-run)", r.Name)
	}
	if r.Status != "completed" {
		status := r.Status
		if status == "" {
			status = "unknown"
		}
		return fmt.Sprintf("%s (%s -- may still go green)", r.Name, status)
	}
	conclusion := r.Conclusion
	if conclusion == "" {
		conclusion = "unknown"
	}
	return fmt.Sprintf("%s (%s)", r.Name, conclusion)
}

func joinComma(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += item
	}
	return out
}
