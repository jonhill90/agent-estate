package stub

import (
	"strings"
	"testing"

	"github.com/jonhill90/keelson/internal/theme"
)

// wantDestinations is S1's full tree (SPEC-shell.md) minus S4's three wired
// screens (Tasks, Usage, Lanes) -- the same exclusion Descriptions'own doc
// comment states. A destination missing here means S1's nav can route
// somewhere this package cannot render, which is exactly the "hidden
// screen" failure S5 exists to close.
var wantDestinations = []string{
	"Home", "Dashboard", "Agents", "Chat", "Knowledge", "Library",
	"Skills", "Workflows", "MCP Servers",
	"Connections", "Models", "Storage", "Discord", "Secrets",
	"Monitoring",
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
	for _, wired := range []string{"Tasks", "Usage", "Lanes"} {
		if _, ok := Descriptions[wired]; ok {
			t.Errorf("Descriptions contains %q, which S4 wires to a real screen -- a stub here would hide it", wired)
		}
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
