package stub

import (
	"strings"
	"testing"

	"github.com/jonhill90/agent-tui/internal/theme"
)

// wantDestinations is S1's full tree (SPEC-shell.md) minus S4's three wired
// screens (Tasks, Usage, Lanes) and w5f.md's two (Workflows, Monitoring) --
// the same exclusion Descriptions' own doc comment states. A destination
// missing here means S1's nav can route somewhere this package cannot
// render, which is exactly the "hidden screen" failure S5 exists to close.
// "Models" is absent from this list entirely -- w5f.md removed it from
// internal/nav's own tree, so it is no longer a destination at all
// (nav.Build's own doc comment on why). The Docs group's two ("api-docs",
// "platform-docs") are absent for the newer reason the wired list below
// names: both now have real destinations of their own.
//
// Keyed by internal/nav.Item.ID, not Label -- Descriptions' own doc
// comment records why this file used to get this wrong (Label-keyed
// entries that shell.stubView, keying by id, could never find).
var wantDestinations = []string{
	"home", "dashboard", "agents", "chat", "knowledge", "library",
	"skills", "mcp-servers",
	"connections", "storage", "discord",
	"admin-services", "admin-profiles", "admin-users", "dependencies", "settings",
}

func TestDescriptions_CoversEveryUnwiredDestination(t *testing.T) {
	for _, id := range wantDestinations {
		desc, ok := Descriptions[id]
		if !ok {
			t.Errorf("Descriptions missing entry for %q", id)
			continue
		}
		if strings.TrimSpace(desc) == "" {
			t.Errorf("Descriptions[%q] is empty", id)
		}
	}
}

func TestDescriptions_ExcludesWiredScreens(t *testing.T) {
	// api-docs and platform-docs join this list with internal/apidocs and
	// internal/external: the first is a real pane over the estate's own
	// OpenAPI document, the second is an external destination that opens
	// in a browser. A stub for either would now hide something real, and
	// for Platform Docs it would also claim a pane is coming that never
	// is. secrets joins the same list for the same reason -- agent-tui#101's
	// decision, internal/secrets.Model over hill90-app's own
	// secrets-schema.yaml.
	for _, wired := range []string{"tasks", "usage", "lanes", "workflows", "monitoring", "api-docs", "platform-docs", "secrets"} {
		if _, ok := Descriptions[wired]; ok {
			t.Errorf("Descriptions contains %q, which is wired to a real screen -- a stub here would hide it", wired)
		}
	}
}

// TestDescriptions_ExcludesRemovedModelsRoute guards against "models"
// reappearing here without also reappearing in internal/nav's own tree --
// w5f.md removed both together (nav.Build's own doc comment on why); a
// description with no route to key it is dead weight, not a safety net.
func TestDescriptions_ExcludesRemovedModelsRoute(t *testing.T) {
	if _, ok := Descriptions["models"]; ok {
		t.Error(`Descriptions contains "models", which w5f.md removed from internal/nav's tree entirely -- there is no destination left for it to describe`)
	}
}

// TestDescriptions_KeyedByRouteIDNotLabel is agent-b3.md's own regression
// guard: this map used to be keyed by Label ("Discord"), but
// shell.stubView looks it up with `m.nav.Active()`, a route id
// ("discord") -- the lookup always missed, and EVERY stub silently fell
// back to the generic "not built yet -- no description recorded for this
// route", regardless of the specific text sitting in this map. A key that
// contains an uppercase letter or a space is the Label shape, not the id
// shape (internal/nav's own ids are lowercase, dash-separated -- see
// "mcp-servers", "admin-services") and would reintroduce exactly that
// silent miss.
func TestDescriptions_KeyedByRouteIDNotLabel(t *testing.T) {
	for key := range Descriptions {
		if key != strings.ToLower(key) {
			t.Errorf("Descriptions key %q is not lowercase -- looks like a Label, not a route id; shell.stubView's lookup (m.nav.Active()) will never find it", key)
		}
		if strings.Contains(key, " ") {
			t.Errorf("Descriptions key %q contains a space -- looks like a Label, not a route id", key)
		}
	}
}

func TestView_RendersTitleDescriptionAndNotBuiltYet(t *testing.T) {
	out := View(theme.Default, "skills", Descriptions["skills"])

	for _, want := range []string{"skills", Descriptions["skills"], "not built yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("View output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestView_NonEmptyForEveryDestination(t *testing.T) {
	for name, desc := range Descriptions {
		out := View(theme.Default, name, desc)
		if strings.TrimSpace(out) == "" {
			t.Errorf("View(%q, ...) produced empty output", name)
		}
	}
}
