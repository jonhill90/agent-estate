package admin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDockerPS_ThreeFieldLines(t *testing.T) {
	out := []byte("mcp-vibes-server\tvibes:latest\tUp 3 days\ngithub-mcp-server\tghcr.io/gh/mcp\tExited (0) 2 hours ago\n")
	got := parseDockerPS(out)
	if len(got) != 2 {
		t.Fatalf("parseDockerPS = %+v, want 2 rows", got)
	}
	if got[0].Name != "mcp-vibes-server" || got[0].Image != "vibes:latest" || got[0].Status != "Up 3 days" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Status != "Exited (0) 2 hours ago" {
		t.Errorf("got[1] = %+v", got[1])
	}
}

func TestParseDockerPS_EmptyOutputIsEmptyNotNilPanic(t *testing.T) {
	got := parseDockerPS([]byte(""))
	if len(got) != 0 {
		t.Fatalf("parseDockerPS(\"\") = %+v, want empty", got)
	}
}

func TestFetchServices_RunnerErrorIsVisible(t *testing.T) {
	run := func(args []string) ([]byte, error) { return nil, errors.New("docker daemon not running") }
	_, err := fetchServices(run)
	if err == nil {
		t.Fatal("fetchServices with a failing runner returned nil error")
	}
}

func TestFetchDependencies_RealAnswerPerName(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "found" {
			return "/usr/bin/found", nil
		}
		return "", errors.New("not found")
	}
	got, err := fetchDependencies([]string{"found", "missing"}, lookPath)
	if err != nil {
		t.Fatalf("fetchDependencies error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v, want 2 entries", got)
	}
	if got[0].Reachable == nil || !*got[0].Reachable {
		t.Errorf("found.Reachable = %v, want true", got[0].Reachable)
	}
	if got[1].Reachable == nil || *got[1].Reachable {
		t.Errorf("missing.Reachable = %v, want false", got[1].Reachable)
	}
}

func TestFetchDependencies_NeverLeavesAnEntryNil(t *testing.T) {
	got, _ := fetchDependencies(KnownDependencies, func(string) (string, error) { return "", errors.New("nope") })
	for _, d := range got {
		if d.Reachable == nil {
			t.Errorf("%s.Reachable is nil, want a real answer for every known dependency", d.Name)
		}
	}
}

func TestFetchSettings_MissingConfigIsDefaultNotError(t *testing.T) {
	settings, err := fetchSettings(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("fetchSettings error: %v, want nil (missing config is theme.Load's own honest default)", err)
	}
	if len(settings) == 0 {
		t.Fatal("fetchSettings returned no settings even for the default theme")
	}
}

func TestFetchSettings_MalformedConfigSurfacesAsNotice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings, err := fetchSettings(path)
	if err != nil {
		t.Fatalf("fetchSettings error: %v", err)
	}
	found := false
	for _, s := range settings {
		if s.Name == "theme notice" {
			found = true
		}
	}
	if !found {
		t.Fatalf("settings = %+v, want a \"theme notice\" entry for a malformed config", settings)
	}
}

func TestNewFetcher_NilDockerRunAndLookPathLeaveThoseSectionsEmpty(t *testing.T) {
	fetch := NewFetcher(nil, nil, nil, filepath.Join(t.TempDir(), "theme.json"))
	snap, err := fetch()
	if err != nil {
		t.Fatalf("fetch error: %v", err)
	}
	if snap.Services != nil || snap.ServicesErr != nil {
		t.Errorf("Services = %+v, ServicesErr = %v, want both zero when dockerRun is nil", snap.Services, snap.ServicesErr)
	}
	if snap.Dependencies != nil || snap.DependenciesErr != nil {
		t.Errorf("Dependencies = %+v, DependenciesErr = %v, want both zero when lookPath is nil", snap.Dependencies, snap.DependenciesErr)
	}
	if len(snap.Settings) == 0 {
		t.Error("Settings empty -- theme is always wired, unlike docker/lookPath")
	}
}

// TestNewFetcher_ProfilesAndUsersNotesAreAlwaysSet pins the whole point of
// this package's scoping decision: these two sections must never look
// like an empty, checked list.
func TestNewFetcher_ProfilesAndUsersNotesAreAlwaysSet(t *testing.T) {
	fetch := NewFetcher(nil, nil, nil, filepath.Join(t.TempDir(), "theme.json"))
	snap, _ := fetch()
	if snap.ProfilesNote == "" {
		t.Error("ProfilesNote is empty")
	}
	if snap.UsersNote == "" {
		t.Error("UsersNote is empty")
	}
}

// TestFetchKeychain_ReadableRendersTrue is this test's positive case: a
// probe that ran and read the item -- readable.
func TestFetchKeychain_ReadableRendersTrue(t *testing.T) {
	probe := func() (bool, error) { return true, nil }
	got := fetchKeychain(probe)
	if got.Name != keychainRowName {
		t.Errorf("Name = %q, want %q", got.Name, keychainRowName)
	}
	if got.Reachable == nil || !*got.Reachable {
		t.Errorf("Reachable = %v, want true", got.Reachable)
	}
}

// TestFetchKeychain_DefiniteRefusalRendersFalse is this test's mutation
// pair to the case above: a probe that ran to completion and got a
// definite refusal (locked, denied, item missing) -- locked-or-denied,
// distinct from "could not determine" below.
func TestFetchKeychain_DefiniteRefusalRendersFalse(t *testing.T) {
	probe := func() (bool, error) { return false, nil }
	got := fetchKeychain(probe)
	if got.Reachable == nil || *got.Reachable {
		t.Errorf("Reachable = %v, want false", got.Reachable)
	}
}

