// Package admin is docs/SPEC-shell.md's S11: "Admin section — Services,
// Profiles, Users, Dependencies, Settings. Read-only first."
//
// A scoping decision, made before any code, the same way S9
// (internal/mcpservers) and S10 (never built -- see its own PR history)
// required one: hill90's own Admin group (Services/Profiles/Users at
// /admin/*, Dependencies at /harness/tools) is backed entirely by
// hill90-app's Postgres-backed API -- a separate product's multi-tenant
// user/role/service inventory that agent-tui has no bridge to and no
// adapter-discipline entry for (AGENTS.md's own table). Rather than
// fabricate rows against that shape, or block the item the way S10 stayed
// blocked, this package renders S11's five named areas against real data
// THIS estate actually has:
//
//   - Services: the docker containers this estate's own MCP servers run
//     against (mcp-vibes-server, github-mcp-server, azure-mcp-server, ...
//     — real containers, confirmed via `docker ps` while building this
//     package) via a real `docker ps` read.
//   - Dependencies: the external CLI binaries this application and its
//     own MCP servers need on $PATH (gh, sqlite3, npx, tmux, docker,
//     python3 -- the same binaries this module's own -gh-bin/-sqlite-bin/
//     -ccusage-bin flags and internal/mcpservers' stdio servers already
//     name elsewhere in this codebase), reachability checked the same
//     real os/exec.LookPath way internal/mcpservers.WithReachability
//     already does for stdio MCP servers.
//   - Settings: this application's own persisted configuration --
//     internal/theme's theme.json, the one setting agent-tui actually
//     has today.
//   - Profiles, Users: this is a single-operator estate (one human, no
//     accounts, no roles) -- there is no real source for either concept
//     here, not a partially-missing column but an entirely absent one.
//     Snapshot.ProfilesNote/UsersNote say so explicitly rather than
//     rendering an empty, "checked, zero" list for a concept this estate
//     does not have at all.
//
// The five areas above map 1:1 onto S11's five nav.Build() routes
// ("admin-services", "admin-profiles", "admin-users", "dependencies",
// "settings" -- Section* in model.go), the same five hrefs hill90's own
// nav-items.ts carries (/admin/services, /admin/profiles, /admin/users,
// /harness/tools, /settings). agent-tui#150 found the content pane did not
// respond to which of the five was selected -- Model.View rendered all
// five stacked regardless of route. Given that fidelity requirement and
// this existing per-area split, the fix (Model.WithSection) narrows View
// to the one selected area rather than collapsing the five nav entries
// into one: each area was already real, distinct data this package
// fetches, not a title to invent.
package admin

// Service is one docker container this estate's MCP servers depend on.
type Service struct {
	Name   string
	Image  string
	Status string
}

// Dependency is one external CLI binary this application or its MCP
// servers need on $PATH. Reachable is nil until a LookPath-shaped check
// has run -- absence as a typed value (AGENTS.md), never a bare false,
// the same discipline internal/mcpservers.Server.Reachable already
// established for this exact shape of check.
type Dependency struct {
	Name      string
	Reachable *bool
}

// Setting is one persisted configuration value for this application.
type Setting struct {
	Name  string
	Value string
}

// Snapshot is everything the admin view renders in one read. Each of the
// three real sections carries its own error rather than the whole
// Snapshot failing when, say, the docker daemon is down but
// theme.json is perfectly readable -- three independent local reads, none
// of which should be able to blank out the other two (the same
// independence internal/agents.Model's optional TaskFetcher already has
// from its primary Fetcher).
type Snapshot struct {
	Services    []Service
	ServicesErr error

	Dependencies    []Dependency
	DependenciesErr error

	Settings    []Setting
	SettingsErr error

	// ProfilesNote and UsersNote are always non-empty -- see this
	// package's own doc comment for why neither concept has a real
	// source in this estate. Never rendered as a silently empty,
	// "checked, zero" list.
	ProfilesNote string
	UsersNote    string
}

// noProfilesNote and noUsersNote are the fixed, honest explanations
// Snapshot.ProfilesNote/UsersNote always carry -- constants, not computed,
// because the fact they state (this is a single-operator estate) is true
// regardless of what any fetch returns.
const (
	noProfilesNote = "no per-user profiles in this estate -- one operator, no accounts"
	noUsersNote    = "no user/role accounts in this estate -- one operator, no multi-tenant concept"
)

// Fetcher retrieves the current admin Snapshot -- the adapter seam
// (AGENTS.md) a Model is built around. NewFetcher is the one real
// implementation this repo ships; every test in this package builds a
// fake instead.
type Fetcher func() (Snapshot, error)

// DockerRunner runs `docker` with args and returns its stdout -- mirrors
// internal/board.LedgerRunner's own shape (a named func type over a raw
// os/exec call), so ServicesFetcher never calls os/exec directly, only
// through this seam.
type DockerRunner func(args []string) ([]byte, error)

// LookPath matches os/exec.LookPath's own signature exactly -- the same
// seam internal/mcpservers.LookPath already names for the identical real
// check, repeated here rather than imported because this package's seam
// is its own (AGENTS.md's adapter discipline), even though today's real
// implementation happens to be the same os/exec.LookPath both packages'
// callers pass in.
type LookPath func(file string) (string, error)

// KnownDependencies is the fixed list of external binaries this
// application or its MCP servers rely on, in the order they're checked.
// Not discovered at runtime -- this is a short, stable list this
// codebase's own flags and doc comments already name elsewhere
// (cmd/estate's -gh-bin/-sqlite-bin/-ccusage-bin defaults, and the
// docker/python3/npx commands internal/mcpservers' own real config
// entries use), so hardcoding it here is recording a known fact, not
// guessing one.
var KnownDependencies = []string{"gh", "sqlite3", "npx", "tmux", "docker", "python3"}

// NewFetcher composes the three real sections into one Fetcher.
// dockerRun/lookPath nil is a valid, silent "section not wired" default --
// the same "wiring is optional" convention WithTasks/WithReachability
// document elsewhere in this module -- rendering that section empty with
// no error, not a synthesized one.
func NewFetcher(dockerRun DockerRunner, lookPath LookPath, themeConfigPath string) Fetcher {
	return func() (Snapshot, error) {
		snap := Snapshot{
			ProfilesNote: noProfilesNote,
			UsersNote:    noUsersNote,
		}

		if dockerRun != nil {
			snap.Services, snap.ServicesErr = fetchServices(dockerRun)
		}
		if lookPath != nil {
			snap.Dependencies, snap.DependenciesErr = fetchDependencies(KnownDependencies, lookPath)
		}
		snap.Settings, snap.SettingsErr = fetchSettings(themeConfigPath)

		return snap, nil
	}
}
