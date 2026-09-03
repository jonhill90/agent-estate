package finder

import "testing"

func items() []Item {
	return []Item{
		{Kind: "route", ID: "knowledge", Label: "Knowledge"},
		{Kind: "route", ID: "chat", Label: "Chat"},
		{Kind: "route", ID: "tasks", Label: "Tasks"},
		{Kind: "route", ID: "mcp-servers", Label: "MCP Servers"},
		{Kind: "route", ID: "admin-services", Label: "Services", Detail: "Admin"},
		{Kind: "task", ID: "916", Label: "#916", Detail: "the merge gate can tell a reviewer from an author"},
	}
}

// Subsequence matching is what makes it feel fuzzy: the letters must appear
// in order, but not adjacently.
func TestSubsequenceMatching(t *testing.T) {
	if _, _, ok := Score("knw", "Knowledge"); !ok {
		t.Error(`"knw" must match "Knowledge"`)
	}
	if _, _, ok := Score("mcps", "MCP Servers"); !ok {
		t.Error(`"mcps" must match "MCP Servers"`)
	}
	if _, _, ok := Score("wnk", "Knowledge"); ok {
		t.Error(`"wnk" is out of order and must NOT match "Knowledge"`)
	}
	if _, _, ok := Score("", "anything"); !ok {
		t.Error("an empty query matches everything")
	}
}

// The obvious answer must rank first, or the finder is a lottery.
func TestTheObviousMatchRanksFirst(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{"chat", "Chat"},
		{"know", "Knowledge"},
		{"task", "Tasks"},
		{"mcp", "MCP Servers"},
	} {
		got := Filter(items(), tc.query)
		if len(got) == 0 {
			t.Fatalf("%q matched nothing", tc.query)
		}
		if got[0].Item.Label != tc.want {
			t.Errorf("%q ranked %q first, want %q", tc.query, got[0].Item.Label, tc.want)
		}
	}
}

// Detail is searchable: a task is found by its words, not only its number.
func TestDetailIsSearchable(t *testing.T) {
	got := Filter(items(), "reviewer")
	if len(got) == 0 || got[0].Item.ID != "916" {
		t.Fatalf(`"reviewer" should find the task by its detail text; got %+v`, got)
	}
}

// A stable query must produce a stable order. A list that reshuffles under
// the same keystrokes is unusable however good the ranking.
func TestOrderIsStableForEqualScores(t *testing.T) {
	a := Filter(items(), "s")
	for i := 0; i < 20; i++ {
		b := Filter(items(), "s")
		for j := range a {
			if a[j].Item.ID != b[j].Item.ID {
				t.Fatalf("order changed between identical queries at %d: %s vs %s",
					j, a[j].Item.ID, b[j].Item.ID)
			}
		}
	}
}

func TestTypingNarrowsAndBackspaceWidens(t *testing.T) {
	m := New(items())
	all := len(m.Matches)
	if all != len(items()) {
		t.Fatalf("an empty query shows everything: got %d of %d", all, len(items()))
	}
	m = m.Type('c').Type('h')
	if len(m.Matches) == 0 || len(m.Matches) >= all {
		t.Fatalf(`typing "ch" must narrow: %d matches out of %d`, len(m.Matches), all)
	}
	m = m.Backspace().Backspace()
	if len(m.Matches) != all {
		t.Errorf("backspacing to empty must restore everything: got %d want %d", len(m.Matches), all)
	}
	// Backspacing past the start is something everyone does.
	if got := m.Backspace(); got.Query != "" {
		t.Errorf("backspace on an empty query must be a no-op, got %q", got.Query)
	}
}

// Selection must clamp, never wrap: wrapping moves you somewhere you were not
// looking.
func TestSelectionClampsAndDoesNotWrap(t *testing.T) {
	m := New(items())
	m = m.Move(-5)
	if m.Selected != 0 {
		t.Errorf("moving up past the top clamps to 0, got %d", m.Selected)
	}
	m = m.Move(100)
	if m.Selected != len(m.Matches)-1 {
		t.Errorf("moving down past the end clamps to the last, got %d", m.Selected)
	}
}

// Pressing enter on an empty list must do nothing rather than jump somewhere
// arbitrary.
func TestNoMatchesMeansNoChoice(t *testing.T) {
	m := New(items())
	for _, r := range "zzzzzz" {
		m = m.Type(r)
	}
	if len(m.Matches) != 0 {
		t.Fatalf("expected no matches, got %d", len(m.Matches))
	}
	if _, ok := m.Choice(); ok {
		t.Error("an empty match list must offer no choice")
	}
}

// Narrowing must reset the selection, or enter jumps to whatever happened to
// be selected before the query changed.
func TestNarrowingResetsSelection(t *testing.T) {
	m := New(items()).Move(3)
	if m.Selected == 0 {
		t.Fatal("precondition: selection moved")
	}
	m = m.Type('c')
	if m.Selected != 0 {
		t.Errorf("typing must reset the selection to the best match, got %d", m.Selected)
	}
}

// The positions returned must actually index the matched characters, since a
// renderer highlights by them.
func TestPositionsPointAtTheMatchedCharacters(t *testing.T) {
	sc, pos, ok := Score("knw", "Knowledge")
	if !ok || sc <= 0 {
		t.Fatalf("expected a scored match, got %d %v", sc, ok)
	}
	want := "knw"
	got := ""
	for _, p := range pos {
		got += string([]rune("knowledge")[p])
	}
	if got != want {
		t.Errorf("positions spell %q, want %q", got, want)
	}
}
