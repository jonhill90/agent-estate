// Package quota reports how much of the operator's token budget is left.
//
// His hard rule: at roughly 10% remaining, stop orchestrating work to lanes.
// The previous implementation could not enforce it. Its launchd job invoked
// `agent-supervisor/scripts/supervisor/quota-watch.sh` -- a repo that does not
// exist -- so its state file accumulated 3,727 consecutive UNKNOWN readings
// while `confirmed: SAFE` sat beside them and `blind_alarm_sent: 0` never
// moved. The week was exhausted with the meter dark.
//
// Two rules follow from that, and they are the whole design:
//
//  1. UNKNOWN IS NOT SAFE. A reading that could not be taken is a refusal,
//     never a pass. Blindness is the condition that cost him the week.
//  2. A STALE READING IS UNKNOWN. A number from hours ago describes a budget
//     that has since been spent. Freshness is part of validity, not a detail.
//
// agent-estate#1163 (Read had no timeout at all, so a stalled codexbar hung
// the gate instead of refusing) asked which of three shapes a bounded
// read should take, and required the rejected two be argued against, not
// just this package's own choice asserted:
//
//   - FAIL OPEN on a timeout -- dispatch anyway, quota unknown -- was
//     explicitly considered and rejected. This is the shape agent-estate
//     #1383 chose for the red-main check, and that reasoning is real for
//     an ADVISORY about a DIFFERENT branch's health. Quota is not
//     advisory: exceeding it kills the turn mid-work (agent-estate#1127's
//     122 uncommitted insertions, the literal incident that field exists
//     to prevent). The cost of being wrong is asymmetric in a direction
//     #1383's own case does not share -- a needless refusal here wastes a
//     few minutes; dispatching into an exhausted quota destroys a turn's
//     work outright. Rule 1 above already settles this and a timeout does
//     not get a new answer to the same question.
//   - A LAST-KNOWN-GOOD READING WITH AN AGE (permit on a timeout if a
//     recent-enough cached value looked healthy) was also considered and
//     rejected, on the real precedent for exactly this shape:
//     agent-estate#436 (a revert of a quota fast path that served a
//     cached SAFE verdict) cites agent-estate#264 item 4 -- "stale-but-
//     dated beats hung" is #264's own verbatim wording -- and states the
//     invariant #436 itself derived and enforced: "the gate's own exit
//     code must never soften to 0 just because a cached number exists."
//     #436 names a real incident behind that rule: a cached-SAFE read
//     that spent an exhausted window, $80 -> $8. A cached reading, by
//     construction, describes what the window looked like before
//     whatever is blocking the LIVE probe -- using it to permit here is
//     exactly the softening #436 reverted. Rule 2 above already encodes
//     the age half of this (a stale reading is unknown); this package
//     has never had a code path that uses an old reading to permit, and
//     this timeout does not become the first one.
//
// So a timeout is exactly rule 1: unmeasurable, refuse -- and, per
// agent-estate#474's other requirement ("make the failure loud... a
// specific code and it should be interpreted, not ignored"), Read below
// says so as its own, specifically-named cause, not folded into the
// generic "codexbar unreachable" wording a plain exit failure gets.
package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// Reading is a point-in-time budget observation. Weekly is the window his
// stop-threshold refers to; Session is the shorter rolling window.
type Reading struct {
	WeeklyUsedPercent  float64
	SessionUsedPercent float64
	UpdatedAt          time.Time
	Age                time.Duration
	// WeeklyResetsAt/SessionResetsAt are codexbar's own machine-readable
	// reset timestamps for each window (agent-estate#993's own request:
	// a refusal should say WHEN it resets, not just that it refused) --
	// RFC3339, not the harness's own human-readable string ("resets
	// 4:10pm (America/New_York)") the incident behind both #993 and
	// #1127 quoted. That string is what killed the dispatch mid-turn, not
	// what this package reads: codexbar's JSON already carries a proper
	// timestamp at usage.primary.resetsAt / usage.secondary.resetsAt, so
	// there is no fragile string-parsing dependency to weigh here at all
	// -- confirmed by reading codexbar's own live, current output (a
	// read-only status call, the same one Read() already performs in
	// production; never a triggered or simulated exhaustion). Zero value
	// means "not reported or unparseable" -- a missing reset time is
	// reported as unknown in a refusal message, never guessed.
	WeeklyResetsAt  time.Time
	SessionResetsAt time.Time
}

func (r Reading) WeeklyRemaining() float64 { return 100 - r.WeeklyUsedPercent }

