package mergepr

import (
	"fmt"
	"strings"
	"testing"
)

const sha = "cccccccccccccccccccccccccccccccccccccc"

// fixtureRunner replays canned `gh` responses keyed on the joined args --
// same fixture pattern internal/board's own tests use for GitHubRunner, no
// real subprocess involved.
func fixtureRunner(t *testing.T, responses map[string]string, errs map[string]error) Runner {
	t.Helper()
	return func(args []string) ([]byte, error) {
		key := strings.Join(args, " ")
		if err, ok := errs[key]; ok {
			return nil, err
		}
		resp, ok := responses[key]
		if !ok {
			t.Fatalf("fixtureRunner: no response stubbed for %q", key)
		}
		return []byte(resp), nil
	}
}

func headKey(repo string, number int) string {
	return fmt.Sprintf("pr view %d --repo %s --json headRefOid", number, repo)
}

func checkRunsKey(repo, sha string) string {
	return fmt.Sprintf("api repos/%s/commits/%s/check-runs", repo, sha)
}

func statusKey(repo, sha string) string {
	return fmt.Sprintf("api repos/%s/commits/%s/status", repo, sha)
}

func TestEvaluateCIGreenAllows(t *testing.T) {
	repo, number := "jonhill90/agent-tui", 1
	responses := map[string]string{
		headKey(repo, number):   fmt.Sprintf(`{"headRefOid":%q}`, sha),
		checkRunsKey(repo, sha): fmt.Sprintf(`{"check_runs":[{"name":"build","status":"completed","conclusion":"success","head_sha":%q,"completed_at":"2026-08-23T00:00:00Z"}]}`, sha),
		statusKey(repo, sha):    `{"statuses":[]}`,
	}
	got := EvaluateCI(fixtureRunner(t, responses, nil), repo, number)
	if got.Decision != CIAllow {
		t.Fatalf("decision = %q, want allow (reason: %s)", got.Decision, got.Reason)
	}
	if got.SHA != sha {
		t.Fatalf("sha = %q, want %q", got.SHA, sha)
	}
}

func TestEvaluateCIRedRefuses(t *testing.T) {
	repo, number := "jonhill90/agent-tui", 1
	responses := map[string]string{
		headKey(repo, number):   fmt.Sprintf(`{"headRefOid":%q}`, sha),
		checkRunsKey(repo, sha): fmt.Sprintf(`{"check_runs":[{"name":"build","status":"completed","conclusion":"failure","head_sha":%q,"completed_at":"2026-08-23T00:00:00Z"}]}`, sha),
		statusKey(repo, sha):    `{"statuses":[]}`,
	}
	got := EvaluateCI(fixtureRunner(t, responses, nil), repo, number)
	if got.Decision != CIRefuse {
		t.Fatalf("decision = %q, want refuse", got.Decision)
	}
	if !strings.Contains(got.Reason, "build") {
		t.Fatalf("reason = %q, want it to name the failing check", got.Reason)
	}
}

func TestEvaluateCINoChecksRefuses(t *testing.T) {
	repo, number := "jonhill90/agent-tui", 1
	responses := map[string]string{
		headKey(repo, number):   fmt.Sprintf(`{"headRefOid":%q}`, sha),
		checkRunsKey(repo, sha): `{"check_runs":[]}`,
		statusKey(repo, sha):    `{"statuses":[]}`,
	}
	got := EvaluateCI(fixtureRunner(t, responses, nil), repo, number)
	if got.Decision != CIRefuse {
		t.Fatalf("decision = %q, want refuse (absent is not pending)", got.Decision)
	}
}

func TestEvaluateCIRerunUsesLatestPerName(t *testing.T) {
	repo, number := "jonhill90/agent-tui", 1
	responses := map[string]string{
		headKey(repo, number): fmt.Sprintf(`{"headRefOid":%q}`, sha),
		checkRunsKey(repo, sha): fmt.Sprintf(`{"check_runs":[
			{"name":"build","status":"completed","conclusion":"failure","head_sha":%q,"completed_at":"2026-08-23T00:00:00Z"},
			{"name":"build","status":"completed","conclusion":"success","head_sha":%q,"completed_at":"2026-08-23T00:05:00Z"}
		]}`, sha, sha),
		statusKey(repo, sha): `{"statuses":[]}`,
	}
	got := EvaluateCI(fixtureRunner(t, responses, nil), repo, number)
	if got.Decision != CIAllow {
		t.Fatalf("decision = %q, want allow -- newest run of `build` succeeded (reason: %s)", got.Decision, got.Reason)
	}
}

func TestEvaluateCIStaleHeadSHAOnCheckRunRefuses(t *testing.T) {
	repo, number := "jonhill90/agent-tui", 1
	other := "dddddddddddddddddddddddddddddddddddddddd"
	responses := map[string]string{
		headKey(repo, number):   fmt.Sprintf(`{"headRefOid":%q}`, sha),
		checkRunsKey(repo, sha): fmt.Sprintf(`{"check_runs":[{"name":"build","status":"completed","conclusion":"success","head_sha":%q,"completed_at":"2026-08-23T00:00:00Z"}]}`, other),
		statusKey(repo, sha):    `{"statuses":[]}`,
	}
	got := EvaluateCI(fixtureRunner(t, responses, nil), repo, number)
	if got.Decision != CIRefuse {
		t.Fatalf("decision = %q, want refuse on a head_sha mismatch", got.Decision)
	}
}
