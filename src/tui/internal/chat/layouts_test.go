package chat

import "testing"

// TestLayoutsAreTwoDistinctRenderers pins layouts.go's own claim: exactly
// two entries, IDs "list" and "grid" -- adding a third near-duplicate
// (the issue's collapsed options [3]/[4]) would be the mistake this
// package's doc comments explain away.
func TestLayoutsAreTwoDistinctRenderers(t *testing.T) {
	if len(Layouts) != 2 {
		t.Fatalf("len(Layouts) = %d, want 2", len(Layouts))
	}
	ids := map[string]bool{}
	for _, l := range Layouts {
		if l.ID == "" {
			t.Error("layoutDef with empty ID")
		}
		if l.Name == "" || l.Description == "" {
			t.Errorf("layout %q missing Name or Description", l.ID)
		}
		ids[l.ID] = true
	}
	if !ids["list"] || !ids["grid"] {
		t.Errorf("Layouts IDs = %v, want {list, grid}", ids)
	}
}

func TestListLayoutIsDefault(t *testing.T) {
	if Layouts[0].ID != listLayout.ID {
		t.Errorf("Layouts[0] = %q, want listLayout (%q) -- the default silence-still-yields-something-sane layout", Layouts[0].ID, listLayout.ID)
	}
}
