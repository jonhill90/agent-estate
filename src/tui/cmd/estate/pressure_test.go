package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

const wellFormedPressureLine = "load 0.14/core  free 4396MB  inflight 3  weekly budget 94% left\n"

// exitError runs a real subprocess that exits with the given code, so the
// tests below exercise a genuine *exec.ExitError rather than a hand-built
// stand-in -- the exact type buildPressureFetchFromRunner type-asserts on.
func exitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+string(rune('0'+code))).Run()
	if err == nil {
		t.Fatalf("exit %d unexpectedly succeeded", code)
	}
	if _, ok := err.(*exec.ExitError); !ok {
		t.Fatalf("exitError helper produced %T, want *exec.ExitError", err)
	}
	return err
}

// TestBuildPressureFetch_Within is `estate pressure` exiting 0 -- the daemon
// measured and allows more work. Reading.OK must be true and Reasons empty.
func TestBuildPressureFetch_Within(t *testing.T) {
	run := func() ([]byte, []byte, error) {
		return []byte(wellFormedPressureLine), nil, nil
	}
	fetch := buildPressureFetchFromRunner("estate", run)
	r, err := fetch()
	if err != nil {
		t.Fatalf("fetch() error = %v, want nil for exit 0", err)
	}
	if !r.OK {
		t.Error("OK = false, want true for exit 0")
	}
	if len(r.Reasons) != 0 {
		t.Errorf("Reasons = %v, want empty for exit 0", r.Reasons)
	}
	if r.InFlight != 3 || r.FreeMemMB != 4396 {
		t.Errorf("reading = %+v, not decoded from the fixture line", r)
	}
}

// TestBuildPressureFetch_Refusing is `estate pressure` exiting 1 -- a REAL
// measurement that says no. This must still be Present (nil error), never
// Unreadable: the figures on stdout were genuinely read, and OK=false with
// Reasons is the whole point of the gate failing closed.
func TestBuildPressureFetch_Refusing(t *testing.T) {
	run := func() ([]byte, []byte, error) {
		return []byte(wellFormedPressureLine),
			[]byte("refuse: load 3.50 at or above limit 3.00\nrefuse: 6 lanes in flight, cap is 6\n"),
			exitError(t, 1)
	}
	fetch := buildPressureFetchFromRunner("estate", run)
	r, err := fetch()
	if err != nil {
		t.Fatalf("fetch() error = %v, want nil -- exit 1 is a real reading, not blindness", err)
	}
	if r.OK {
		t.Error("OK = true, want false for exit 1")
	}
	want := []string{"load 3.50 at or above limit 3.00", "6 lanes in flight, cap is 6"}
	if len(r.Reasons) != len(want) || r.Reasons[0] != want[0] || r.Reasons[1] != want[1] {
		t.Errorf("Reasons = %v, want %v", r.Reasons, want)
	}
}

// TestBuildPressureFetch_BinaryMissing is the exec-failed-to-even-start
// case (a *exec.Error, not *exec.ExitError) -- Unreadable, never a zero
// reading.
func TestBuildPressureFetch_BinaryMissing(t *testing.T) {
	run := func() ([]byte, []byte, error) {
		return nil, nil, errors.New(`exec: "estate-does-not-exist": executable file not found in $PATH`)
	}
	fetch := buildPressureFetchFromRunner("estate-does-not-exist", run)
	_, err := fetch()
	if err == nil {
		t.Fatal("fetch() error = nil, want non-nil for a binary that cannot even start")
	}
}

// TestBuildPressureFetch_UnexpectedExit is exit 2 (src/estate's own
// "ledger unavailable" case) -- not one of the two outcomes this program
// understands, so it must refuse to present a verdict rather than guess
// which of within/refusing it means.
func TestBuildPressureFetch_UnexpectedExit(t *testing.T) {
	run := func() ([]byte, []byte, error) {
		return []byte(wellFormedPressureLine), []byte("estate: ledger unavailable: ..."), exitError(t, 2)
	}
	fetch := buildPressureFetchFromRunner("estate", run)
	_, err := fetch()
	if err == nil {
		t.Fatal("fetch() error = nil, want non-nil for an exit code this program does not understand")
	}
}

// TestBuildPressureFetch_UnparsableOutput pins the fail-closed behaviour
// for stdout that does not match src/estate's own printed shape at all --
// a future format change to that line must make this program say
// "unreadable," not silently render zeros.
func TestBuildPressureFetch_UnparsableOutput(t *testing.T) {
	run := func() ([]byte, []byte, error) {
		return []byte("some other binary entirely, not estate pressure\n"), nil, nil
	}
	fetch := buildPressureFetchFromRunner("estate", run)
	_, err := fetch()
	if err == nil {
		t.Fatal("fetch() error = nil, want non-nil for output that does not match the expected line shape")
	}
	if !strings.Contains(err.Error(), "could not parse") {
		t.Errorf("error = %q, want it to name a parse failure", err)
	}
}
