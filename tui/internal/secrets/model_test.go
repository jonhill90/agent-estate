package secrets

import (
	"errors"
	"strings"
	"testing"
)

func fakeInventory() Inventory {
	return Inventory{
		SourcePath:      "/fake/secrets-schema.yaml",
		TotalKeys:       2,
		ApproleServices: []string{"api", "ai"},
		Paths: []Path{
			{
				VaultPath: "secret/shared/database",
				Keys: []Key{
					{Name: "DB_USER", Consumers: []string{"db", "api"}},
					{Name: "DB_PASSWORD", Consumers: []string{"db", "api"}},
				},
			},
		},
	}
}

func fetched(m Model, inv Inventory, err error) Model {
	next, _ := m.Update(fetchResultMsg{inv: inv, err: err})
	return next.(Model)
}

func TestViewRendersEveryKeyFromTheInventory(t *testing.T) {
	m := fetched(New(func() (Inventory, error) { return fakeInventory(), nil }), fakeInventory(), nil)
	out := m.View()
	for _, want := range []string{
		"/fake/secrets-schema.yaml", "approle services: api, ai",
		"secret/shared/database", "DB_USER", "DB_PASSWORD", "db,api",
		"2 keys across 1 paths",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q:\n%s", want, out)
		}
	}
}

// TestViewNeverRendersAValue is agent-tui#101's level-5 rule, checked at
// the render boundary in addition to secrets_test.go's type-level guard:
// nothing resembling a secret value may ever reach the screen. "unknown"
// covers the age/rotation columns for a fixture with no Rotation set, so
// this pins the ABSENCE of a value column, not merely that this one
// fixture's fake values are missing.
func TestViewNeverRendersAValue(t *testing.T) {
	out := fetched(New(func() (Inventory, error) { return fakeInventory(), nil }), fakeInventory(), nil).View()
	if !strings.Contains(out, "unknown") {
		t.Errorf("View() does not show the age/rotation columns as unknown:\n%s", out)
	}
}

// TestUnconfiguredNamesTheRealSource: "not built yet" tells the next
// person nothing; this pane must name the file and flag that would back
// it instead, the same rule internal/apidocs' own unconfigured state
// follows.
func TestUnconfiguredNamesTheRealSource(t *testing.T) {
	out := New(nil).View()
	for _, want := range []string{"no schema configured", "-secrets-schema", "HILL90_APP_REPO", "platform/vault/secrets-schema.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("unconfigured View() missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not built yet") {
		t.Errorf("unconfigured View() reads as an unbuilt stub:\n%s", out)
	}
}

func TestUnconfiguredModelDoesNotFetch(t *testing.T) {
	if cmd := New(nil).Init(); cmd != nil {
		t.Error("Init() on an unconfigured model returned a command")
	}
}

// TestFetchErrorIsVisible: a schema that could not be read must say so,
// not render an empty table that reads as "this estate has no secrets".
func TestFetchErrorIsVisible(t *testing.T) {
	m := fetched(New(func() (Inventory, error) { return Inventory{}, errors.New("read /nope: no such file") }),
		Inventory{}, errors.New("read /nope: no such file"))
	out := m.View()
	if !strings.Contains(out, "could not read the schema") || !strings.Contains(out, "/nope") {
		t.Errorf("View() hides the read failure:\n%s", out)
	}
}
