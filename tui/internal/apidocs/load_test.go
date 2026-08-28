package apidocs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureSpec is shaped like the real document this pane reads
// (hill90-app/services/api/src/openapi/openapi.yaml): operations carrying
// `security: []` for public and `- bearerAuth: []` for authenticated, one
// path with several methods, and a path item with a non-operation sibling
// key (`parameters`) that must NOT become an endpoint.
const fixtureSpec = `openapi: 3.0.3
info:
  title: Fixture API
  description: a spec used only by this package's own tests
  version: 9.9.9
paths:
  /health:
    get:
      summary: Health check
      security: []
  /agents:
    post:
      summary: Create an agent
      tags: [agents]
      security:
        - bearerAuth: []
    get:
      summary: List agents
      security:
        - bearerAuth: []
  /agents/{id}:
    parameters:
      - name: id
        in: path
    delete:
      summary: Delete an agent
`

func writeSpec(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadReadsInfoAndEveryOperation(t *testing.T) {
	ref, err := Load(writeSpec(t, fixtureSpec))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if ref.Title != "Fixture API" || ref.Version != "9.9.9" || ref.OpenAPI != "3.0.3" {
		t.Errorf("Load() info = %q/%q/%q", ref.Title, ref.Version, ref.OpenAPI)
	}
	if ref.PathCount != 3 {
		t.Errorf("PathCount = %d, want 3", ref.PathCount)
	}
	if len(ref.Endpoints) != 4 {
		t.Fatalf("got %d endpoints, want 4 (the `parameters` key under /agents/{id} is not an operation): %+v",
			len(ref.Endpoints), ref.Endpoints)
	}
}

// TestLoadSortsByPathThenMethodOrder pins the read order a human gets: the
// document lists post before get under /agents, and this projection must
// not.
func TestLoadSortsByPathThenMethodOrder(t *testing.T) {
	ref, err := Load(writeSpec(t, fixtureSpec))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	var got []string
	for _, e := range ref.Endpoints {
		got = append(got, e.Method+" "+e.Path)
	}
	want := []string{"GET /agents", "POST /agents", "DELETE /agents/{id}", "GET /health"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// TestLoadKeepsAuthsThreeStates is the "absence is a typed value"
// convention (AGENTS.md) applied to OpenAPI's own distinction: `security:
// []` is a claim that an operation is public, and no security key at all
// is no claim either way. A bool could not tell them apart.
func TestLoadKeepsAuthsThreeStates(t *testing.T) {
	ref, err := Load(writeSpec(t, fixtureSpec))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	byKey := map[string]*bool{}
	for _, e := range ref.Endpoints {
		byKey[e.Method+" "+e.Path] = e.Auth
	}

	public := byKey["GET /health"]
	if public == nil || *public {
		t.Errorf("GET /health auth = %v, want an explicit false (security: [])", public)
	}
	secured := byKey["POST /agents"]
	if secured == nil || !*secured {
		t.Errorf("POST /agents auth = %v, want an explicit true", secured)
	}
	if unstated := byKey["DELETE /agents/{id}"]; unstated != nil {
		t.Errorf("DELETE /agents/{id} auth = %v, want nil -- the document states nothing", *unstated)
	}
}

func TestLoadSummariesAndTagsComeFromTheDocument(t *testing.T) {
	ref, err := Load(writeSpec(t, fixtureSpec))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	for _, e := range ref.Endpoints {
		if e.Method == "POST" && e.Path == "/agents" {
			if e.Summary != "Create an agent" {
				t.Errorf("summary = %q", e.Summary)
			}
			if len(e.Tags) != 1 || e.Tags[0] != "agents" {
				t.Errorf("tags = %v", e.Tags)
			}
		}
	}
}

// TestLoadMissingFileNamesThePath is the difference between "we could not
// read the spec" and "the spec has no endpoints" -- the view prints this
// error, so it has to identify the file.
func TestLoadMissingFileNamesThePath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	_, err := Load(missing)
	if err == nil {
		t.Fatal("Load() of a missing file returned nil error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not name the path it tried", err)
	}
}

func TestLoadEmptyPathIsAnError(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Fatal("Load(\"\") returned nil error")
	}
}

// TestLoadRejectsANonOpenAPIFile: a readable YAML file that is not a spec
// must not render as an API with zero endpoints, which would look like a
// real (empty) reference.
func TestLoadRejectsANonOpenAPIFile(t *testing.T) {
	path := writeSpec(t, "some: yaml\nbut: not a spec\n")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() of a non-OpenAPI YAML file returned nil error")
	}
}

// TestLoadAgainstTheRealSpecWhenAvailable parses the document this pane
// actually ships to read, when a hill90-app checkout is present. It skips
// otherwise -- this module must build and test standalone with no sibling
// repo, the same rule internal/lane/states_lanessh_test.go follows for
// $AGENT_SUPERVISOR_REPO (AGENTS.md, "Running the tests").
//
// A fixture proves the parser handles the shapes the fixture has. Only
// this proves it handles the ~5,600-line document a user will point it at.
func TestLoadAgainstTheRealSpecWhenAvailable(t *testing.T) {
	repo := os.Getenv("HILL90_APP_REPO")
	if repo == "" {
		t.Skip("HILL90_APP_REPO not set -- no hill90-app checkout to read")
	}
	path := filepath.Join(repo, "services", "api", "src", "openapi", "openapi.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("%s not present: %v", path, err)
	}

	ref, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s) error: %v", path, err)
	}
	if ref.Title == "" || ref.Version == "" {
		t.Errorf("real spec parsed with no title/version: %+v", ref)
	}
	if len(ref.Endpoints) == 0 || ref.PathCount == 0 {
		t.Fatalf("real spec parsed to %d operations across %d paths -- a silently empty parse is the failure this test exists to catch",
			len(ref.Endpoints), ref.PathCount)
	}
	for _, e := range ref.Endpoints {
		if e.Method == "" || e.Path == "" {
			t.Fatalf("real spec produced an endpoint with an empty method/path: %+v", e)
		}
	}
}
