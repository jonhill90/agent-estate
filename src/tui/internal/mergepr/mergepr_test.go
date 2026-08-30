package mergepr

import (
	"fmt"
	"strings"
	"testing"
)

func viewKey(repo string, number int, fields string) string {
	return fmt.Sprintf("pr view %d --repo %s --json %s", number, repo, fields)
}

// greenCI stubs a repo/number that reads as CI-green at sha, for tests
// whose focus is the verdict gate, not the CI gate.
func greenCI(repo string, number int, sha string, responses map[string]string) {
	responses[headKey(repo, number)] = fmt.Sprintf(`{"headRefOid":%q}`, sha)
	responses[checkRunsKey(repo, sha)] = fmt.Sprintf(`{"check_runs":[{"name":"build","status":"completed","conclusion":"success","head_sha":%q,"completed_at":"2026-08-23T00:00:00Z"}]}`, sha)
	responses[statusKey(repo, sha)] = `{"statuses":[]}`
}

func verdictView(repo string, number int, body string, sha string, comments string) string {
	responses := map[string]string{}
	_ = responses
	return fmt.Sprintf(`{"body":%q,"headRefOid":%q,"comments":%s}`, body, sha, comments)
}

func commentJSON(login, body string) string {
	return fmt.Sprintf(`{"author":{"login":%q},"body":%q}`, login, body)
}

// The four directions the brief names, exercised at the full Evaluate
// level (CI gate + verdict gate chained), mirroring
// internal/prverdict's own TestMutationCheckFourDirections but through
// this package's public entry point rather than prverdict.Resolve
// directly -- this is the seam a real `mergepr` invocation actually goes
// through.
func TestEvaluateFourDirections(t *testing.T) {
	repo, number := "jonhill90/agent-tui", 1
	fields := "body,headRefOid,comments"

	t.Run("1_ci_red_refuses", func(t *testing.T) {
		responses := map[string]string{
			headKey(repo, number):   fmt.Sprintf(`{"headRefOid":%q}`, sha),
			checkRunsKey(repo, sha): fmt.Sprintf(`{"check_runs":[{"name":"build","status":"completed","conclusion":"failure","head_sha":%q,"completed_at":"2026-08-23T00:00:00Z"}]}`, sha),
			statusKey(repo, sha):    `{"statuses":[]}`,
		}
		got := Evaluate(fixtureRunner(t, responses, nil), repo, number)
		if got.Decision != MergeRefuse {
			t.Fatalf("decision = %q, want refuse", got.Decision)
		}
		if !strings.Contains(got.Reason, "CI gate refused") {
			t.Fatalf("reason = %q, want it to name the CI gate", got.Reason)
		}
	})

	t.Run("2_ci_green_no_verdict_refuses", func(t *testing.T) {
		responses := map[string]string{}
		greenCI(repo, number, sha, responses)
		responses[viewKey(repo, number, fields)] = verdictView(repo, number, "Author-Lane: build-2\n", sha, "[]")
		got := Evaluate(fixtureRunner(t, responses, nil), repo, number)
		if got.Decision != MergeRefuse {
			t.Fatalf("decision = %q, want refuse", got.Decision)
		}
		if !strings.Contains(got.Reason, "verdict gate refused") {
			t.Fatalf("reason = %q, want it to name the verdict gate", got.Reason)
		}
	})

	t.Run("3_ci_green_same_lane_verdict_refuses", func(t *testing.T) {
		responses := map[string]string{}
		greenCI(repo, number, sha, responses)
		comments := "[" + commentJSON("jonhill90", "Verdict: APPROVE\nReview-Lane: build-5\nReviewed-SHA: "+sha+"\n") + "]"
		responses[viewKey(repo, number, fields)] = verdictView(repo, number, "Author-Lane: build-5\n", sha, comments)
		got := Evaluate(fixtureRunner(t, responses, nil), repo, number)
		if got.Decision != MergeRefuse {
			t.Fatalf("decision = %q, want refuse (self-review)", got.Decision)
		}
	})

	t.Run("4_ci_green_genuine_cross_lane_verdict_at_current_head_allows", func(t *testing.T) {
		responses := map[string]string{}
		greenCI(repo, number, sha, responses)
		comments := "[" + commentJSON("jonhill90", "Verdict: APPROVE\nReview-Lane: build-5\nReviewed-SHA: "+sha+"\n") + "]"
		responses[viewKey(repo, number, fields)] = verdictView(repo, number, "Author-Lane: build-2\n", sha, comments)
		got := Evaluate(fixtureRunner(t, responses, nil), repo, number)
		if got.Decision != MergeAllow {
			t.Fatalf("decision = %q, want allow (reason: %s)", got.Decision, got.Reason)
		}
	})
}

