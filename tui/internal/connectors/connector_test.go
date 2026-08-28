package connectors

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func byHarness(conns []Connection, harness string) Connection {
	for _, c := range conns {
		if c.Harness == harness {
			return c
		}
	}
	return Connection{}
}

// TestLoadClaudeOnlyConfirmsFileExists is loadClaude's own contract --
// pinned so a future change cannot start reading "statsigModel" or
// "additionalModelOptionsCache" as if either were a real per-user model
// setting (this file's own doc comment says why neither is).
func TestLoadClaudeOnlyConfirmsFileExists(t *testing.T) {
	path := writeFixture(t, "claude.json", `{"statsigModel":{"firstParty":"claude-sonnet-4-20250514"},"additionalModelOptionsCache":[{"value":"claude-fable-5"}]}`)
	conns, _ := Load(Paths{ClaudeConfig: path})
	c := byHarness(conns, HarnessClaude)
	if !c.Configured {
		t.Error("Configured = false, want true (the file exists)")
	}
	if c.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", c.Provider, "anthropic")
	}
	if c.DefaultModel != nil {
		t.Errorf("DefaultModel = %v, want nil -- neither statsigModel nor additionalModelOptionsCache is a real setting", *c.DefaultModel)
	}
}

func TestLoadClaudeMissingFileIsNotConfigured(t *testing.T) {
	conns, _ := Load(Paths{ClaudeConfig: filepath.Join(t.TempDir(), "does-not-exist.json")})
	c := byHarness(conns, HarnessClaude)
	if c.Configured {
		t.Error("Configured = true for a missing file, want false")
	}
}

// TestLoadCodexReadsTopLevelModel is a real fixture shaped like an actual
// ~/.codex/config.toml -- includes a [mcp_servers] section afterward to
// confirm the top-level line is found before any section header, not
// confused by one.
func TestLoadCodexReadsTopLevelModel(t *testing.T) {
	path := writeFixture(t, "config.toml", "model = \"gpt-5.6-terra\"\nmodel_reasoning_effort = 'medium'\n\n[mcp_servers]\n[mcp_servers.node_repl]\ncommand = \"node\"\n")
	conns, _ := Load(Paths{CodexConfig: path})
	c := byHarness(conns, HarnessCodex)
	if !c.Configured {
		t.Fatal("Configured = false, want true")
	}
	if c.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", c.Provider, "openai")
	}
	if c.DefaultModel == nil || *c.DefaultModel != "gpt-5.6-terra" {
		t.Errorf("DefaultModel = %v, want \"gpt-5.6-terra\"", c.DefaultModel)
	}
}

func TestLoadCodexMissingModelLineLeavesDefaultModelNil(t *testing.T) {
	path := writeFixture(t, "config.toml", "personality = 'pragmatic'\n")
	conns, _ := Load(Paths{CodexConfig: path})
	c := byHarness(conns, HarnessCodex)
	if !c.Configured {
		t.Fatal("Configured = false, want true (file exists, just no model= line)")
	}
	if c.DefaultModel != nil {
		t.Errorf("DefaultModel = %v, want nil", *c.DefaultModel)
	}
}

func TestLoadPiReadsProviderAndModel(t *testing.T) {
	path := writeFixture(t, "settings.json", `{"defaultProvider":"openai-codex","defaultModel":"gpt-5.5","theme":"dark"}`)
	conns, _ := Load(Paths{PiSettings: path})
	c := byHarness(conns, HarnessPi)
	if !c.Configured {
		t.Fatal("Configured = false, want true")
	}
	if c.Provider != "openai-codex" {
		t.Errorf("Provider = %q, want %q", c.Provider, "openai-codex")
	}
	if c.DefaultModel == nil || *c.DefaultModel != "gpt-5.5" {
		t.Errorf("DefaultModel = %v, want \"gpt-5.5\"", c.DefaultModel)
	}
}

// TestLoadCodexModelsParsesCache mirrors a real ~/.codex/models_cache.json
// shape (fetched_at/etag/client_version plus a models array).
func TestLoadCodexModelsParsesCache(t *testing.T) {
	path := writeFixture(t, "models_cache.json", `{
		"fetched_at": "2026-08-22T03:11:20Z",
		"models": [
			{"slug": "gpt-5.6-sol", "display_name": "GPT-5.6-Sol", "description": "Latest frontier agentic coding model."}
		]
	}`)
	_, models := Load(Paths{CodexModelsCache: path})
	if len(models) != 1 {
		t.Fatalf("models = %+v, want 1", models)
	}
	m := models[0]
	if m.Harness != HarnessCodex || m.Slug != "gpt-5.6-sol" || m.DisplayName != "GPT-5.6-Sol" {
		t.Errorf("models[0] = %+v", m)
	}
}

func TestLoadMissingPathsProduceNoErrorJustUnconfigured(t *testing.T) {
	conns, models := Load(Paths{})
	if len(conns) != 3 {
		t.Fatalf("conns = %+v, want 3 (one per harness, even with nothing to read)", conns)
	}
	for _, c := range conns {
		if c.Configured {
			t.Errorf("harness %q Configured = true with an empty path, want false", c.Harness)
		}
	}
	if models != nil {
		t.Errorf("models = %+v, want nil with no CodexModelsCache path", models)
	}
}

func TestLoadMalformedJSONDoesNotPanicOrConfuseConfigured(t *testing.T) {
	path := writeFixture(t, "settings.json", `not valid json {{{`)
	conns, _ := Load(Paths{PiSettings: path})
	c := byHarness(conns, HarnessPi)
	if !c.Configured {
		t.Error("Configured = false for an existing-but-malformed file, want true -- the file DOES exist")
	}
	if c.DefaultModel != nil || c.Provider != "" {
		t.Errorf("malformed JSON produced a value anyway: %+v", c)
	}
}
