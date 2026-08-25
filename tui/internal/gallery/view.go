package gallery

import "fmt"

// Render lays out rows[offset:] as plain text, one block per state, each
// listing every Variant's glyph followed by any Candidates for that state
// -- until either rows runs out or maxLines is reached (the caller clips
// to what the terminal can actually show; Render itself never assumes a
// screen height). Plain text and no lipgloss here, same split
// internal/cost/view.go documents for its own Render functions: testable
// without a styled terminal, with colour applied by the caller (Model, in
// this package's case) over the literal text this returns.
//
// The renderability flag (Cell.Flag) is always printed as a suffix on its
// own glyph's line, in plain ASCII ("[NF]", "[emoji]") -- issue agent-tui#11's own
// acceptance item 3, "unrenderable glyphs are flagged... demonstrably":
// the flag must never be silent, and it must never itself be a glyph that
// could go unrendered.
func Render(rows []Row, offset, maxLines, width int) []string {
	if width <= 0 {
		width = 60
	}
	var out []string
	lines := 0
	for i := offset; i < len(rows) && lines < maxLines; i++ {
		row := rows[i]
		out = append(out, fmt.Sprintf("state: %s", row.State))
		lines++
		for _, c := range row.VariantsBy {
			if lines >= maxLines {
				return out
			}
			out = append(out, renderCell(c, "  "))
			lines++
		}
		for _, c := range row.Candidates {
			if lines >= maxLines {
				return out
			}
			out = append(out, renderCell(c, "  + "))
			lines++
		}
		if lines >= maxLines {
			return out
		}
		out = append(out, "")
		lines++
	}
	return out
}

func renderCell(c Cell, prefix string) string {
	label := c.Source
	if c.Source == "candidate" {
		label = "candidate"
	}
	line := fmt.Sprintf("%s%-10s %s", prefix, label, c.Glyph)
	if c.Flag != "" {
		line += " " + c.Flag
	}
	if c.Source == "candidate" && c.Name != "" {
		line += "  -- " + c.Name
	}
	return line
}

// Legend is the gallery's own explanation of its flags, printed once (not
// per row) -- the same "never make a reader decode a symbol alone"
// discipline internal/rail.Model's legend line already follows for state
// labels.
func Legend() []string {
	return []string{
		"[NF]     Private Use Area glyph -- needs a Nerd Font installed; renders as a fallback box (tofu) without one",
		"[emoji]  relies on the terminal's own colour/emoji font fallback, not this program's font",
		"(no tag) plain ASCII or common Unicode already shipped in a non-Nerd-Font set",
	}
}
