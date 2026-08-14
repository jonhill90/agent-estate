package board

import "testing"

func TestFetchIssuesRequestsAllStates(t *testing.T) {
	var gotArgs []string
	run := GitHubRunner(func(args []string) ([]byte, error) {
		gotArgs = args
		return []byte(`[{"number":6,"title":"task board","state":"OPEN","url":"https://github.com/jonhill90/agent-tui/issues/6"}]`), nil
	})
	issues, err := FetchIssues(run, testRepo)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number != 6 {
		t.Fatalf("issues = %+v", issues)
	}
	want := []string{"issue", "list", "--repo", "jonhill90/agent-tui", "--state", "all", "--limit", "1000", "--json", issueFields}
	if len(gotArgs) != len(want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, gotArgs[i], want[i])
		}
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
