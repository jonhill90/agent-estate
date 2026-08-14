package board

import (
	"encoding/json"
	"fmt"
)

// Issue is one row of `gh issue list --json
// number,title,state,url,createdAt,updatedAt,closedAt`.
type Issue struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"` // OPEN or CLOSED
	URL       string `json:"url"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	ClosedAt  string `json:"closedAt"` // empty while OPEN
}

// closingRef is one entry of a PR's closingIssuesReferences -- the field
// GitHub itself resolves from a PR body's "Fixes #N" (or equivalent), so
// this board never has to parse PR bodies for the issue<->PR link.
type closingRef struct {
	Number int `json:"number"`
}

// PR is one row of `gh pr list --json
// number,title,state,url,createdAt,updatedAt,closedAt,mergedAt,mergeStateStatus,closingIssuesReferences,headRefName`.
type PR struct {
	Number           int          `json:"number"`
	Title            string       `json:"title"`
	State            string       `json:"state"` // OPEN, CLOSED, or MERGED
	URL              string       `json:"url"`
	CreatedAt        string       `json:"createdAt"`
	UpdatedAt        string       `json:"updatedAt"`
	ClosedAt         string       `json:"closedAt"`         // set for CLOSED and MERGED
	MergedAt         string       `json:"mergedAt"`         // set only for MERGED
	MergeStateStatus string       `json:"mergeStateStatus"` // e.g. CONFLICTING, CLEAN, UNKNOWN
	HeadRefName      string       `json:"headRefName"`
	ClosingIssues    []closingRef `json:"closingIssuesReferences"`
}

// ClosesIssue reports whether this PR's own closingIssuesReferences names
// the given issue number in the given repo. GitHub only resolves that
// field for references within the SAME repo, so repo isn't part of the
// match -- the caller (card.go) has already scoped prs to one repo before
// calling this.
func (p PR) ClosesIssue(number int) bool {
	for _, ref := range p.ClosingIssues {
		if ref.Number == number {
			return true
		}
	}
	return false
}

// issueFields/prFields are the exact --json field lists this board asks
// `gh` for -- named once here so FetchIssues/FetchPRs and any fixture in
// board_test.go stay honest about what's actually requested.
var (
	issueFields = "number,title,state,url,createdAt,updatedAt,closedAt"
	prFields    = "number,title,state,url,createdAt,updatedAt,closedAt,mergedAt,mergeStateStatus,headRefName,closingIssuesReferences"
)

// GitHubRunner executes `gh` with the given args and returns its stdout.
type GitHubRunner func(args []string) ([]byte, error)

// FetchIssues lists every issue (open and closed) in repo -- both are
// needed: Backlog and In-progress cards come from open issues, Done cards
// from closed ones. --limit 1000 matches reconcile_sources.py's own choice
// for the identical reason (module docstring): "a repo with more than
// --limit silently omits the oldest", stated once rather than re-derived.
func FetchIssues(run GitHubRunner, repo Repo) ([]Issue, error) {
	out, err := run([]string{
		"issue", "list", "--repo", repo.GitHubID(),
		"--state", "all", "--limit", "1000", "--json", issueFields,
	})
	if err != nil {
		return nil, fmt.Errorf("board: gh issue list %s: %w", repo.GitHubID(), err)
	}
	var issues []Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("board: decode issues for %s: %w", repo.GitHubID(), err)
	}
	return issues, nil
}

// FetchPRs lists every PR (open, closed, and merged) in repo.
func FetchPRs(run GitHubRunner, repo Repo) ([]PR, error) {
	out, err := run([]string{
		"pr", "list", "--repo", repo.GitHubID(),
		"--state", "all", "--limit", "1000", "--json", prFields,
	})
	if err != nil {
		return nil, fmt.Errorf("board: gh pr list %s: %w", repo.GitHubID(), err)
	}
	var prs []PR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("board: decode PRs for %s: %w", repo.GitHubID(), err)
	}
	return prs, nil
}
