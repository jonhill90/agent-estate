package lane

import "testing"

func TestDecode(t *testing.T) {
	text := `{"lanes":[{"window":3,"window_id":"@7","name":"sk107-rail","command":"claude","state":"busy","idle_seconds":4}],"count":1}`
	lanes, err := Decode(text)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(lanes) != 1 {
		t.Fatalf("expected 1 lane, got %d", len(lanes))
	}
	l := lanes[0]
	if l.Window != 3 || l.WindowID != "@7" || l.Name != "sk107-rail" || l.Command != "claude" || l.State != "busy" || l.IdleSeconds != 4 {
		t.Fatalf("unexpected decode: %+v", l)
	}
}

func TestDecodeInvalidJSON(t *testing.T) {
	if _, err := Decode("not json"); err == nil {
		t.Fatal("expected an error decoding non-JSON, got nil")
	}
}

// TestEveryVariantNamesEveryState is the addendum's rule 2 applied to every
// entry in Variants, not just the default: "stale, menu-blocked and unsent
// are the forgotten ones -- a variant that cannot show them is not a
// candidate." This duplicates variants.go's init() panic as a normal test
// failure (readable in `go test` output, not a crash trace) and is the
// thing that goes red the moment someone adds a fifth variant without
// filling in all twelve states.
func TestEveryVariantNamesEveryState(t *testing.T) {
	for _, set := range Variants {
		if missing := MissingStates(set); len(missing) > 0 {
			t.Errorf("variant %q is missing states %v", set.ID, missing)
		}
	}
}

func TestUnmappedStateStillNamesItself(t *testing.T) {
	for _, set := range Variants {
		style := StyleFor(set, "some-future-state-nobody-mapped-yet")
		if style.Label != "some-future-state-nobody-mapped-yet" {
			t.Errorf("variant %q: an unmapped state must still print its own name, got label %q", set.ID, style.Label)
		}
		if len(style.Frames) == 0 {
			t.Errorf("variant %q: Unmapped must still have frames to draw, not a blank glyph", set.ID)
		}
	}
}

func TestFrameNeverPanicsAcrossManyTicks(t *testing.T) {
	states := append(append([]string{}, AllStates...), "totally-unknown")
	for _, set := range Variants {
		for _, state := range states {
			for tick := 0; tick < 50; tick++ {
				if g := Frame(set, state, tick); g == "" {
					t.Fatalf("variant %q: Frame(%q, %d) returned an empty glyph", set.ID, state, tick)
				}
			}
		}
	}
}

func TestVariantsHaveUniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, set := range Variants {
		if seen[set.ID] {
			t.Errorf("duplicate variant ID %q", set.ID)
		}
		seen[set.ID] = true
	}
}

func TestDefaultIsFirstVariant(t *testing.T) {
	if len(Variants) == 0 {
		t.Fatal("Variants must not be empty -- there must always be a working default")
	}
	if Default.ID != Variants[0].ID {
		t.Fatalf("Default must be Variants[0], got %q vs %q", Default.ID, Variants[0].ID)
	}
}
