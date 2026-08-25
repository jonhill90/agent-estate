package sendmsg

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"agent-supervisor/daemon/internal/agent"
)

// fakeAdapter is the same shape dispatch_test.go's own fakeAdapter takes:
// a Run that returns whatever the test configured, no subprocess involved.
type fakeAdapter struct {
	result *agent.Result
	err    error
}

func (f fakeAdapter) Run(context.Context, string) (*agent.Result, error) {
	return f.result, f.err
}

// TestSend_Delivered is the success direction: a nil error from the
// adapter must produce StatusDelivered with the result's own fields
// carried through untouched.
func TestSend_Delivered(t *testing.T) {
	got := Send(context.Background(), fakeAdapter{
		result: &agent.Result{SessionID: "sess-123", NumTurns: 3, CostUSD: 0.42},
	}, "keep going")

	if got.Status != StatusDelivered {
		t.Fatalf("Status = %q, want %q", got.Status, StatusDelivered)
	}
	if got.Err != nil {
		t.Fatalf("Err = %v, want nil on delivery", got.Err)
	}
	if got.SessionID != "sess-123" || got.Turns != 3 || got.CostUSD != 0.42 {
		t.Fatalf("result fields not carried through: %+v", got)
	}
}

// TestSend_Failed is the direction the brief calls out as mattering more:
// a confirmed failure from the adapter (not a timeout) MUST produce
// StatusFailed, never StatusDelivered -- a send that silently reports
// success is the exact defect this transport exists to eliminate.
func TestSend_Failed(t *testing.T) {
	wantErr := errors.New("agent: turn reported is_error subtype=\"error_max_turns\"")
	got := Send(context.Background(), fakeAdapter{err: wantErr}, "keep going")

	if got.Status != StatusFailed {
		t.Fatalf("Status = %q, want %q", got.Status, StatusFailed)
	}
	if got.Status == StatusDelivered {
		t.Fatal("a failed adapter call must never be reported as delivered")
	}
	if !errors.Is(got.Err, wantErr) {
		t.Fatalf("Err = %v, want it to wrap %v", got.Err, wantErr)
	}
	if got.SessionID != "" || got.Turns != 0 {
		t.Fatalf("a failed send must not report result fields as if delivered: %+v", got)
	}
}

// TestSend_Timeout_LeavesOutcomeUnknown is agent-supervisor#488's own
// lesson: a turn that does not come back before the deadline must be
// StatusUnknown, NOT StatusFailed -- the disposition is non-terminal, the
// same posture dispatch.go's isTimeout already takes for task dispatch.
func TestSend_Timeout_LeavesOutcomeUnknown(t *testing.T) {
	timeoutErr := fmt.Errorf("%w after 15m0s", agent.ErrTimeout)
	got := Send(context.Background(), fakeAdapter{err: timeoutErr}, "keep going")

	if got.Status != StatusUnknown {
		t.Fatalf("Status = %q, want %q -- a timeout must not be stamped failed", got.Status, StatusUnknown)
	}
	if got.Status == StatusFailed {
		t.Fatal("a timeout must never be reported as a definite failure")
	}
	if !errors.Is(got.Err, agent.ErrTimeout) {
		t.Fatalf("Err = %v, want it to wrap agent.ErrTimeout", got.Err)
	}
}
