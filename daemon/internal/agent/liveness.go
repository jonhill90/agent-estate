package agent

import "strings"

// Liveness is the run's QUALITY verdict, orthogonal to its exit status.
//
// STOLEN FROM: paperclip RUN_LIVENESS_STATES (packages/shared/src/constants.ts:930).
// Their insight, and it is the single most relevant idea in all three
// codebases for this estate: "exit 0" is not "did work".
//
// agent-supervisor#414 is exactly this failure -- a lane that backgrounds a
// command is recorded complete having delivered nothing. Exit status said
// success; nothing happened. With only pass/fail there is nowhere to record
// that, so it gets recorded as pass.
type Liveness string

const (
	LivenessCompleted     Liveness = "completed"      // did the work
	LivenessAdvanced      Liveness = "advanced"       // partial, real progress
	LivenessPlanOnly      Liveness = "plan_only"      // described work, did none
	LivenessEmptyResponse Liveness = "empty_response" // produced nothing
	LivenessBlocked       Liveness = "blocked"        // stopped on a decision
	LivenessFailed        Liveness = "failed"
	LivenessNeedsFollowup Liveness = "needs_followup"
)

// Classify derives the verdict from an observed result.
//
// Deliberately conservative: it only claims `completed` when there is real
// output AND at least one turn ran. Anything it cannot judge becomes
// `needs_followup`, never `completed` -- the could-not-measure rule, which in
// this estate must never borrow "pass".
func Classify(r *Result, err error) Liveness {
	if err != nil {
		return LivenessFailed
	}
	if r == nil {
		return LivenessEmptyResponse
	}
	if r.IsError {
		return LivenessFailed
	}
	text := strings.TrimSpace(r.Text)
	if text == "" {
		return LivenessEmptyResponse
	}
	if r.NumTurns <= 0 {
		return LivenessEmptyResponse
	}
	// A turn that only narrates intent is the #414 shape. Cheap heuristic,
	// deliberately narrow: short output that is all future-tense planning.
	if len(text) < 400 && looksPlanOnly(text) {
		return LivenessPlanOnly
	}
	return LivenessCompleted
}

func looksPlanOnly(s string) bool {
	l := strings.ToLower(s)
	markers := []string{"i will ", "i'll ", "next i ", "i plan to ", "here is the plan", "here's the plan"}
	for _, m := range markers {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}