// TestFetchKeychain_ProbeErrorRendersCouldNotDetermine is the THIRD state
// this issue's whole point rests on: a probe that could not itself
// produce an answer (timeout, could not start) must render distinctly
// from a definite refusal -- collapsing the two reproduces the exact bug
// agent-tui#163 already fixed once for the skills eval store.
func TestFetchKeychain_ProbeErrorRendersCouldNotDetermine(t *testing.T) {
	probe := func() (bool, error) { return false, errors.New("context deadline exceeded") }
	got := fetchKeychain(probe)
	if got.Reachable != nil {
		t.Errorf("Reachable = %v, want nil (could not determine)", *got.Reachable)
	}
}

// TestFetchKeychain_ProbeErrorTextNeverRendered pins "do not leak the
// credential or its contents into the pane, logs, or an error string" --
// even the probe's own error text (which could itself describe what is
// or is not in the keychain) must never surface on the Dependency row.
func TestFetchKeychain_ProbeErrorTextNeverRendered(t *testing.T) {
	probe := func() (bool, error) { return false, errors.New("very secret detail") }
	got := fetchKeychain(probe)
	if got.Reachable != nil {
		t.Fatalf("Reachable = %v, want nil", *got.Reachable)
	}
	// Dependency has no field an error string could even reach other than
	// Name -- assert that stays fixed too, not silently repurposed.
	if got.Name != keychainRowName {
		t.Errorf("Name = %q, want the fixed row name, not the probe's own error text", got.Name)
	}
}

func TestNewFetcher_KeychainProbeAppendsARowToDependencies(t *testing.T) {
	lookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	readable := true
	probe := func() (bool, error) { return readable, nil }

	fetch := NewFetcher(nil, lookPath, probe, filepath.Join(t.TempDir(), "theme.json"))
	snap, err := fetch()
	if err != nil {
		t.Fatalf("fetch error: %v", err)
	}
	if len(snap.Dependencies) != len(KnownDependencies)+1 {
		t.Fatalf("Dependencies = %+v, want %d known deps + 1 keychain row", snap.Dependencies, len(KnownDependencies))
	}
	last := snap.Dependencies[len(snap.Dependencies)-1]
	if last.Name != keychainRowName || last.Reachable == nil || !*last.Reachable {
		t.Errorf("last Dependency = %+v, want the keychain row, readable", last)
	}
}

func TestNewFetcher_NilKeychainProbeAddsNoRow(t *testing.T) {
	lookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	fetch := NewFetcher(nil, lookPath, nil, filepath.Join(t.TempDir(), "theme.json"))
	snap, _ := fetch()
	for _, d := range snap.Dependencies {
		if d.Name == keychainRowName {
			t.Fatalf("Dependencies = %+v, want no keychain row when keychainProbe is nil", snap.Dependencies)
		}
	}
}

// TestExecKeychainProbe_MissingItemIsADefiniteRefusalNotAHang simulates
// the "locked-or-denied" state without touching any real keychain: a
// service name that cannot exist gets `security`'s own fast, definite
// "not found" exit, never a block. This is the read-side of this
// change's mutation check -- see TestExecKeychainProbe_TimeoutIsCouldNotDetermine
// for the other direction.
func TestExecKeychainProbe_MissingItemIsADefiniteRefusalNotAHang(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("security binary not present on this host")
	}
	probe := ExecKeychainProbe("agent-tui-test-service-does-not-exist-149", 5*time.Second)
	readable, err := probe()
	if err != nil {
		t.Fatalf("probe error = %v, want a definite (false, nil) refusal for a missing item", err)
	}
	if readable {
		t.Fatalf("readable = true for a service name that cannot exist")
	}
}

// TestExecKeychainProbe_TimeoutIsCouldNotDetermine simulates the
// "keychain blocks instead of failing" case from the outage this issue is
// grounded in, without needing an actually locked keychain: an
// impossibly short timeout forces ctx.Err() regardless of how fast the
// underlying command would otherwise answer.
func TestExecKeychainProbe_TimeoutIsCouldNotDetermine(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("security binary not present on this host")
	}
	probe := ExecKeychainProbe("agent-tui-test-service-149", 1*time.Nanosecond)
	_, err := probe()
	if err == nil {
		t.Fatal("probe error = nil, want a timeout error with a 1ns budget")
	}
}

func TestNewFetcher_OneSectionsErrorDoesNotBlankTheOthers(t *testing.T) {
	dockerRun := func(args []string) ([]byte, error) { return nil, errors.New("docker: command not found") }
	lookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	fetch := NewFetcher(dockerRun, lookPath, nil, filepath.Join(t.TempDir(), "theme.json"))

	snap, err := fetch()
	if err != nil {
		t.Fatalf("fetch error: %v", err)
	}
	if snap.ServicesErr == nil {
		t.Error("ServicesErr is nil, want the docker runner's error surfaced")
	}
	if snap.DependenciesErr != nil || len(snap.Dependencies) != len(KnownDependencies) {
		t.Errorf("Dependencies = %+v, err = %v -- a Services failure must not blank this section", snap.Dependencies, snap.DependenciesErr)
	}
	if len(snap.Settings) == 0 {
		t.Error("Settings empty -- a Services failure must not blank this section either")
	}
}
