// Package secrets is jonhill90/agent-tui#101's decision, implemented: the
// Connect group's Secrets nav route, showing exactly levels 1-4 of the
// exposure scale that issue laid out (existence, key name + vault path +
// consuming services, age, last rotation) and never level 5 (the secret's
// actual value). See Key and Rotation's own doc comments for how each
// level maps to a field -- level 5 is not a display choice made at render
// time, it is a type that does not exist anywhere in this package.
//
// Source: hill90-app's platform/vault/secrets-schema.yaml, the exact
// declarative, credential-free file services/api/src/routes/secrets.ts
// parses to render hill90-app's own /admin/secrets inventory -- that
// route's own doc comment already states the boundary agent-tui#101 asked
// this repo to match or narrow: "Secret values are write-only -- the read
// endpoint returns key names, never values." This package reads the
// identical file, never hill90-app's live OpenBao deployment -- the same
// "read the checked-in declarative file, not the live authenticated
// backend" choice internal/apidocs already made for the OpenAPI document,
// and the same reasoning internal/connectors' own doc comment gives for
// declining to invent an HTTP client with guessed auth against a live
// hill90-app service this repo has no seam to.
package secrets

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rotation is a secret's age and last-known-rotation -- levels 3-4 of
// agent-tui#101's exposure scale. OpenBao's KV v2 API can answer this
// without ever reading a value: GET /v1/secret/metadata/<path> returns
// per-version created_time, confirmed against hill90-app's own
// services/api/src/helpers/vault-client.ts, which already decodes a
// metadata.created_time field for its internal merge-on-write path but
// exposes it through no route today. Answering it for real means a live,
// authenticated call to hill90-app's OpenBao deployment with a vault
// token -- exactly the seam internal/connectors' own doc comment declines
// to invent for Connections/Models ("inventing an HTTP client with
// guessed auth ... is exactly what AGENTS.md's adapter discipline and
// 'never commit secrets' rule warn against"). Levels 1-2 need no such
// call -- secrets-schema.yaml is a plain, credential-free file already
// checked into hill90-app's own repo -- so Known is true for Key.Name/
// Consumers always, but stays false here, always, until a real
// credential-free seam to OpenBao's metadata exists. Never fabricated
// just to fill agent-tui#101's ceiling.
type Rotation struct {
	Known        bool
	AgeDays      int
	LastRotation string // RFC3339; meaningful only when Known
}

// Key is one secret name at a vault path, plus which services consume it
// -- levels 1-2 of agent-tui#101's scale. Consumers is schema.yaml's own
// compose_refs reduced to service names, the same reduction
// secrets.ts's extractService() applies for hill90-app's own admin page.
type Key struct {
	Name      string
	Consumers []string
	Rotation  Rotation
}

// Path groups every Key that shares a vault path, matching schema.yaml's
// own grouping and secrets.ts's groupByPath -- Load performs the
// identical grouping over the identical file.
type Path struct {
	VaultPath string
	Keys      []Key
}

// Inventory is one Load's worth of the schema file, projected for this
// pane. ApproleServices is schema.yaml's own vault_approle_services list
// -- which services hold an AppRole into this vault at all, independent
// of any one key.
type Inventory struct {
	SourcePath      string
	Paths           []Path
	TotalKeys       int
	ApproleServices []string
}

type schemaEntry struct {
	Key         string   `yaml:"key"`
	VaultPath   string   `yaml:"vault_path"`
	ComposeRefs []string `yaml:"compose_refs"`
}

type schemaDoc struct {
	RuntimeSecrets       []schemaEntry `yaml:"runtime_secrets"`
	VaultApproleServices []string      `yaml:"vault_approle_services"`
}

// composeRefRE mirrors secrets.ts's extractService(): a compose filename
// like docker-compose.api.yml reduces to "api"; anything not matching
// that shape is left as-is rather than dropped, the same fallback
// extractService takes.
var composeRefRE = regexp.MustCompile(`docker-compose\.(.+)\.yml`)

func extractService(ref string) string {
	if m := composeRefRE.FindStringSubmatch(ref); m != nil {
		return m[1]
	}
	return ref
}

// Load reads and projects the secrets schema at path. An unreadable or
// unparseable file is an error naming the path -- the view renders that
// text verbatim rather than an empty inventory, the same distinction
// internal/apidocs.Load draws between "could not read the spec" and "the
// spec has no endpoints."
func Load(path string) (Inventory, error) {
	if strings.TrimSpace(path) == "" {
		return Inventory{}, fmt.Errorf("no secrets schema path configured")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Inventory{}, fmt.Errorf("read %s: %w", path, err)
	}
	var doc schemaDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Inventory{}, fmt.Errorf("parse %s: %w", path, err)
	}

	grouped := map[string][]Key{}
	var order []string
	for _, e := range doc.RuntimeSecrets {
		if _, ok := grouped[e.VaultPath]; !ok {
			order = append(order, e.VaultPath)
		}
		var consumers []string
		seen := map[string]bool{}
		for _, ref := range e.ComposeRefs {
			svc := extractService(ref)
			if !seen[svc] {
				seen[svc] = true
				consumers = append(consumers, svc)
			}
		}
		// Rotation is never populated from this file -- schema.yaml
		// describes layout, not version history. Left at its zero value
		// (Known: false), per this type's own doc comment.
		grouped[e.VaultPath] = append(grouped[e.VaultPath], Key{Name: e.Key, Consumers: consumers})
	}
	sort.Strings(order)

	inv := Inventory{
		SourcePath:      path,
		TotalKeys:       len(doc.RuntimeSecrets),
		ApproleServices: doc.VaultApproleServices,
	}
	for _, p := range order {
		inv.Paths = append(inv.Paths, Path{VaultPath: p, Keys: grouped[p]})
	}
	return inv, nil
}
