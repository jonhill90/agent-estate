package gate

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// MainBranch and MainWorkflow name what "main is green" means for this
// specific estate: the branch every PR here merges onto, and the workflow
// (.github/workflows/estate-ci.yml) that is this repo's own build/vet/test
// gate -- not a caller-supplied value, the same discipline DispatchBranchPrefix
// already applies to the branch-naming convention.
const (
	MainBranch   = "main"
	MainWorkflow = "estate-ci"
)

// MainRun is the most recent completed run of one CI workflow on one branch,
// as GitHub itself reports it -- never anything a PR or a caller asserts
// about the branch's own state.
type MainRun struct {
	ID         int64
	Conclusion string // "success", "failure", "cancelled", ... ; "" means no completed run was reported (still queued/in-progress, or none exists)
	HeadSHA    string
}

// listMainRuns is a package-level seam so tests can drive MainStatusReason
// without a network dependency -- the same discipline fetch/evaluate already
// use for the PR-scoped conditions (see gate.go's own doc comment on
// evaluate). The real implementation below is swapped out in tests, never
// reimplemented against a copy of the decision logic.
var listMainRuns = ghListMainRuns

type mainRunRow struct {
	DatabaseID int64  `json:"databaseId"`
	Conclusion string `json:"conclusion"`
	HeadSHA    string `json:"headSha"`
}

// ghListMainRuns reads the single most recent run of workflow on branch,
// most-recent-first (gh run list's own default order), regardless of
// conclusion -- a queued or in-progress run reports conclusion "", which
// MainGreen treats the same as an unreported check: not a pass.
func ghListMainRuns(repo, branch, workflow string) (MainRun, bool, error) {
	out, err := exec.Command("gh", "run", "list",
		"-R", repo, "--branch", branch, "--workflow", workflow, "--limit", "1",
		"--json", "databaseId,conclusion,headSha").Output()
	if err != nil {
		return MainRun{}, false, fmt.Errorf("gh run list %s %s@%s: %w", repo, workflow, branch, err)
	}
	var rows []mainRunRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return MainRun{}, false, fmt.Errorf("decode run list: %w", err)
	}
	if len(rows) == 0 {
		return MainRun{}, false, nil
	}
	r := rows[0]
	return MainRun{ID: r.DatabaseID, Conclusion: r.Conclusion, HeadSHA: r.HeadSHA}, true, nil
}

// MainGreen reports whether the branch's own most recent CI run permits a
// merge to proceed. found=false (no run on record at all) refuses for the
// same reason checksGreen refuses when a PR reports no checks: "cannot tell"
// is never "allowed" anywhere else in this gate, and treating an absent
// signal as a pass here would be the one exception.
//
// agent-estate#1379: measured 2026-09-02 through 2026-09-11 (250 estate-ci
// runs on main), every red run was a deterministic build-time (vet) or
// environment-mismatch (test) failure -- zero were transient/flaky. Nothing
// observed on main itself supports treating a test-step failure as lower
// confidence than a build/vet one, so this does not distinguish them; both
// refuse. See the PR body for the measurement and the rejected alternatives.
func MainGreen(run MainRun, found bool) (green bool, reason string) {
	if !found {
		return false, "no completed run on record for this branch/workflow -- refusing rather than assuming green"
	}
	switch run.Conclusion {
	case "success":
		return true, ""
	case "":
		return false, "latest run (" + strconv.FormatInt(run.ID, 10) + ") is still queued or in progress -- not green yet"
	default:
		return false, "latest run (" + strconv.FormatInt(run.ID, 10) + ") at head " + short(run.HeadSHA) + " concluded " + run.Conclusion
	}
}

// MainStatusReason is the whole red-main check. It is called from Evaluate
// (never from evaluate(), which stays PR-fixture-driven and network-free --
// see gate.go's own doc comment on that split), because this condition does
// not concern any one PR: it is about the branch every PR merges onto.
//
// override, when non-empty, is an EXPLICIT, logged acknowledgement that
// bypasses this one condition -- agent-estate#1379's own brief: "an override
// nobody can use is the same defect as a guard nobody calls." A bypass is
// never silent: the returned note says so, every time, whether or not it
// changes the outcome, so a Decision's Reasons always show what happened.
func MainStatusReason(repo, branch, workflow, override string) (note string, refuse bool) {
	run, found, err := listMainRuns(repo, branch, workflow)
	if err != nil {
		return "could not read " + branch + "'s own CI state: " + err.Error(), true
	}
	green, reason := MainGreen(run, found)
	if green {
		return "", false
	}
	if override != "" {
		return branch + " is red (" + reason + ") -- merging anyway, override: " + override, false
	}
	return branch + " is red (" + reason + ") -- refusing to add another merge on top of a broken build " +
		"(agent-estate#1379); pass an explicit --override-red-main reason if this must proceed anyway", true
}