// SessionRemaining is WeeklyRemaining's sibling for the shorter rolling
// window -- agent-estate#1127: this value was read into SessionUsedPercent
// above and then never consulted by anything. That is the gap this reading
// closes; AllowSession (below) is what acts on it.
func (r Reading) SessionRemaining() float64 { return 100 - r.SessionUsedPercent }

type payload struct {
	Usage struct {
		UpdatedAt string `json:"updatedAt"`
		Primary   struct {
			UsedPercent float64 `json:"usedPercent"`
			ResetsAt    string  `json:"resetsAt"`
		} `json:"primary"`
		Secondary *struct {
			UsedPercent float64 `json:"usedPercent"`
			ResetsAt    string  `json:"resetsAt"`
		} `json:"secondary"`
	} `json:"usage"`
}

// MaxAge past which a reading is treated as no reading at all.
const MaxAge = 20 * time.Minute

// ReadTimeout bounds the codexbar call below -- agent-estate#1163: Read used
// to run `exec.Command(...).Output()` with no deadline at all, so a stalled
// codexbar made the gate hang instead of producing its intended fail-closed
// refusal, stalling every dispatch and the Director's own tick along with
// it. A var, not a const, so a test can lower it temporarily and prove the
// bound is real without waiting out the production value.
//
// 60s, not a round number pulled from nowhere -- measured fresh tonight
// (agent-estate#1163's own brief: do not trust the issue's 2026-09-05
// "8-9 seconds", things move), five consecutive live `codexbar usage
// --provider claude --json` calls on this host under real load (3
// worktrees->33 tonight, load average 3.0-4.3): four landed at 17.9-18.4s,
// one outlier at 41.2s. Following #428's own methodology for this exact
// class of timeout ("4x the measured call, not a round number") rather than
// inventing a fresh one: 60s clears the observed outlier with real margin
// (~1.5x) while still bounding a genuine hang to roughly the same order of
// magnitude #428 itself judged acceptable for a stalled quota probe.
var ReadTimeout = 60 * time.Second

// runCodexbar is a package-level seam so a test can drive Read's timeout
// path deterministically -- codexbar itself has no injectable fake short of
// this, and a real subprocess that never exits is not something a unit
// test should depend on. Mirrors the discipline stars.go's defaultGHRunner
// and build_commit.go's defaultGitRunner already established elsewhere in
// this daemon -- the same class of fix (a real external dependency gets a
// function seam so a test never touches it) pressure.Limits.ReadQuota
// itself is, per its own comment (agent-estate#1321), though that comment
// cites a different, same-file precedent (pressure.go's own swapoutRate
// sample func()) rather than these two, since ReadQuota's seam lives in a
// different package than either.
var runCodexbar = defaultRunCodexbar

func defaultRunCodexbar(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "codexbar", args...).Output()
}

// Read takes a fresh reading. Every failure path returns an error: there is no
// value this function can return that means "could not tell, carry on".
func Read(now time.Time) (Reading, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ReadTimeout)
	defer cancel()
	out, err := runCodexbar(ctx, "usage", "--provider", "claude", "--json")
	if err != nil {
		if ctx.Err() != nil {
			// Named specifically, not folded into the generic "unreachable"
			// wrap below -- agent-estate#474's own explicit ask, applied
			// one level up: a timeout is not the same claim as "codexbar
			// exited non-zero" or "codexbar was never on PATH", and an
			// operator or a later fix should not have to guess which one
			// happened from an error string that reads the same either way.
			return Reading{}, fmt.Errorf("codexbar timed out after %s -- refusing rather than guessing (agent-estate#1163: an unbounded call here used to hang the gate instead of refusing)", ReadTimeout)
		}
		return Reading{}, fmt.Errorf("codexbar unreachable: %w", err)
	}
	var ps []payload
	if err := json.Unmarshal(out, &ps); err != nil {
		return Reading{}, fmt.Errorf("codexbar output unparseable: %w", err)
	}
	if len(ps) == 0 {
		return Reading{}, fmt.Errorf("codexbar returned no providers -- refusing to read that as budget available")
	}
	p := ps[0]
	if p.Usage.Secondary == nil {
		return Reading{}, fmt.Errorf("codexbar reported no weekly window -- the window the stop-threshold refers to is missing")
	}
	ts, err := time.Parse(time.RFC3339, p.Usage.UpdatedAt)
	if err != nil {
		return Reading{}, fmt.Errorf("codexbar timestamp %q unparseable: %w", p.Usage.UpdatedAt, err)
	}
	r := Reading{
		WeeklyUsedPercent:  p.Usage.Secondary.UsedPercent,
		SessionUsedPercent: p.Usage.Primary.UsedPercent,
		UpdatedAt:          ts,
		Age:                now.Sub(ts),
		// parseResetsAt never fails Read() over a missing/malformed reset
		// time -- it is a courtesy in the refusal MESSAGE, not part of the
		// refuse/allow decision (which keys on UsedPercent alone). A
		// reading that refuses correctly but cannot yet say when it
		// resets is still a correct, actionable refusal; the zero value
		// says "unknown" in that case, per Reading's own doc comment.
		WeeklyResetsAt:  parseResetsAt(p.Usage.Secondary.ResetsAt),
		SessionResetsAt: parseResetsAt(p.Usage.Primary.ResetsAt),
	}
	if r.Age > MaxAge {
		return Reading{}, fmt.Errorf("codexbar reading is %s old (limit %s) -- stale is unknown, and unknown is not safe", r.Age.Round(time.Second), MaxAge)
	}
	if r.Age < -2*time.Minute {
		return Reading{}, fmt.Errorf("codexbar reading is %s in the future -- clock skew, refusing to trust it", (-r.Age).Round(time.Second))
	}
	return r, nil
}

