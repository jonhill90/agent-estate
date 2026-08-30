// Package connectors is docs/SPEC-shell.md's S10: "Provider connections
// and models. Mirrors web Connect."
//
// Web hill90's own Connect > Connections/Models pages
// (hill90-app/services/ui/src/app/harness/connections/ConnectionsClient.tsx)
// are backed entirely by hill90's own server-side database -- provider API
// connections with stored keys, live validation status, a
// NextAuth-authenticated REST API. There is no local file, CLI, or MCP tool
// anywhere in this environment that exposes THAT data, and this repo's own
// docs/SPEC.md already states the reason on the record: "the hill90 1:1
// comparison is currently unfalsifiable -- no estate access to the web
// harness to compare against." Building against hill90's own backend would
// mean inventing an HTTP client with guessed auth against a service that
// stores provider API keys, against a system this repo has never had a
// seam to -- exactly what AGENTS.md's adapter discipline and "never commit
// secrets" rule warn against.
//
// What DOES exist locally, matching this module's other two config-file
// panes (internal/skills reads ~/.claude/skills' SKILL.md frontmatter,
// internal/mcpservers reads ~/.claude.json's mcpServers): each of the three
// harnesses ccusage/internal/cost already knows by name (claude/codex/pi)
// keeps its own local config naming which provider/model it is set to use.
// This package reads those three files -- never hill90, never a live
// network call -- and reports "provider connection" in the sense this
// estate can actually verify: is this harness's own CLI configured at all,
// and which model does its config say to use. Two of the three (codex, pi)
// have a real answer; the third (claude) does not, measured, not assumed --
// see Load's own doc comment.
package connectors

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

// Harness names match internal/cost.Harness.Name exactly (ccusage's own
// agent ids) -- this package reuses that vocabulary rather than inventing
// a second one for the same three CLIs.
const (
	HarnessClaude = "claude"
	HarnessCodex  = "codex"
	HarnessPi     = "pi"
)

// Connection is one harness's local configuration, read from disk. Provider
// is the backend API this harness actually calls -- "anthropic" and
// "openai" are self-evident from the CLI's own identity (Claude Code only
// ever calls Anthropic, Codex only ever calls OpenAI), so those two are
// constants, not read from anywhere; pi is the one harness in this trio
// that is itself a multi-provider router (its own settings.json has a
// literal "defaultProvider" field for exactly this reason), so pi's
// Provider is read, not assumed.
type Connection struct {
	Harness string
	// Provider is "" only if it could not be determined -- never a guess.
	Provider string
	// Configured reports whether this harness's own config file was found
	// on disk at all -- "is there anything here to read," independent of
	// whether DefaultModel could be extracted from it.
	Configured bool
	// DefaultModel is nil when this harness's config has no such field, or
	// the config itself does not exist. Never a fabricated value -- see
	// Load's own doc comment on why Claude's is always nil today.
	DefaultModel *string
}

// AvailableModel is one entry from a harness's own published model catalog
// -- today only Codex publishes one locally (models_cache.json, written by
// the Codex CLI itself from its own backend, refreshed on each run).
// Neither Claude Code's nor pi's local state has an equivalent catalog
// file; grepped both config directories for anything shaped like one
// (2026-08-22) and found none -- see Load's own doc comment.
type AvailableModel struct {
	Harness     string
	Slug        string
	DisplayName string
	Description string
}

// Paths names every file this package reads -- one field per harness, so
// a caller (cmd/estate, or a test) can point each at a fixture
// independently rather than this package hardcoding "~/.codex/..." itself.
// An empty path means "do not read this one" -- Load treats it exactly
// like a missing file (Connection.Configured == false), never an error.
type Paths struct {
	// ClaudeConfig is Claude Code's own global config -- read only to
	// confirm the file exists; it has no stable "current model" field to
	// extract (Load's own doc comment explains why) so DefaultModel for
	// this harness is always nil regardless of this file's contents.
	ClaudeConfig string
	// CodexConfig is Codex CLI's config.toml -- read for its top-level
	// `model = "..."` line.
	CodexConfig string
	// CodexModelsCache is Codex CLI's own models_cache.json -- the one
	// real model catalog this package can read, written by the CLI
	// itself from its backend on each run.
	CodexModelsCache string
	// PiSettings is pi's own settings.json -- read for
	// "defaultProvider"/"defaultModel".
	PiSettings string
}

