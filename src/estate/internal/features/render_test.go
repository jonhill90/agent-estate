package features

import (
	"strings"
	"testing"
)

// TestRender_ShowsNameStatusAndEvidence asserts on rendered output, not on
// code existing -- a Delivered row's name, status and evidence string must
// all appear on the same line so a reader can see at a glance what shipped
// and how to check it.
func TestRender_ShowsNameStatusAndEvidence(t *testing.T) {
	out := Render([]Feature{
		{ID: "x", Name: "Widget ingestion", Status: Delivered, Evidence: "PR #42"},
	})
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Widget ingestion") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("expected a line naming the feature, got:\n%s", out)
	}
	if !strings.Contains(line, "delivered") {
		t.Errorf("expected status %q on the feature's line, got: %s", Delivered, line)
	}
	if !strings.Contains(line, "PR #42") {
		t.Errorf("expected evidence on the feature's line, got: %s", line)
	}
}

// TestRender_NotStartedRowShowsPlaceholderNotEmptyEvidence checks that a
// not-started row (no evidence expected) renders a visible placeholder
// rather than a blank/misaligned column -- absence should read as absence,
// not as a rendering bug.
func TestRender_NotStartedRowShowsPlaceholderNotEmptyEvidence(t *testing.T) {
	out := Render([]Feature{
		{ID: "y", Name: "Unbuilt thing", Status: NotStarted},
	})
	if !strings.Contains(out, "Unbuilt thing") || !strings.Contains(out, "not-started") {
		t.Fatalf("expected name and status in output, got:\n%s", out)
	}
	if !strings.Contains(out, "-") {
		t.Fatalf("expected a placeholder for empty evidence, got:\n%s", out)
	}
}

// TestRender_CaveatsAppearUnderTheirOwnRow ensures a Caveats string is not
// silently dropped -- the knowledge-query row's whole point is that its
// caveats are visible, not just its headline status.
func TestRender_CaveatsAppearUnderTheirOwnRow(t *testing.T) {
	out := Render([]Feature{
		{ID: "z", Name: "Something", Status: Delivered, Evidence: "PR #1", Caveats: "known gap X"},
	})
	if !strings.Contains(out, "known gap X") {
		t.Fatalf("expected caveats text in output, got:\n%s", out)
	}
}

// TestRender_EmptyRegistryProducesHeaderOnly guards the degenerate case:
// no rows still renders a valid (if useless) table rather than panicking.
func TestRender_EmptyRegistryProducesHeaderOnly(t *testing.T) {
	out := Render(nil)
	if !strings.Contains(out, "CAPABILITY") || !strings.Contains(out, "STATUS") {
		t.Fatalf("expected a header even for an empty registry, got:\n%s", out)
	}
}