func TestEvaluateRefusesOnSHAMismatchBetweenGates(t *testing.T) {
	// A defense this package adds beyond either single gate: if the CI
	// gate and the verdict gate somehow read different head SHAs for the
	// same PR (a push racing between the two `gh` calls), refuse rather
	// than trust either one -- verified directly since nothing else in
	// this package's own tests exercises the mismatch branch.
	repo, number := "jonhill90/agent-tui", 1
	other := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	fields := "body,headRefOid,comments"

	callCount := 0
	responses := map[string]string{}
	greenCI(repo, number, sha, responses)
	comments := "[" + commentJSON("jonhill90", "Verdict: APPROVE\nReview-Lane: build-5\nReviewed-SHA: "+sha+"\n") + "]"
	responses[viewKey(repo, number, fields)] = verdictView(repo, number, "Author-Lane: build-2\n", other, comments)

	run := func(args []string) ([]byte, error) {
		callCount++
		key := strings.Join(args, " ")
		resp, ok := responses[key]
		if !ok {
			t.Fatalf("no response stubbed for %q", key)
		}
		return []byte(resp), nil
	}

	got := Evaluate(run, repo, number)
	if got.Decision != MergeRefuse {
		t.Fatalf("decision = %q, want refuse on a moving target", got.Decision)
	}
	if !strings.Contains(got.Reason, "moving target") {
		t.Fatalf("reason = %q, want it to mention a moving target", got.Reason)
	}
}

// Merge must never call the GHMerger when Evaluate refuses -- the
// invariant merge-pr.sh's own doc comment states as "cannot fall back to
// merging when either gate cannot be evaluated."
func TestMergeNeverCallsGHMergerOnRefusal(t *testing.T) {
	repo, number := "jonhill90/agent-tui", 1
	responses := map[string]string{
		headKey(repo, number):   fmt.Sprintf(`{"headRefOid":%q}`, sha),
		checkRunsKey(repo, sha): `{"check_runs":[]}`,
		statusKey(repo, sha):    `{"statuses":[]}`,
	}
	called := false
	merger := func(ghBin string, number int, repo string, extraArgs []string) (string, error) {
		called = true
		return "", nil
	}
	result, _, err := Merge(fixtureRunner(t, responses, nil), merger, "gh", repo, number, nil)
	if err == nil {
		t.Fatalf("Merge returned no error on a refused gate")
	}
	if result.Decision != MergeRefuse {
		t.Fatalf("decision = %q, want refuse", result.Decision)
	}
	if called {
		t.Fatalf("GHMerger was called despite a refused gate")
	}
}

func TestMergeCallsGHMergerOnAllow(t *testing.T) {
	repo, number := "jonhill90/agent-tui", 1
	fields := "body,headRefOid,comments"
	responses := map[string]string{}
	greenCI(repo, number, sha, responses)
	comments := "[" + commentJSON("jonhill90", "Verdict: APPROVE\nReview-Lane: build-5\nReviewed-SHA: "+sha+"\n") + "]"
	responses[viewKey(repo, number, fields)] = verdictView(repo, number, "Author-Lane: build-2\n", sha, comments)

	called := false
	merger := func(ghBin string, number int, repo string, extraArgs []string) (string, error) {
		called = true
		if ghBin != "gh" || number != 1 || repo != "jonhill90/agent-tui" {
			t.Fatalf("merger called with unexpected args: %s %d %s", ghBin, number, repo)
		}
		return "merged", nil
	}
	result, out, err := Merge(fixtureRunner(t, responses, nil), merger, "gh", repo, number, nil)
	if err != nil {
		t.Fatalf("Merge returned an error on an allowed gate: %s", err)
	}
	if result.Decision != MergeAllow {
		t.Fatalf("decision = %q, want allow", result.Decision)
	}
	if !called {
		t.Fatalf("GHMerger was not called despite an allowed gate")
	}
	if out != "merged" {
		t.Fatalf("out = %q, want %q", out, "merged")
	}
}
