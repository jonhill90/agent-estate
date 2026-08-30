package pressure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

// A ledger that cannot be read must refuse. Blindness is not capacity.
func TestUnreadableLedgerRefuses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.jsonl")
	if err := os.WriteFile(p, []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := ledger.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	v := Check(l, Default())
	if v.OK {
		t.Fatal("Check() allowed dispatch with an unreadable ledger; it must fail closed")
	}
}

func TestAtCapRefuses(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lim := Default()
	lim.MaxInFlight = 2
	lim.MaxLoadPerCore = 1e9 // isolate the cap from real host load
	lim.MinFreeMemMB = 0
	for _, id := range []string{"a", "b"} {
		if err := l.Append(ledger.Record{ID: id, State: ledger.Dispatched}); err != nil {
			t.Fatal(err)
		}
	}
	if v := Check(l, lim); v.OK {
		t.Fatalf("Check() allowed dispatch at the cap; reading=%+v", v.Reading)
	}
}

func TestBelowCapAllows(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lim := Default()
	lim.MaxInFlight = 2
	lim.MaxLoadPerCore = 1e9
	lim.MinFreeMemMB = 0
	if err := l.Append(ledger.Record{ID: "a", State: ledger.Complete}); err != nil {
		t.Fatal(err)
	}
	if v := Check(l, lim); !v.OK {
		t.Fatalf("Check() refused below the cap: %v", v.Reasons)
	}
}
