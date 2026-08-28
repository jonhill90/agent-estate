package board

import "testing"

// TestFetchIssuesRequestsAllStates is agent-tui#28's REST-migration guard
// for FetchIssues: it must ask `gh api repos/.../issues` (REST core, its
// own separate budget), never `gh issue list` (GraphQL, the budget
// agent-supervisor#144 established as the one that deadlocks dispatch) --
// see FetchPRs' own doc comment (github.go) for why that call site alone
// stays GraphQL.
func TestFetchIssuesRequestsAllStates(t *testing.T) {
	var gotArgs []string
	run := GitHubRunner(func(args []string) ([]byte, error) {
		gotArgs = args
		return []byte(`[{"number":6,"title":"task board","state":"open","html_url":"https://github.com/jonhill90/agent-tui/issues/6"}]`), nil
	})
	issues, err := FetchIssues(run, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number != 6 || issues[0].State != "OPEN" {
		t.Fatalf("issues = %+v", issues)
	}
	want := []string{"api", "repos/jonhill90/agent-tui/issues", "-X", "GET", "--paginate", "-f", "state=all", "-f", "per_page=100"}
	if len(gotArgs) != len(want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, gotArgs[i], want[i])
		}
	}
	for _, a := range gotArgs {
		if a == "issue" || a == "list" {
			t.Fatalf("args %v still shape a GraphQL `gh issue list` call", gotArgs)
		}
	}
}

// TestFetchIssuesSkipsPullRequests: GitHub's REST issues endpoint returns
// pull requests too (a PR is an issue in GitHub's own data model) --
// `gh issue list` used to filter these out for us invisibly; now that
// FetchIssues reads REST directly (see above), it has to do that filtering
// itself, on the one field (pull_request) REST uses to mark the
// difference.
func TestFetchIssuesSkipsPullRequests(t *testing.T) {
	run := GitHubRunner(func(args []string) ([]byte, error) {
		return []byte(`[
			{"number":6,"title":"an issue","state":"open"},
			{"number":7,"title":"a PR, not an issue","state":"open","pull_request":{"url":"https://api.github.com/repos/jonhill90/agent-tui/pulls/7"}}
		]`), nil
	})
	issues, err := FetchIssues(run, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number != 6 {
		t.Fatalf("issues = %+v, want only agent-tui#6 (a real issue, not the PR at agent-tui#7)", issues)
	}
}

func TestFetchPRsDecodesClosingIssues(t *testing.T) {
	run := GitHubRunner(func(args []string) ([]byte, error) {
		return []byte(`[{"number":10,"state":"OPEN","mergeStateStatus":"CONFLICTING","closingIssuesReferences":[{"number":6}]}]`), nil
	})
	prs, err := FetchPRs(run, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || !prs[0].ClosesIssue(6) || prs[0].ClosesIssue(7) {
		t.Fatalf("prs = %+v", prs)
	}
}
