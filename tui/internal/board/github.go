package board

import (
	"encoding/json"
	"fmt"
	"strings"
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

// prFields is the exact --json field list this board asks `gh pr list` for
// -- named here so FetchPRs and any fixture in board_test.go stay honest
// about what's actually requested.
//
// FetchPRs stays on `gh pr list` (GraphQL-backed) rather than following
// FetchIssues to REST (below): closingIssuesReferences -- how Derive
// (card.go) links a PR back to the issue it closes, without ever parsing a
// PR body itself -- has no REST list-endpoint equivalent, and neither does
// mergeStateStatus (REST only exposes mergeable_state on a single-PR GET,
// one call per PR, which would trade one GraphQL call per repo for N REST
// calls per repo -- worse, not better). agent-tui#28's brief warns
// specifically against "swap one GraphQL call for another and call it
// done"; reimplementing GitHub's own closing-keyword resolution here would
// be swapping a GraphQL call for a correctness risk instead, on the one
// field (issue<->PR linkage) this board can least afford to get wrong.
// #28's rate fix for this call site is therefore the refresh interval
// (model.go's DefaultRefreshInterval / -board-refresh), not a transport
// change.
var prFields = "number,title,state,url,createdAt,updatedAt,closedAt,mergedAt,mergeStateStatus,headRefName,closingIssuesReferences"

// GitHubRunner executes `gh` with the given args and returns its stdout.
type GitHubRunner func(args []string) ([]byte, error)

// restIssue is one row of `gh api repos/{owner}/{repo}/issues` -- REST
// core, on its own 5,000/hr budget separate from `gh issue list`/`gh pr
// list`'s GraphQL budget (agent-supervisor#144). Every field FetchIssues
// used from the GraphQL shape has a plain REST equivalent; PullRequest is
// the one extra field this shape needs that Issue doesn't: GitHub's
// REST issues endpoint returns pull requests too (a PR *is* an issue in
// GitHub's own data model), distinguishable only by this field's presence,
// so FetchIssues below filters PRs out itself rather than trust `gh issue
// list --state all` to have already done it (which is who did this
// filtering before this switch, invisibly).
type restIssue struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	State       string `json:"state"` // "open" or "closed", lowercase -- REST's own casing
	HTMLURL     string `json:"html_url"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	ClosedAt    string `json:"closed_at"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request,omitempty"`
}

// FetchIssues lists every issue (open and closed) in repo -- both are
// needed: Backlog and In-progress cards come from open issues, Done cards
// from closed ones. agent-tui#28: this call site needs nothing GraphQL-only
// (unlike FetchPRs, see prFields' doc comment above), so it reads REST core
// via `gh api` instead of `gh issue list` -- REST core sits on its own
// separate 5,000/hr budget from the GraphQL one `gh issue list`/`gh pr
// list` share, so this call site alone no longer counts against the budget
// agent-supervisor#144 established as the one that deadlocks dispatch.
// --paginate follows every page itself (still REST core, still cheap) --
// the previous `gh issue list --limit 1000` cap is dropped rather than
// reproduced, since --paginate has no equivalent silent truncation to
// guard against.
func FetchIssues(run GitHubRunner, repo Repo) ([]Issue, error) {
	out, err := run([]string{
		"api", "repos/" + repo.GitHubID() + "/issues",
		"--paginate", "-f", "state=all", "-f", "per_page=100",
	})
	if err != nil {
		return nil, fmt.Errorf("board: gh api repos/%s/issues: %w", repo.GitHubID(), err)
	}
	var raw []restIssue
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("board: decode issues for %s: %w", repo.GitHubID(), err)
	}
	issues := make([]Issue, 0, len(raw))
	for _, r := range raw {
		if r.PullRequest != nil {
			continue // a PR, not an issue -- see restIssue's doc comment
		}
		issues = append(issues, Issue{
			Number:    r.Number,
			Title:     r.Title,
			State:     strings.ToUpper(r.State), // normalized to the "OPEN"/"CLOSED" every other reader of Issue.State (card.go) already expects
			URL:       r.HTMLURL,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
			ClosedAt:  r.ClosedAt,
		})
	}
	return issues, nil
}

// FetchPRs lists every PR (open, closed, and merged) in repo. See prFields'
// doc comment for why this call site is still `gh pr list` (GraphQL), not
// REST.
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
