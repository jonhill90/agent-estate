package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unknown must not free a slot. A turn we could not observe may still be
// running; treating it as finished is how a cap fails open.
func TestUnknownIsNotTerminal(t *testing.T) {
	for s, want := range map[State]bool{
		Complete: true, Failed: true, Unknown: false, Dispatched: false,
	} {
		if got := s.Terminal(); got != want {
			t.Fatalf("State(%q).Terminal() = %v, want %v", s, got, want)
		}
	}
}

func TestInFlightCountsUnknownAndDispatched(t *testing.T) {
	l := &Ledger{path: filepath.Join(t.TempDir(), "l.jsonl")}
	for _, r := range []Record{
		{ID: "a", State: Dispatched}, {ID: "a", State: Complete},
		{ID: "b", State: Dispatched}, {ID: "b", State: Unknown},
		{ID: "c", State: Dispatched},
	} {
		if err := l.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.InFlight()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("InFlight() = %d records, want 2 (b unknown, c dispatched)", len(got))
	}
}

// A corrupt ledger must be an error, never a short list. A truncated read
// reads as "less work in flight", which fails open on the cap.
func TestMalformedLineIsAnErrorNotAShortList(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.jsonl")
	l := &Ledger{path: p}
	if err := l.Append(Record{ID: "a", State: Dispatched}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{not json\n")
	f.Close()

	if _, err := l.Current(); err == nil {
		t.Fatal("Current() returned nil error on a corrupt ledger; it must refuse, not truncate")
	}
}

func TestMissingLedgerIsEmptyNotAnError(t *testing.T) {
	l := &Ledger{path: filepath.Join(t.TempDir(), "absent.jsonl")}
	got, err := l.Current()
	if err != nil || got != nil {
		t.Fatalf("Current() on absent ledger = %v, %v; want nil, nil", got, err)
	}
}

// A missing ledger in a directory that does not exist is a wrong path, and
// reporting "zero in flight" from it tells the cap the host is free while
// agents run. A missing ledger in a real directory is a first run.
func TestMissingLedgerInMissingDirectoryRefuses(t *testing.T) {
	l := &Ledger{path: filepath.Join(t.TempDir(), "no-such-dir", "l.jsonl"), explicit: true}
	if _, err := l.Current(); err == nil {
		t.Fatal("Current() reported zero tasks from a path whose directory does not exist -- fail open")
	}
}

func TestMissingLedgerInRealDirectoryIsAFirstRun(t *testing.T) {
	l := &Ledger{path: filepath.Join(t.TempDir(), "l.jsonl"), explicit: true}
	got, err := l.Current()
	if err != nil || got != nil {
		t.Fatalf("first run should be empty and fine, got %v / %v", got, err)
	}
}


// Phase is the join agent-estate#1012 needs: what actually ran, against what
// we were trying to do. Empty must stay ABSENT on the wire -- a record
// written before the field existed and a dispatch that named no phase are
// the same absence, and neither may read back as a real phase.
func TestPhaseRoundTripsAndOmitsWhenUnstated(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Record{ID: "stated", State: Dispatched, Phase: "phase-3"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Record{ID: "unstated", State: Dispatched}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d line(s), want 2", len(lines))
	}
	if !strings.Contains(lines[0], `"phase":"phase-3"`) {
		t.Errorf("stated phase did not reach the wire: %s", lines[0])
	}
	if strings.Contains(lines[1], `"phase"`) {
		t.Errorf("an unstated phase was written as a value rather than omitted: %s", lines[1])
	}

	cur, err := l.Current()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range cur {
		got[r.ID] = r.Phase
	}
	if got["stated"] != "phase-3" {
		t.Errorf("read back phase %q, want phase-3", got["stated"])
	}
	if got["unstated"] != "" {
		t.Errorf("read back phase %q for a record that stated none, want empty", got["unstated"])
	}
}
