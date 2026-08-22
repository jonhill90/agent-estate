// Package mcpservers is docs/SPEC-shell.md's S9: "List configured MCP
// servers, scope (global/project), and reachability." Named mcpservers,
// not mcp, deliberately: internal/mcp already exists in this module and
// is something entirely different (the JSON-RPC stdio client this whole
// application uses to talk to agent-supervisor) -- this package never
// imports it and is not related to it beyond the three letters.
//
// S9's own note: "lanes launch --strict-mcp-config (agent-tui#494), so
// this is about what a lane MAY be given, not what it inherits." That is
// a claim about CONFIGURATION, not about a live session's actual runtime
// environment -- Load (below) reads exactly the configuration Claude Code
// itself reads before it ever launches an agent, the same file
// `claude mcp list`/`claude mcp add` write to, never a lane's own runtime
// state (this package has no lane concept at all).
package mcpservers

import (
	"encoding/json"
	"os"
)

// Scope is where a server's configuration entry lives -- Claude Code's
// own precedence, global first (~/.claude.json's top-level mcpServers,
// available to every project) then project (the same file's
// projects[cwd].mcpServers, this one project only).
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

// Transport is how Claude Code reaches the server -- Type in its own
// config schema. Empty in the config's own JSON (the historical
// `claude mcp add` default, no --transport given) reads as TransportStdio
// -- the same inference Claude Code itself makes.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
	TransportSSE   Transport = "sse"
)

// Server is one configured MCP server entry. Reachable is nil unless
// WithReachability (below) has been run over it -- absence as a typed
// value (AGENTS.md), never a bare false: "we did not check" and "we
// checked and it's missing" are different facts.
type Server struct {
	Name      string
	Scope     Scope
	Transport Transport
	Command   string // TransportStdio
	URL       string // TransportHTTP, TransportSSE

	// Reachable is set only for TransportStdio servers, by
	// WithReachability -- see that function's own doc comment for why
	// TransportHTTP/TransportSSE are never live-probed and stay nil
	// forever.
	Reachable *bool
}

// rawServer mirrors Claude Code's own per-server JSON shape closely
// enough to read Name/Command/URL -- Args, Env and Headers exist in the
// real config but are not part of what S9 asks this view to show, so they
// are read (to keep json.Unmarshal from erroring on unknown-shaped input)
// but discarded.
type rawServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	URL     string   `json:"url"`
}

type rawProject struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

type rawConfig struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
	Projects   map[string]rawProject      `json:"projects"`
}

// Load reads configPath (Claude Code's own config file -- ~/.claude.json
// in a real run) and returns every server from its top-level mcpServers
// (ScopeGlobal) plus, when projectDir names an entry in its projects map,
// that project's own mcpServers (ScopeProject). Does NOT read a
// repo-committed .mcp.json -- Claude Code merges a third source from
// there (gated by enabledMcpjsonServers/disabledMcpjsonServers), which
// this function does not attempt; see this package's own README note (or
// its callers' doc comments) for that as a known, named gap, not a
// silently missed one.
func Load(configPath, projectDir string) ([]Server, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var cfg rawConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	var out []Server
	out = append(out, decodeScope(cfg.MCPServers, ScopeGlobal)...)
	if proj, ok := cfg.Projects[projectDir]; ok {
		out = append(out, decodeScope(proj.MCPServers, ScopeProject)...)
	}
	return out, nil
}

func decodeScope(raw map[string]json.RawMessage, scope Scope) []Server {
	var out []Server
	for name, msg := range raw {
		var rs rawServer
		if err := json.Unmarshal(msg, &rs); err != nil {
			// A server entry Claude Code itself could not have written --
			// skip rather than fail the whole Load over one bad entry.
			// Unlike internal/skills.Scan's ParseErr (a whole file this
			// package fully owns reading), this is a sub-object of a file
			// another program owns and writes continuously; silently
			// tolerating one malformed entry here is the safer default.
			continue
		}
		transport := Transport(rs.Type)
		if transport == "" {
			transport = TransportStdio
		}
		out = append(out, Server{
			Name:      name,
			Scope:     scope,
			Transport: transport,
			Command:   rs.Command,
			URL:       rs.URL,
		})
	}
	return out
}

// LookPath matches os/exec.LookPath's own signature exactly -- callers
// pass exec.LookPath itself, never a copy of its logic. Kept as a named
// type (not a raw func literal inline) so WithReachability's own
// signature reads as an adapter seam, the same discipline every other
// package's Fetcher-shaped type documents (AGENTS.md).
type LookPath func(file string) (string, error)

// WithReachability returns a copy of servers with Reachable set for every
// TransportStdio entry -- true when lookPath finds Command on $PATH,
// false otherwise. TransportHTTP/TransportSSE entries are left with
// Reachable == nil, always: confirming an HTTP(S) endpoint answers would
// mean this view making a live network call on every render/refresh
// tick, which is a materially different (and slower, and
// network-dependent) operation than internal/rail or internal/cost's own
// local reads -- S9's own framing ("what a lane MAY be given") is about
// configuration, not a live health check, so this deliberately does not
// attempt one. lookPath == nil leaves every Reachable untouched (nil) --
// the same "wiring is optional" convention WithTasks/WithSender document
// elsewhere in this module.
func WithReachability(servers []Server, lookPath LookPath) []Server {
	if lookPath == nil {
		return servers
	}
	out := make([]Server, len(servers))
	copy(out, servers)
	for i, s := range out {
		if s.Transport != TransportStdio {
			continue
		}
		_, err := lookPath(s.Command)
		reachable := err == nil
		out[i].Reachable = &reachable
	}
	return out
}
