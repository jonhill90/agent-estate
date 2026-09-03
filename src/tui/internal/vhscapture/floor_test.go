package vhscapture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPerTargetFloorsReturnsNilWhenNoSidecarExists(t *testing.T) {
	dir := t.TempDir()
	tape := filepath.Join(dir, "unmeasured.tape")
	if err := os.WriteFile(tape, []byte("Screenshot testdata/vhs/out/x.png\n"), 0o644); err != nil {
		t.Fatalf("write tape: %v", err)
	}
	floors, err := PerTargetFloors(tape)
	if err != nil {
		t.Fatalf("PerTargetFloors: %v", err)
	}
	if floors != nil {
		t.Fatalf("PerTargetFloors = %v, want nil (no sidecar recorded)", floors)
	}
}

func TestPerTargetFloorsParsesSidecarIgnoringCommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	tape := filepath.Join(dir, "knowledge-index.tape")
	sidecar := filepath.Join(dir, "knowledge-index.mincolors")
	content := "# measured 2026-09-03, see agent-estate#960\n" +
		"\n" +
		"knowledge-01-vault-list.png=110\n" +
		"knowledge-02-compiled-list.png=220\n"
	if err := os.WriteFile(sidecar, []byte(content), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	floors, err := PerTargetFloors(tape)
	if err != nil {
		t.Fatalf("PerTargetFloors: %v", err)
	}
	want := map[string]int{
		"knowledge-01-vault-list.png":    110,
		"knowledge-02-compiled-list.png": 220,
	}
	if len(floors) != len(want) {
		t.Fatalf("PerTargetFloors = %v, want %v", floors, want)
	}
	for k, v := range want {
		if floors[k] != v {
			t.Fatalf("PerTargetFloors[%q] = %d, want %d", k, floors[k], v)
		}
	}
}

func TestPerTargetFloorsRejectsAMalformedLine(t *testing.T) {
	dir := t.TempDir()
	tape := filepath.Join(dir, "bad.tape")
	sidecar := filepath.Join(dir, "bad.mincolors")
	if err := os.WriteFile(sidecar, []byte("this line has no equals sign\n"), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	if _, err := PerTargetFloors(tape); err == nil {
		t.Fatalf("PerTargetFloors: want error for a malformed sidecar line, got nil")
	}
}

func TestSidecarPathReplacesTapeExtension(t *testing.T) {
	got := SidecarPath("testdata/vhs/knowledge-index.tape")
	want := "testdata/vhs/knowledge-index.mincolors"
	if got != want {
		t.Fatalf("SidecarPath = %q, want %q", got, want)
	}
}

// TestGlobalFloorRejectsASparseSettledFrame_AndPerTargetFloorAcceptsIt is
// agent-estate#960 itself, reproduced as a mutation check: a sparse
// pane's genuine settled frame (measured directly against
// testdata/vhs/knowledge-index.tape: 219-446 colors) sits BELOW the
// busier testdata/vhs/agents-mode.tape's own measured partial/torn frame
// (263 colors) -- so no single global floor can accept the first while
// rejecting the second. A per-target floor recorded from the sparse
// tape's own evidence must accept it anyway.
func TestGlobalFloorRejectsASparseSettledFrame_AndPerTargetFloorAcceptsIt(t *testing.T) {
	const sparseSettled = 220   // testdata/vhs/knowledge-index.tape, measured
	const busyTapePartial = 263 // testdata/vhs/agents-mode.tape, measured
	const globalDefault = 1000  // cmd/vhscapture's own fallback default

	if !(sparseSettled < busyTapePartial && busyTapePartial < globalDefault) {
		t.Fatalf("evidence assumption broken: want sparseSettled(%d) < busyTapePartial(%d) < globalDefault(%d)", sparseSettled, busyTapePartial, globalDefault)
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "knowledge-01-vault-list.png")
	runTape := func() error {
		writeManyColorPNG(t, target, sparseSettled)
		return nil
	}

	// The global default alone -- the pre-agent-estate#960 behavior -- rejects this
	// genuine settled frame every time.
	if report, err := RunUntilSettled([]string{target}, UniformFloor(globalDefault), 2, runTape); err == nil || report.Settled {
		t.Fatalf("RunUntilSettled at the global default: want failure for a genuine %d-colour sparse frame, got settled=%v err=%v", sparseSettled, report.Settled, err)
	}

	// A per-target floor recorded from this tape's own measured evidence
	// (half its lowest observed settled count, comfortably above a
	// blank capture and still below every real observed value) accepts
	// it.
	perTarget := UniformFloor(sparseSettled / 2)
	report, err := RunUntilSettled([]string{target}, perTarget, 2, runTape)
	if err != nil {
		t.Fatalf("RunUntilSettled with a per-target floor: %v", err)
	}
	if !report.Settled {
		t.Fatalf("report.Settled = false, want true")
	}
}
