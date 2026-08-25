// Package sendmsg sends one ad-hoc message to an EXISTING agent session and
// reports what happened as an observed fact, never an inference -- the same
// delivery contract agent.Claude.Run and dispatch.Run already hold.
//
// This is deliberately NOT dispatch.Run reused as-is: dispatch is
// task-shaped (a ledger row, an optional claim, a brief that starts new
// work); a send is thread-shaped -- there is no task ID, no ledger row, no
// claim to take, just "continue thread T with message M" (agent-tui's own
// SPEC-shell.md S7, and agent-supervisor#508, the issue this package
// closes). What IS inherited from dispatch.go is the three-state delivery
// verdict: a nil error from the adapter means delivered, a confirmable
// failure is Failed, and a turn that does not come back before the
// deadline is Unknown -- never stamped Failed. That is the exact
// distinction agent-supervisor#488 was filed over ("a task stamped
// `complete` while its process still ran"): a send that silently reports
// success when it cannot confirm one is the defect this whole transport
// exists to eliminate.
package sendmsg

import (
	"context"
	"errors"
	"fmt"

	"agent-supervisor/daemon/internal/agent"
)

// Status is the only vocabulary a caller may report. There is no fourth
// state and no zero-value Status that reads as anything but "unset".
type Status string

const (
	// StatusDelivered: the agent received the message and produced a
	// result -- res.SessionID/Turns/CostUSD are populated from it.
	StatusDelivered Status = "delivered"
	// StatusFailed: the process ran to completion and the outcome is a
	// confirmed failure (a non-zero exit, a malformed result, or the
	// result's own is_error field). Never used for a timeout -- see
	// StatusUnknown.
	StatusFailed Status = "failed"
	// StatusUnknown: the turn did not come back before the deadline. The
	// message may have been received and may still be running -- this is
	// NOT a failure, exactly as dispatch.go's own isTimeout leaves a
	// dispatched task's ledger row non-terminal rather than stamping it
	// failed (agent-supervisor#488).
	StatusUnknown Status = "unknown"
)

// Result is the one value Send returns. Err is set for Failed and Unknown,
// nil for Delivered -- a caller can switch on Status alone and never needs
// to also nil-check Err to know which branch it is in.
type Result struct {
	Status    Status
	SessionID string
	Turns     int
	CostUSD   float64
	Err       error
}

// Send drives one turn against `a`, which the caller has already configured
// to resume the target session (agent.Claude.SessionID / agent.Codex.SessionID
// -- both already exist for exactly this, "" meaning a fresh session, non-""
// meaning --resume). Send itself has no session-scoping knowledge: nothing
// here knows or cares whether it is sending to a fresh thread or an existing
// one, only how to classify what the adapter's own Run call observed.
func Send(ctx context.Context, a agent.Adapter, message string) Result {
	res, err := a.Run(ctx, message)
	if err != nil {
		if errors.Is(err, agent.ErrTimeout) {
			return Result{
				Status: StatusUnknown,
				Err:    fmt.Errorf("%w -- outcome left unknown on purpose, not stamped failed", err),
			}
		}
		return Result{Status: StatusFailed, Err: err}
	}
	return Result{
		Status:    StatusDelivered,
		SessionID: res.SessionID,
		Turns:     res.NumTurns,
		CostUSD:   res.CostUSD,
	}
}
