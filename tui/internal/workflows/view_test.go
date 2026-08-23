package workflows

import (
	"strings"
	"testing"

	"github.com/jonhill90/agent-tui/internal/board"
)

func TestView_UnconfiguredRendersVisibleError(t *testing.T) {
	m := New(nil)
	out := m.View()
	if !strings.Contains(out, "! unavailable") {
		t.Errorf("View missing visible unavailable error, got:\n%s", out)
	}
	if !strings.Contains(out, "-ledger") {
		t.Errorf("View missing -ledger guidance, got:\n%s", out)
	}
}

func TestView_NotFetchedYetIsDistinctFromZeroRows(t *testing.T) {
	m := New(func() ([]board.TaskRow, error) { return nil, nil })
	out := m.View()
	if !strings.Contains(out, "not fetched yet") {
		t.Errorf("View missing 'not fetched yet' before any fetch, got:\n%s", out)
	}
}

func TestView_RendersRowFields(t *testing.T) {
	m := New(func() ([]board.TaskRow, error) { return nil, nil })
	delivered := int64(1700000100)
	next, _ := m.Update(fetchResultMsg{rows: []board.TaskRow{
		{
			TaskID: "t-abc123", SourceKind: "pull", SourceRef: "94",
			SourceStatus: "resolved", TaskStatus: "complete", Lane: "estate:build-5",
			Repo: board.Repo{Owner: "jonhill90", Name: "agent-tui"}, Number: "94",
			CreatedAt: 1700000000, DeliveredAt: &delivered,
		},
	}})
	m = next.(Model)
	out := m.View()

	for _, want := range []string{"agent-tui PR#94", "estate:build-5", "complete"} {
		if !strings.Contains(out, want) {
			t.Errorf("View missing %q, got:\n%s", want, out)
		}
	}
	// AcceptedAt/CompletedAt are nil -- must render as "--", not "unknown"
	// (formatOptTS's own distinction from formatTS's zero-CreatedAt case).
	if !strings.Contains(out, "--") {
		t.Errorf("View missing '--' for unset timestamps, got:\n%s", out)
	}
}

func TestView_EmptyAfterFetchIsDistinctFromUnconfigured(t *testing.T) {
	m := New(func() ([]board.TaskRow, error) { return nil, nil })
	next, _ := m.Update(fetchResultMsg{rows: nil})
	m = next.(Model)
	out := m.View()
	if !strings.Contains(out, "(no dispatches recorded)") {
		t.Errorf("View missing empty-but-fetched message, got:\n%s", out)
	}
	if strings.Contains(out, "! unavailable") {
		t.Errorf("View should not show the unconfigured error for a real fetch that found zero rows, got:\n%s", out)
	}
}

func TestSourceLabel_UnresolvedURLIsUnknown(t *testing.T) {
	got := sourceLabel(board.TaskRow{})
	if got != unknown {
		t.Errorf("sourceLabel(zero value) = %q, want %q", got, unknown)
	}
}
