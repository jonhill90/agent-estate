package admin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
	fetch := NewFetcher(nil, nil, filepath.Join(t.TempDir(), "theme.json"))
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
	fetch := NewFetcher(nil, nil, filepath.Join(t.TempDir(), "theme.json"))
	snap, _ := fetch()
	if snap.ProfilesNote == "" {
		t.Error("ProfilesNote is empty")
	}
	if snap.UsersNote == "" {
		t.Error("UsersNote is empty")
	}
}

func TestNewFetcher_OneSectionsErrorDoesNotBlankTheOthers(t *testing.T) {
	dockerRun := func(args []string) ([]byte, error) { return nil, errors.New("docker: command not found") }
	lookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	fetch := NewFetcher(dockerRun, lookPath, filepath.Join(t.TempDir(), "theme.json"))

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
