package nav

import "testing"

// TestBuildMatchesSpec is SPEC-shell.md S1's own acceptance line: "Table
// test asserting the tree matches this list exactly." Every ID/label pair
// below is transcribed from the spec's own bullet list, independently of
// tree.go's literal values, so a typo or a dropped/renamed item in Build
// fails this test rather than only "looking right" on screen.
//
// Connect's children below have three entries, not S1's original four:
// "Models" was removed by w5f.md (tree.go's own doc comment on that
// Group's Children for why) -- this is a deliberate, later departure from
// S1's own bullet list, not a transcription error, so this comment
// documents it rather than leaving a silent mismatch with the spec text.
func TestBuildMatchesSpec(t *testing.T) {
	tr := Build()

	wantItems := []string{
		"Home", "Dashboard", "Agents", "Chat", "Tasks", "Knowledge", "Library", "Lanes",
	}
	if len(tr.Items) != len(wantItems) {
		t.Fatalf("top-level items = %d, want %d (%v)", len(tr.Items), len(wantItems), tr.Items)
	}
	for i, want := range wantItems {
		if got := tr.Items[i].Label; got != want {
			t.Errorf("Items[%d].Label = %q, want %q", i, got, want)
		}
	}
	if tr.Items[len(tr.Items)-1].Kind != KindNative {
		t.Errorf("Lanes item Kind = %v, want KindNative (SPEC-shell.md: %q)",
			tr.Items[len(tr.Items)-1].Kind, "the one item with no web equivalent")
	}

	wantGroups := []struct {
		label    string
		children []string
	}{
		{"Build", []string{"Skills", "Workflows", "MCP Servers"}},
		{"Connect", []string{"Connections", "Storage", "Discord", "Secrets"}},
		{"Observe", []string{"Usage", "Monitoring"}},
		{"Docs", []string{"API Docs", "Platform Docs"}},
		{"Admin", []string{"Services", "Profiles", "Users", "Dependencies", "Settings"}},
	}
	if len(tr.Groups) != len(wantGroups) {
		t.Fatalf("groups = %d, want %d (%v)", len(tr.Groups), len(wantGroups), tr.Groups)
	}
	for gi, wg := range wantGroups {
		g := tr.Groups[gi]
		if g.Label != wg.label {
			t.Errorf("Groups[%d].Label = %q, want %q", gi, g.Label, wg.label)
		}
		if len(g.Children) != len(wg.children) {
			t.Fatalf("Groups[%d] (%s) children = %d, want %d (%v)", gi, g.Label, len(g.Children), len(wg.children), g.Children)
		}
		for ci, wc := range wg.children {
			if got := g.Children[ci].Label; got != wc {
				t.Errorf("Groups[%d].Children[%d].Label = %q, want %q", gi, ci, got, wc)
			}
		}
	}
}

// TestBuildIDsUnique guards Flatten/GroupContaining's own lookups: a
// duplicate ID would let one node silently shadow another's active/expand
// state.
func TestBuildIDsUnique(t *testing.T) {
	seen := map[string]bool{}
	tr := Build()
	for _, it := range tr.Items {
		if seen[it.ID] {
			t.Fatalf("duplicate top-level ID %q", it.ID)
		}
		seen[it.ID] = true
	}
	for _, g := range tr.Groups {
		if seen[g.ID] {
			t.Fatalf("duplicate group ID %q", g.ID)
		}
		seen[g.ID] = true
		for _, child := range g.Children {
			if seen[child.ID] {
				t.Fatalf("duplicate child ID %q", child.ID)
			}
			seen[child.ID] = true
		}
	}
}

// TestFlattenOrderAndCounts asserts Flatten's contract: every top-level
// Item first (in order, GroupID == ""), then each Group as a header node
// immediately followed by its own Children (GroupID == the owning group).
func TestFlattenOrderAndCounts(t *testing.T) {
	tr := Build()
	nodes := tr.Flatten()

	wantLen := len(tr.Items)
	for _, g := range tr.Groups {
		wantLen += 1 + len(g.Children)
	}
	if len(nodes) != wantLen {
		t.Fatalf("Flatten() len = %d, want %d", len(nodes), wantLen)
	}

	i := 0
	for _, it := range tr.Items {
		n := nodes[i]
		if n.IsGroupHeader() || n.Item.ID != it.ID || n.GroupID != "" {
			t.Fatalf("nodes[%d] = %+v, want top-level item %q", i, n, it.ID)
		}
		i++
	}
	for _, g := range tr.Groups {
		n := nodes[i]
		if !n.IsGroupHeader() || n.Group.ID != g.ID {
			t.Fatalf("nodes[%d] = %+v, want group header %q", i, n, g.ID)
		}
		i++
		for _, child := range g.Children {
			n := nodes[i]
			if n.IsGroupHeader() || n.Item.ID != child.ID || n.GroupID != g.ID {
				t.Fatalf("nodes[%d] = %+v, want child %q of group %q", i, n, child.ID, g.ID)
			}
			i++
		}
	}
}

// TestGroupContaining covers a top-level item (""), a child of the first
// group, and a child of the last group -- the two ends and the general
// case of the lookup model.go's auto-expand depends on.
func TestGroupContaining(t *testing.T) {
	tr := Build()
	cases := []struct {
		id   string
		want string
	}{
		{"home", ""},
		{"lanes", ""},
		{"skills", "build"},
		{"settings", "admin"},
		{"does-not-exist", ""},
	}
	for _, c := range cases {
		if got := tr.GroupContaining(c.id); got != c.want {
			t.Errorf("GroupContaining(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}
