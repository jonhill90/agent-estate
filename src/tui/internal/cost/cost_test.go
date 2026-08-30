package cost

import "testing"

func TestComposeOrdersClaudeCodexPiFirst(t *testing.T) {
	snap := Compose([]Harness{
		{Name: "pi", Cost: KnownFigure(0.1)},
		{Name: "amp", Cost: KnownFigure(1)},
		{Name: "codex", Cost: KnownFigure(2)},
		{Name: "claude", Cost: KnownFigure(3)},
	}, Limit{})

	want := []string{"claude", "codex", "pi", "amp"}
	if len(snap.Harnesses) != len(want) {
		t.Fatalf("got %d harnesses, want %d", len(snap.Harnesses), len(want))
	}
	for i, name := range want {
		if snap.Harnesses[i].Name != name {
			t.Errorf("harness[%d] = %q, want %q", i, snap.Harnesses[i].Name, name)
		}
	}
}

func TestComposeAttachesClaudeLimitOnlyToClaude(t *testing.T) {
	limit := Limit{Known: true, Percent: 42, Label: "test"}
	snap := Compose([]Harness{{Name: "claude"}, {Name: "codex"}}, limit)

	for _, h := range snap.Harnesses {
		switch h.Name {
		case "claude":
			if !h.Limit.Known || h.Limit.Percent != 42 {
				t.Errorf("claude limit = %+v, want %+v", h.Limit, limit)
			}
		case "codex":
			if h.Limit.Known {
				t.Errorf("codex limit = %+v, want Known=false -- ccusage has no codex quota source", h.Limit)
			}
		}
	}
}

func TestComposeSetsKnownTrue(t *testing.T) {
	snap := Compose(nil, Limit{})
	if !snap.Known {
		t.Error("Compose's Snapshot.Known = false, want true -- Compose is only called after a successful fetch")
	}
}

func TestUnknownSnapshotIsNeverKnown(t *testing.T) {
	snap := Unknown()
	if snap.Known {
		t.Error("Unknown().Known = true, want false")
	}
	if len(snap.Harnesses) != 0 {
		t.Errorf("Unknown().Harnesses = %+v, want empty", snap.Harnesses)
	}
}
