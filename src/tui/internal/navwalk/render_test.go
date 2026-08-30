package navwalk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadManifestParsesOrderedEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte(`[{"id":"home","label":"Home"},{"id":"chat","label":"Chat"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadManifest(path)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if len(entries) != 2 || entries[0].ID != "home" || entries[1].ID != "chat" {
		t.Fatalf("got %+v, want [{home Home} {chat Chat}] in order", entries)
	}
}

func TestResolveUsesLatestObservationPerRoute(t *testing.T) {
	dir := t.TempDir()
	if err := AppendObservation(filepath.Join(dir, "observations", "chat.jsonl"),
		Observation{Date: "2026-08-15", Source: "old walk", Verdict: VerdictStub}); err != nil {
		t.Fatal(err)
	}
	if err := AppendObservation(filepath.Join(dir, "observations", "chat.jsonl"),
		Observation{Date: "2026-08-22", Source: "PR agent-tui#99", Verdict: VerdictRenders, Notes: "real source"}); err != nil {
		t.Fatal(err)
	}

	manifest := []ManifestEntry{{ID: "chat", Label: "Chat"}, {ID: "tasks", Label: "Tasks"}}
	rows, err := Resolve(dir, manifest)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if !rows[0].Known || rows[0].Obs.Verdict != VerdictRenders || rows[0].Obs.Source != "PR agent-tui#99" {
		t.Errorf("chat row = %+v, want the LATEST (2026-08-22) observation, not the older one", rows[0])
	}
	if rows[1].Known {
		t.Errorf("tasks row = %+v, want Known=false -- never measured", rows[1])
	}
}

func TestRenderNeverMeasuredRouteIsCouldNotMeasureNotBlank(t *testing.T) {
	rows := []ResolvedRow{{Entry: ManifestEntry{ID: "tasks", Label: "Tasks"}, Known: false}}
	out := Render(rows)
	if !strings.Contains(out, "could not measure") {
		t.Errorf("Render output missing %q for a never-measured route:\n%s", "could not measure", out)
	}
	if !strings.Contains(out, "Tasks") {
		t.Errorf("Render output missing the destination label:\n%s", out)
	}
}

func TestRenderIncludesEveryRowsSourceSoNewestIsDecidableFromTheFile(t *testing.T) {
	rows := []ResolvedRow{
		{Entry: ManifestEntry{ID: "chat", Label: "Chat"}, Known: true,
			Obs: Observation{Date: "2026-08-22", Source: "PR agent-tui#99", Verdict: VerdictRenders, Notes: "real"}},
	}
	out := Render(rows)
	if !strings.Contains(out, "PR agent-tui#99") || !strings.Contains(out, "2026-08-22") {
		t.Errorf("Render output missing the observation's own source/date -- a reader must be able to tell what produced this row without asking anyone:\n%s", out)
	}
}

func TestRenderSummaryCountsMatchTheRows(t *testing.T) {
	rows := []ResolvedRow{
		{Entry: ManifestEntry{ID: "a", Label: "A"}, Known: true, Obs: Observation{Verdict: VerdictRenders, Date: "2026-08-22", Source: "s"}},
		{Entry: ManifestEntry{ID: "b", Label: "B"}, Known: true, Obs: Observation{Verdict: VerdictRenders, Date: "2026-08-22", Source: "s"}},
		{Entry: ManifestEntry{ID: "c", Label: "C"}, Known: true, Obs: Observation{Verdict: VerdictStub, Date: "2026-08-22", Source: "s"}},
		{Entry: ManifestEntry{ID: "d", Label: "D"}, Known: false},
	}
	out := Render(rows)
	if !strings.Contains(out, "RENDERS: 2") {
		t.Errorf("Render summary missing RENDERS: 2:\n%s", out)
	}
	if !strings.Contains(out, "STUB: 1") {
		t.Errorf("Render summary missing STUB: 1:\n%s", out)
	}
	if !strings.Contains(out, "could not measure: 1") {
		t.Errorf("Render summary missing could not measure: 1:\n%s", out)
	}
}

// TestRenderStaleIsItsOwnVerdictNotRenders is agent-tui#182's own
// mutation check: a route showing agent-tui#176's retained-last-good-data
// behaviour must count and print as STALE, not fold silently into
// RENDERS -- the exact distinction agent-tui#182 was filed to restore.
func TestRenderStaleIsItsOwnVerdictNotRenders(t *testing.T) {
	rows := []ResolvedRow{
		{Entry: ManifestEntry{ID: "agents", Label: "Agents"}, Known: true,
			Obs: Observation{Verdict: VerdictStale, Date: "2026-08-29", Source: "s",
				Notes: "showing last good data, age: 22s"}},
		{Entry: ManifestEntry{ID: "chat", Label: "Chat"}, Known: true,
			Obs: Observation{Verdict: VerdictRenders, Date: "2026-08-29", Source: "s"}},
	}
	out := Render(rows)
	if !strings.Contains(out, "| Agents | STALE |") {
		t.Errorf("Render output missing a STALE row for Agents:\n%s", out)
	}
	if !strings.Contains(out, "STALE: 1") {
		t.Errorf("Render summary missing STALE: 1:\n%s", out)
	}
	if !strings.Contains(out, "RENDERS: 1") {
		t.Errorf("Render summary missing RENDERS: 1 -- a STALE row must not also count as RENDERS:\n%s", out)
	}
}

// TestVerdictStaleValueIsStable pins the exact string persisted to disk
// (testdata/vhs/nav-walk/observations/*.jsonl) -- a future rename of the
// Go constant must not silently reflow every JSONL file already recorded
// with the old string.
func TestVerdictStaleValueIsStable(t *testing.T) {
	if VerdictStale != "STALE" {
		t.Fatalf("VerdictStale = %q, want %q", VerdictStale, "STALE")
	}
}
