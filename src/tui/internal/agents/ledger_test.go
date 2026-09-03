package agents

import (
	"reflect"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/src/tui/internal/estatus"
)

func TestDeriveLedgerOnlyIncludesInFlightTurns(t *testing.T) {
	at := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	status := estatus.Status{
		Dispatches: []estatus.Dispatch{
			{ID: "930-1", Issue: "#930", Role: "reviewer", State: "dispatched", At: at, PID: 4242},
			{ID: "920-1", Issue: "#920", State: "complete", At: at},
		},
		InFlight: []estatus.Dispatch{
			{ID: "930-1", Issue: "#930", Role: "reviewer", State: "dispatched", At: at, PID: 4242},
		},
	}

	got := DeriveLedger(status)
	want := []LedgerRow{
		{ID: "930-1", Issue: "#930", Role: "reviewer", State: "dispatched", Started: at.Format("2006-01-02T15:04:05Z07:00"), PID: intPtr(4242)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeriveLedger = %#v, want %#v", got, want)
	}
}

// A record written before agent-estate#944 (PID) or before Role existed
// must decode as an absent PID / default "author" role -- never a
// fabricated 0 pid rendered as if it were real, and never a guessed third
// role.
func TestDeriveLedgerDefaultsRoleAndOmitsZeroPID(t *testing.T) {
	at := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	status := estatus.Status{
		InFlight: []estatus.Dispatch{
			{ID: "old-1", Issue: "#1", State: "dispatched", At: at},
		},
	}
	got := DeriveLedger(status)
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}
	if got[0].Role != "author" {
		t.Errorf("Role = %q, want %q (EffectiveRole's own default)", got[0].Role, "author")
	}
	if got[0].PID != nil {
		t.Errorf("PID = %v, want nil for a record with no recorded pid", *got[0].PID)
	}
}

func TestDeriveLedgerEmptyInFlightIsEmptySlice(t *testing.T) {
	got := DeriveLedger(estatus.Status{})
	if len(got) != 0 {
		t.Fatalf("want 0 rows, got %d", len(got))
	}
}

func intPtr(v int) *int { return &v }
