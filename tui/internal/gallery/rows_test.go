package gallery

import (
	"strings"
	"testing"

	"github.com/jonhill90/keelson/internal/lane"
)

func TestBuildRowsCoversEveryState(t *testing.T) {
	rows := BuildRows()
	if len(rows) != len(lane.AllStates) {
		t.Fatalf("BuildRows returned %d rows, want %d (one per lane.AllStates entry)", len(rows), len(lane.AllStates))
	}
	for i, state := range lane.AllStates {
		if rows[i].State != state {
			t.Errorf("row %d: state = %q, want %q (BuildRows must follow AllStates order)", i, rows[i].State, state)
		}
	}
}

// TestBuildRowsHasEveryVariantForEveryState is the gallery-level echo of
// lane.TestEveryVariantNamesEveryState: every row must carry exactly one
// Cell per lane.Variants entry, in Variants order, because lane.StyleFor
// never returns "nothing" -- an unmapped state falls back to Unmapped, it
// never produces a missing Cell. If this ever produced fewer cells than
// len(lane.Variants), the gallery itself would be the thing silently
// hiding a variant, not lane's own guard.
func TestBuildRowsHasEveryVariantForEveryState(t *testing.T) {
	rows := BuildRows()
	for _, row := range rows {
		if len(row.VariantsBy) != len(lane.Variants) {
			t.Errorf("state %q: got %d variant cells, want %d", row.State, len(row.VariantsBy), len(lane.Variants))
		}
		for i, cell := range row.VariantsBy {
			if cell.Source != lane.Variants[i].ID {
				t.Errorf("state %q cell %d: Source = %q, want %q", row.State, i, cell.Source, lane.Variants[i].ID)
			}
			if cell.Glyph == "" {
				t.Errorf("state %q variant %q: empty glyph", row.State, cell.Source)
			}
		}
	}
}

func TestBuildRowsNerdCellsAreFlagged(t *testing.T) {
	rows := BuildRows()
	for _, row := range rows {
		for _, cell := range row.VariantsBy {
			if cell.Source != "nerd" {
				continue
			}
			if cell.Flag != "[NF]" {
				t.Errorf("state %q nerd cell: Flag = %q, want \"[NF]\"", row.State, cell.Flag)
			}
		}
	}
}

// TestBuildRowsIncludesCandidates is agent-tui#11's "discovery, not
// confirmation" requirement made concrete: at least one state must show a
// glyph that is in NO shipped Variant.
func TestBuildRowsIncludesCandidates(t *testing.T) {
	rows := BuildRows()
	found := 0
	for _, row := range rows {
		for _, cand := range row.Candidates {
			found++
			for _, v := range row.VariantsBy {
				if v.Glyph == cand.Glyph {
					t.Errorf("state %q: candidate glyph %q duplicates a shipped variant's glyph -- not actually a discovery entry", row.State, cand.Glyph)
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("no state has any candidate glyphs -- the gallery would only show what's already shipped")
	}
}

func TestRenderFlagsUnrenderableGlyphsInOutput(t *testing.T) {
	rows := BuildRows()
	lines := Render(rows, 0, 1000, 80)
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "[NF]") {
		t.Fatal("Render output never mentions [NF] -- a Nerd Font glyph must be visibly flagged, not silently offered")
	}
}

func TestRenderRespectsMaxLines(t *testing.T) {
	rows := BuildRows()
	lines := Render(rows, 0, 5, 80)
	if len(lines) > 5 {
		t.Errorf("Render(..., maxLines=5) returned %d lines, want <= 5", len(lines))
	}
}

func TestRenderOffsetSkipsEarlierStates(t *testing.T) {
	rows := BuildRows()
	lines := Render(rows, 0, 1000, 80)
	full := strings.Join(lines, "\n")
	if !strings.Contains(full, "state: "+rows[0].State) {
		t.Fatalf("Render at offset 0 should include the first state %q", rows[0].State)
	}

	skipped := Render(rows, 1, 1000, 80)
	skippedText := strings.Join(skipped, "\n")
	if strings.Contains(skippedText, "state: "+rows[0].State) {
		t.Errorf("Render at offset 1 still shows state %q, which should have been skipped", rows[0].State)
	}
}