// parseResetsAt is a best-effort RFC3339 parse -- an empty or malformed
// value returns the zero time.Time rather than an error, matching the
// "courtesy, not a decision input" status this field's own doc comment on
// Reading states. Never fails Read() itself.
func parseResetsAt(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// resetClause renders "" when resetsAt is unknown (the zero value), or a
// human-readable " -- resets HH:MM (in Nm)" clause otherwise. Shared by
// Allow and AllowSession so the two refusal messages stay in the same
// shape; "in Nm" is computed against time.Now() at call time, which is
// accurate to within the caller's own call latency -- more than close
// enough for a minutes-granularity clause a caller reads once.
func resetClause(resetsAt time.Time) string {
	if resetsAt.IsZero() {
		return ""
	}
	until := time.Until(resetsAt)
	if until < 0 {
		return fmt.Sprintf(" -- reported reset %s was in the past; treat the reset time as unknown", resetsAt.Local().Format("15:04"))
	}
	return fmt.Sprintf(" -- resets %s (in %s)", resetsAt.Local().Format("15:04"), until.Round(time.Minute))
}

// StopThresholdPercent is his rule: at roughly 10% remaining, stop
// orchestrating work to the lanes.
const StopThresholdPercent = 10.0

// Allow reports whether more orchestrated work may start, judged against
// the WEEKLY window. See AllowSession for the shorter rolling window --
// kept as two functions rather than one that ORs both together, so a
// caller (pressure.Check) can report each refusal under its own,
// independently-attributable reason string instead of one that could name
// either cause (agent-estate#1127's own requirement: a session refusal
// must not read as, or be indistinguishable from, a weekly one).
func Allow(r Reading) (bool, string) {
	if rem := r.WeeklyRemaining(); rem <= StopThresholdPercent {
		return false, fmt.Sprintf("weekly budget %.0f%% remaining, at or below the %.0f%% stop threshold%s", rem, StopThresholdPercent, resetClause(r.WeeklyResetsAt))
	}
	return true, ""
}

// SessionStopThresholdPercent is the operator's own stated threshold for
// this same primary/session window -- a hard, acted-on parameter recorded
// 2026-08-22 for the estate tick's own quota halt, verbatim: "once primary
// Claude usage reaches or exceeds 97%." Never implemented in Go anywhere
// before this (agent-estate#1127): the field it acts on (SessionUsedPercent,
// above) was read from codexbar and then never consulted by anything in
// this daemon. The old shell supervisor had an equivalent >=97% quota-halt
// in check.sh; it was silently dropped when the scheduler moved off
// launchd and nothing rebuilt it, so this is a real re-implementation of a
// standing rule, not a new invention.
//
// 3.0, not StopThresholdPercent's 10.0: the session window is short and
// rolling -- the incident that motivated this issue observed "resets
// 4:10pm" on the same day it exhausted -- so the operator's own stated
// margin for it is deliberately tighter than the weekly one's.
const SessionStopThresholdPercent = 3.0

// AllowSession reports whether more orchestrated work may start, judged
// against the SHORTER rolling window alone. See Allow's own doc comment
// for why this is a separate function rather than folded into it.
func AllowSession(r Reading) (bool, string) {
	if rem := r.SessionRemaining(); rem <= SessionStopThresholdPercent {
		return false, fmt.Sprintf("session usage %.0f%% remaining, at or below the %.0f%% stop threshold (agent-estate#1127)%s", rem, SessionStopThresholdPercent, resetClause(r.SessionResetsAt))
	}
	return true, ""
}
