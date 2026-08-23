package mergepr

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/jonhill90/keelson/internal/prverdict"
)

// MergeDecision is Evaluate's whole answer, before Merge ever shells out
// to `gh pr merge` -- kept separate from the act of merging so a caller
// (or a test) can inspect exactly why a merge was refused without also
// mutating the PR, the same split ci_gate.py/merge-pr.sh keep in
// agent-supervisor.
type MergeDecision string

const (
	// MergeAllow: both gates passed; it is genuinely safe to merge.
	MergeAllow MergeDecision = "allow"
	// MergeRefuse: at least one gate refused. Never merge on this.
	MergeRefuse MergeDecision = "refuse"
)

// EvaluateResult is Evaluate's return value -- CI and Verdict are both
// always populated (never partially, since Evaluate short-circuits on the
// first refusal) so a caller can log the full picture, not just the
// decision.
type EvaluateResult struct {
	Decision MergeDecision
	Reason   string
	CI       CIResult
	Verdict  prverdict.Result
}

// Evaluate chains both gates in order -- CI first, then the comment-verdict
// gate -- fail-closed at each step, exactly mirroring merge-pr.sh's own
// two-gate order (its own comment: "a gate refused; nothing was merged").
// It never calls `gh pr merge`; Merge does that only after Evaluate
// returns MergeAllow.
func Evaluate(run Runner, repo string, number int) EvaluateResult {
	ci := EvaluateCI(run, repo, number)
	if ci.Decision != CIAllow {
		return EvaluateResult{Decision: MergeRefuse, Reason: fmt.Sprintf("CI gate refused -- %s", ci.Reason), CI: ci}
	}

	// agent-dotfiles#218's rule, ported by internal/prverdict's own doc
	// comment and re-applied here explicitly: the verdict gate must
	// answer for the SAME sha the CI gate just evaluated, not a
	// re-fetched one that could have moved between the two calls. Fetch
	// once here and let prverdict.Resolve compare Reviewed-SHA against
	// this exact value.
	payload, err := prverdict.Fetch(run, repo, number)
	if err != nil {
		v := prverdict.Result{Decision: prverdict.Unknown, Detail: err.Error()}
		return EvaluateResult{Decision: MergeRefuse, Reason: fmt.Sprintf("verdict gate refused -- %s", v.Detail), CI: ci, Verdict: v}
	}
	if payload.HeadSHA != ci.SHA {
		// Fails closed rather than silently trusting either read: the two
		// gates disagreeing about the PR's own head SHA means a push
		// raced between the two `gh` calls, the exact race ci_gate.py's
		// own doc comment refuses to paper over for a single gate, now
		// applied across both of this package's gates.
		v := prverdict.Result{Decision: prverdict.Unknown, Detail: fmt.Sprintf("CI gate read head %s but verdict gate read head %s -- refusing on a moving target", ci.SHA, payload.HeadSHA)}
		return EvaluateResult{Decision: MergeRefuse, Reason: fmt.Sprintf("verdict gate refused -- %s", v.Detail), CI: ci, Verdict: v}
	}

	verdict := prverdict.Resolve(payload)
	if verdict.Decision != prverdict.Approved {
		return EvaluateResult{Decision: MergeRefuse, Reason: fmt.Sprintf("verdict gate refused -- %s", verdict.Detail), CI: ci, Verdict: verdict}
	}

	return EvaluateResult{Decision: MergeAllow, Reason: fmt.Sprintf("CI green at %s; verdict approved -- %s", ci.SHA, verdict.Detail), CI: ci, Verdict: verdict}
}

// mergeTimeout bounds the `gh pr merge` call itself -- same reasoning as
// prverdict's execTimeout: an unbounded network stall must not hang
// whatever invoked this package forever. A package var, not a literal, so
// a test can shrink it (mirrors prverdict.execTimeout's own pattern).
var mergeTimeout = 30 * time.Second

// GHMerger runs `gh pr merge` for real -- the only function in this
// package that mutates anything. Kept separate from Runner (which this
// package only ever uses for read-only `gh` calls) so a test exercising
// Evaluate can never accidentally merge a real PR by supplying the wrong
// double.
type GHMerger func(ghBin string, number int, repo string, extraArgs []string) (string, error)

// ExecGHMerger shells `gh pr merge` out via os/exec -- the real
// implementation, same execution pattern as prverdict.ExecRunner.
func ExecGHMerger(ghBin string, number int, repo string, extraArgs []string) (string, error) {
	args := append([]string{"pr", "merge", fmt.Sprintf("%d", number), "--repo", repo}, extraArgs...)
	ctx, cancel := context.WithTimeout(context.Background(), mergeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, ghBin, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %v: %w", ghBin, args, err)
	}
	return string(out), nil
}

// Merge is the one entry point that both evaluates and, only on
// MergeAllow, actually merges -- the same "this script is the ONLY thing
// that chains the two" role merge-pr.sh names explicitly for
// agent-supervisor. extraArgs is passed through to `gh pr merge` verbatim
// (e.g. --squash, --delete-branch) exactly as merge-pr.sh does, so this
// package never picks a merge strategy on the caller's behalf.
func Merge(run Runner, merger GHMerger, ghBin, repo string, number int, extraArgs []string) (EvaluateResult, string, error) {
	result := Evaluate(run, repo, number)
	if result.Decision != MergeAllow {
		return result, "", fmt.Errorf("merge refused: %s", result.Reason)
	}
	out, err := merger(ghBin, number, repo, extraArgs)
	return result, out, err
}