// Load reads every path in p and returns one Connection per harness plus
// every AvailableModel Codex's own cache currently lists. A path that does
// not exist or cannot be read produces Configured: false for that harness
// (or no AvailableModel rows for Codex's cache) -- never an error for the
// whole call, since "one harness isn't installed on this machine" is an
// entirely ordinary, expected state, not a failure.
func Load(p Paths) ([]Connection, []AvailableModel) {
	conns := []Connection{
		loadClaude(p.ClaudeConfig),
		loadCodex(p.CodexConfig),
		loadPi(p.PiSettings),
	}
	models := loadCodexModels(p.CodexModelsCache)
	return conns, models
}

// loadClaude only ever confirms the file exists. Claude Code's own global
// config (~/.claude.json) has no stable, documented "current model" field
// -- the closest thing present is "statsigModel" (an internal feature-flag
// artifact keyed by deployment target, e.g. "bedrock"/"vertex", not a
// user-facing model choice) and "additionalModelOptionsCache" (a transient
// UI-picker cache, not a setting) -- neither is something this package
// will read as if it were a real setting; Claude Code picks its model per
// invocation (a CLI flag/session choice), not from a field in this file.
// Measured by inspecting a real ~/.claude.json, 2026-08-22 -- see this
// package's own test fixture for the exact shape found.
func loadClaude(path string) Connection {
	c := Connection{Harness: HarnessClaude, Provider: "anthropic"}
	if path == "" {
		return c
	}
	if _, err := os.Stat(path); err == nil {
		c.Configured = true
	}
	return c
}

// codexModelLineRE matches config.toml's own top-level `model = "..."`
// line -- a minimal, documented scan rather than a TOML library dependency
// this module does not otherwise need (the same tradeoff
// internal/skills.parseFrontmatter already documents for a comparably
// small, known field set). Anchored to the start of the line (^) so a
// `model = "..."` line nested under some OTHER [section] later in the
// file is not mistaken for the top-level one -- config.toml's own
// top-level fields always precede its first [section] header, so the
// first match scanning top-down is the one that matters. A key inside a
// value's own string content (e.g. "model = \"...\"" as descriptive text)
// is not a concern here: found nowhere in a real config.toml, and toml's
// own key=value line shape does not appear as prose.
var codexModelLineRE = regexp.MustCompile(`(?m)^model\s*=\s*"([^"]*)"`)

func loadCodex(path string) Connection {
	c := Connection{Harness: HarnessCodex, Provider: "openai"}
	if path == "" {
		return c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	c.Configured = true
	if m := codexModelLineRE.FindSubmatch(data); m != nil {
		model := string(m[1])
		c.DefaultModel = &model
	}
	return c
}

type piSettings struct {
	DefaultProvider string `json:"defaultProvider"`
	DefaultModel    string `json:"defaultModel"`
}

func loadPi(path string) Connection {
	c := Connection{Harness: HarnessPi}
	if path == "" {
		return c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	c.Configured = true
	var s piSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return c
	}
	if s.DefaultProvider != "" {
		c.Provider = s.DefaultProvider
	}
	if s.DefaultModel != "" {
		model := s.DefaultModel
		c.DefaultModel = &model
	}
	return c
}

type codexModelsCache struct {
	Models []struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
	} `json:"models"`
}

func loadCodexModels(path string) []AvailableModel {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cache codexModelsCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	out := make([]AvailableModel, 0, len(cache.Models))
	for _, m := range cache.Models {
		out = append(out, AvailableModel{
			Harness:     HarnessCodex,
			Slug:        m.Slug,
			DisplayName: m.DisplayName,
			Description: strings.TrimSpace(m.Description),
		})
	}
	return out
}
