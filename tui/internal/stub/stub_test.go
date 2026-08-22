package stub

import (
	"strings"
	"testing"

	"github.com/jonhill90/keelson/internal/theme"
)

// wantDestinations is S1's full tree (SPEC-shell.md) minus S4's three wired
// screens (Tasks, Usage, Lanes) and w5f.md's two (Workflows, Monitoring) --
// the same exclusion Descriptions' own doc comment states. A destination
// missing here means S1's nav can route somewhere this package cannot
// render, which is exactly the "hidden screen" failure S5 exists to close.
// "Models" is absent from this list entirely -- w5f.md removed it from
// internal/nav's own tree, so it is no longer a destination at all
// (nav.Build's own doc comment on why).
var wantDestinations = []string{
	"Home", "Dashboard", "Agents", "Chat", "Knowledge", "Library",
	"Skills", "MCP Servers",
	"Connections", "Storage", "Discord", "Secrets",
	"API Docs", "Platform Docs",
	"Services", "Profiles", "Users", "Dependencies", "Settings",
}

func TestDescriptions_CoversEveryUnwiredDestination(t *testing.T) {
	for _, name := range wantDestinations {
		desc, ok := Descriptions[name]
		if !ok {
			t.Errorf("Descriptions missing entry for %q", name)
			continue
		}
		if strings.TrimSpace(desc) == "" {
			t.Errorf("Descriptions[%q] is empty", name)
		}
	}
}

func TestDescriptions_ExcludesWiredScreens(t *testing.T) {
	for _, wired := range []string{"Tasks", "Usage", "Lanes", "Workflows", "Monitoring"} {
		if _, ok := Descriptions[wired]; ok {
			t.Errorf("Descriptions contains %q, which is wired to a real screen -- a stub here would hide it", wired)
		}
	}
}

// TestDescriptions_ExcludesRemovedModelsRoute guards against "Models"
// reappearing here without also reappearing in internal/nav's own tree --
// w5f.md removed both together (nav.Build's own doc comment on why); a
// description with no route to key it is dead weight, not a safety net.
func TestDescriptions_ExcludesRemovedModelsRoute(t *testing.T) {
	if _, ok := Descriptions["Models"]; ok {
		t.Error(`Descriptions contains "Models", which w5f.md removed from internal/nav's tree entirely -- there is no destination left for it to describe`)
	}
}

func TestView_RendersTitleDescriptionAndNotBuiltYet(t *testing.T) {
	out := View(theme.Default, "Skills", Descriptions["Skills"])

	for _, want := range []string{"Skills", Descriptions["Skills"], "not built yet"} {
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
