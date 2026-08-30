package mcpservers

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeConfig(t *testing.T, json string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude.json")
	if err := os.WriteFile(path, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const fixtureConfig = `{
  "mcpServers": {
    "context7": {"type": "http", "url": "https://mcp.context7.com/mcp"},
    "supervisor": {"command": "python3", "args": ["mcp_server.py"]}
  },
  "projects": {
    "/repo/a": {
      "mcpServers": {
        "obsidian": {"type": "stdio", "command": "python", "args": ["adapter.py"]}
      }
    },
    "/repo/b": {
      "mcpServers": {
        "other": {"command": "whatever"}
      }
    }
  }
}`

func TestLoadReadsGlobalServers(t *testing.T) {
	path := writeConfig(t, fixtureConfig)
	got, err := Load(path, "/repo/nonexistent")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Load() = %+v, want 2 global servers (no matching project)", got)
	}
	byName := map[string]Server{}
	for _, s := range got {
		byName[s.Name] = s
	}
	if s := byName["context7"]; s.Transport != TransportHTTP || s.URL != "https://mcp.context7.com/mcp" || s.Scope != ScopeGlobal {
		t.Errorf("context7 = %+v", s)
	}
	if s := byName["supervisor"]; s.Transport != TransportStdio || s.Command != "python3" || s.Scope != ScopeGlobal {
		t.Errorf("supervisor = %+v", s)
	}
}

// TestLoadDefaultsMissingTypeToStdio matches Claude Code's own historical
// convention: a server with a command and no explicit "type" is stdio.
func TestLoadDefaultsMissingTypeToStdio(t *testing.T) {
	path := writeConfig(t, fixtureConfig)
	got, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	for _, s := range got {
		if s.Name == "supervisor" && s.Transport != TransportStdio {
			t.Errorf("supervisor.Transport = %q, want %q", s.Transport, TransportStdio)
		}
	}
}

// TestLoadIncludesMatchingProjectServers is the ScopeProject half --
// only the NAMED project's own servers are included, never another
// project's.
func TestLoadIncludesMatchingProjectServers(t *testing.T) {
	path := writeConfig(t, fixtureConfig)
	got, err := Load(path, "/repo/a")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Load() = %+v, want 2 global + 1 project (\"obsidian\")", got)
	}
	var found bool
	for _, s := range got {
		if s.Name == "obsidian" {
			found = true
			if s.Scope != ScopeProject {
				t.Errorf("obsidian.Scope = %q, want %q", s.Scope, ScopeProject)
			}
		}
		if s.Name == "other" {
			t.Error("Load(path, \"/repo/a\") included /repo/b's own server \"other\"")
		}
	}
	if !found {
		t.Error("obsidian (a real /repo/a project server) not found in Load's output")
	}
}

func TestLoadMissingConfigFileIsAnError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"), "")
	if err == nil {
		t.Fatal("Load() on a missing file returned no error")
	}
}

func TestLoadMalformedServerEntrySkippedNotFatal(t *testing.T) {
	path := writeConfig(t, `{"mcpServers": {"good": {"command": "x"}, "bad": "not an object"}}`)
	got, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("Load() = %+v, want exactly [\"good\"]", got)
	}
}

// TestWithReachabilityChecksOnlyStdio is WithReachability's own contract:
// TransportHTTP/TransportSSE never get a Reachable value, even when a
// lookPath IS wired in, because they were never looked up at all.
func TestWithReachabilityChecksOnlyStdio(t *testing.T) {
	servers := []Server{
		{Name: "found", Transport: TransportStdio, Command: "found-bin"},
		{Name: "missing", Transport: TransportStdio, Command: "missing-bin"},
		{Name: "web", Transport: TransportHTTP, URL: "https://example.com"},
	}
	lookPath := func(name string) (string, error) {
		if name == "found-bin" {
			return "/usr/bin/found-bin", nil
		}
		return "", errors.New("not found")
	}

	got := WithReachability(servers, lookPath)
	byName := map[string]Server{}
	for _, s := range got {
		byName[s.Name] = s
	}

	if byName["found"].Reachable == nil || !*byName["found"].Reachable {
		t.Errorf("found.Reachable = %v, want true", byName["found"].Reachable)
	}
	if byName["missing"].Reachable == nil || *byName["missing"].Reachable {
		t.Errorf("missing.Reachable = %v, want false", byName["missing"].Reachable)
	}
	if byName["web"].Reachable != nil {
		t.Errorf("web (TransportHTTP).Reachable = %v, want nil -- never live-probed", *byName["web"].Reachable)
	}
}

// TestWithReachabilityNilLookPathLeavesEveryReachableNil is the "wiring is
// optional" default.
func TestWithReachabilityNilLookPathLeavesEveryReachableNil(t *testing.T) {
	servers := []Server{{Name: "s", Transport: TransportStdio, Command: "x"}}
	got := WithReachability(servers, nil)
	if !reflect.DeepEqual(got, servers) {
		t.Errorf("WithReachability(servers, nil) = %+v, want servers unchanged", got)
	}
}

func TestNewFetcherComposesLoadAndReachability(t *testing.T) {
	path := writeConfig(t, `{"mcpServers": {"supervisor": {"command": "definitely-not-a-real-binary-xyz"}}}`)
	fetch := NewFetcher(path, "", func(string) (string, error) { return "", errors.New("nope") })

	got, err := fetch()
	if err != nil {
		t.Fatalf("fetch() error: %v", err)
	}
	if len(got) != 1 || got[0].Reachable == nil || *got[0].Reachable {
		t.Fatalf("fetch() = %+v, want one unreachable stdio server", got)
	}
}
