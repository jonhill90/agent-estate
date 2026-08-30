package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtureSchema is shaped like hill90-app's real
// platform/vault/secrets-schema.yaml: two keys sharing one vault_path
// (grouping must merge them), one key at a path of its own, and
// compose_refs that repeat a service across two files (dedup must not
// list "api" twice for DB_PASSWORD).
const fixtureSchema = `vault_approle_services:
  - api
  - ai

runtime_secrets:
  - key: DB_USER
    vault_path: secret/shared/database
    compose_refs:
      - docker-compose.db.yml
      - docker-compose.api.yml
  - key: DB_PASSWORD
    vault_path: secret/shared/database
    compose_refs:
      - docker-compose.db.yml
      - docker-compose.api.yml
  - key: ANTHROPIC_API_KEY
    vault_path: secret/ai/config
    compose_refs:
      - docker-compose.ai.yml
`

func writeSchema(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets-schema.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadGroupsKeysByVaultPath(t *testing.T) {
	inv, err := Load(writeSchema(t, fixtureSchema))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if inv.TotalKeys != 3 {
		t.Errorf("TotalKeys = %d, want 3", inv.TotalKeys)
	}
	if len(inv.Paths) != 2 {
		t.Fatalf("got %d paths, want 2: %+v", len(inv.Paths), inv.Paths)
	}
	// Sorted: "secret/ai/config" < "secret/shared/database".
	if inv.Paths[0].VaultPath != "secret/ai/config" {
		t.Errorf("Paths[0] = %q, want secret/ai/config", inv.Paths[0].VaultPath)
	}
	dbPath := inv.Paths[1]
	if dbPath.VaultPath != "secret/shared/database" || len(dbPath.Keys) != 2 {
		t.Fatalf("Paths[1] = %+v, want secret/shared/database with 2 keys", dbPath)
	}
}

func TestLoadDedupsConsumersPerKey(t *testing.T) {
	inv, err := Load(writeSchema(t, fixtureSchema))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	for _, p := range inv.Paths {
		if p.VaultPath != "secret/shared/database" {
			continue
		}
		for _, k := range p.Keys {
			if k.Name != "DB_USER" {
				continue
			}
			if len(k.Consumers) != 2 || k.Consumers[0] != "db" || k.Consumers[1] != "api" {
				t.Errorf("DB_USER.Consumers = %v, want [db api]", k.Consumers)
			}
		}
	}
}

// TestLoadNeverPopulatesRotation pins agent-tui#101's decision at the data
// layer, not just the view: schema.yaml carries no version history, so
// Rotation.Known must be false for every key Load returns -- never
// fabricated to fill the ceiling levels 3-4 describe.
func TestLoadNeverPopulatesRotation(t *testing.T) {
	inv, err := Load(writeSchema(t, fixtureSchema))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	for _, p := range inv.Paths {
		for _, k := range p.Keys {
			if k.Rotation.Known {
				t.Errorf("%s/%s: Rotation.Known = true, want false (no source for this yet)", p.VaultPath, k.Name)
			}
		}
	}
}

// TestKeyHasNoValueField is a compile-time guard, not a runtime assertion:
// if this package ever grows a field capable of holding a secret's actual
// value, this test file needs editing to reference it, which is the point
// -- level 5 exposure (agent-tui#101) must never be an accidental,
// unreviewed addition.
func TestKeyHasNoValueField(t *testing.T) {
	k := Key{Name: "X", Consumers: nil, Rotation: Rotation{}}
	_ = k // Key{Name, Consumers, Rotation} is its whole field set.
}

func TestLoadMissingFileIsAnError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("Load() error = nil, want an error for a missing file")
	}
}

func TestLoadEmptyPathIsAnError(t *testing.T) {
	_, err := Load("")
	if err == nil {
		t.Fatal("Load(\"\") error = nil, want an error")
	}
}

func TestLoadUnparseableFileIsAnError(t *testing.T) {
	_, err := Load(writeSchema(t, "not: [valid yaml"))
	if err == nil {
		t.Fatal("Load() error = nil, want an error for unparseable YAML")
	}
}

func TestLoadApproleServices(t *testing.T) {
	inv, err := Load(writeSchema(t, fixtureSchema))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(inv.ApproleServices) != 2 || inv.ApproleServices[0] != "api" || inv.ApproleServices[1] != "ai" {
		t.Errorf("ApproleServices = %v, want [api ai]", inv.ApproleServices)
	}
}
