package shell

import (
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/src/tui/internal/estatus"
	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

// TestHomeViewNeverCallsPressureFetchDirectly is agent-estate#994's fix-pass
// regression guard. The finding: homeView used to call m.estateStatus()
// synchronously inside View(), and this PR's own estateStatus closure
// (cmd/estate/main.go, before the fix) ran `estate pressure` inline -- a
// wedged binary froze the whole shell for up to 15s on every render that
// touched Home. This pins the fix's contract: a WithEstatePressure fetch
// must NEVER be invoked from homeView/View() itself, only from Init/Update
// (doEstatePressureFetch). A fetch that panics or blocks forever if called
// proves View() never touches it.
func TestHomeViewNeverCallsPressureFetchDirectly(t *testing.T) {
	called := false
	m := Model{
		theme:         theme.Default,
		contentWidth:  92,
		contentHeight: 18,
		estateStatus: func() estatus.Status {
			return estatus.Status{}
		},
	}
	m = m.WithEstatePressure(func() (estatus.PressureReading, error) {
		called = true
		t.Fatal("homeView must never call the pressure fetch itself -- it belongs to Init/Update only")
		return estatus.PressureReading{}, nil
	})

	out := m.homeView()

	if called {
		t.Fatal("pressure fetch was invoked synchronously from homeView")
	}
	if !strings.Contains(out, "Pressure: measuring") {
		t.Errorf("homeView() = %q, want a pending \"measuring\" line before the async fetch has returned", out)
	}
}

// TestEstatePressureFetchedMsgUpdatesHomeView confirms the async result
// actually reaches homeView once Update processes it -- the other half of
// the contract TestHomeViewNeverCallsPressureFetchDirectly pins.
func TestEstatePressureFetchedMsgUpdatesHomeView(t *testing.T) {
	m := Model{
		theme:         theme.Default,
		contentWidth:  92,
		contentHeight: 18,
		estateStatus: func() estatus.Status {
			return estatus.Status{}
		},
	}
	m = m.WithEstatePressure(func() (estatus.PressureReading, error) {
		return estatus.PressureReading{LoadPerCore: 0.5, FreeMemMB: 1000, InFlight: 1, WeeklyRemaining: 80, OK: true}, nil
	})

	if !strings.Contains(m.homeView(), "Pressure: measuring") {
		t.Fatalf("before the first fetch result, homeView() = %q, want measuring", m.homeView())
	}

	next, _ := m.Update(estatePressureFetchedMsg(estatus.ReadPressure(m.estatePressureFetch)))
	nm := next.(Model)

	out := nm.homeView()
	if strings.Contains(out, "measuring") {
		t.Errorf("homeView() after a fetch result still shows measuring: %q", out)
	}
	if !strings.Contains(out, "within limits") {
		t.Errorf("homeView() = %q, want the real reading rendered", out)
	}
}

// TestWithEstatePressureNilLeavesEstateStatusUnchanged pins the no-op case:
// a caller (or every pre-agent-estate#994 test in this package) that never
// wires a pressure fetch in must see homeView render exactly what
// estateStatus() itself returned -- WithEstatePressure(nil) must not force
// a "measuring" line onto a Status that never asked for one.
func TestWithEstatePressureNilLeavesEstateStatusUnchanged(t *testing.T) {
	m := Model{
		theme:         theme.Default,
		contentWidth:  92,
		contentHeight: 18,
		estateStatus: func() estatus.Status {
			return estatus.Status{}
		},
	}
	if strings.Contains(m.homeView(), "Pressure") {
		t.Errorf("homeView() = %q, want no Pressure section with no fetch wired in", m.homeView())
	}
}
