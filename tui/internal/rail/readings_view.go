package rail

import (
	"fmt"
	"time"

	"github.com/jonhill90/keelson/internal/board"
	"github.com/jonhill90/keelson/internal/lane"
)

// renderReadingDetail is the selected-lane detail block -- what used to be
// the flat "state: / idle:" pair in renderFlatBody/renderSessionsBody,
// pulled out so both share one implementation and so m.reading actually
// changes what is drawn, not just how. Called with sel already resolved to
// one lane.Lane and its lane.Style (StyleFor's result), the same way both
// callers already had both values on hand.
//
// ledgerLane is the caller's own "<session>:<window-index>" task-join key
// (each caller builds it differently -- renderFlatBody has only m.sessionName
// and must go through ledgerLaneKey; renderSessionsBody already has the
// session name AND the window index on hand from its own sessionRow, so it
// builds the key directly) -- this function never re-derives it from sel,
// which is exactly the agent-tui#86-shaped bug (joining by sel.Name, the
// pane's descriptive display name, against a ledger keyed by numeric window
// index) this signature exists to make impossible to reintroduce here.
func (m Model) renderReadingDetail(sel lane.Lane, ledgerLane string, style lane.Style, st railStyles, innerWidth int) []string {
	label := style.Label
	if label == "" {
		label = sel.State // Unmapped: still print the raw word, never blank
	}

	t, haveTask := m.tasksByLane()[ledgerLane]
	needsHuman, reason := taskNeedsHuman(t, haveTask, sel.State)
	age, haveAge := taskOpenAge(t, haveTask, time.Now())

	id := workReading.ID
	if m.reading >= 0 && m.reading < len(readings) {
		id = readings[m.reading].ID
	}

	if id == statusReading.ID {
		return m.renderStatusDetail(label, needsHuman, reason, age, haveAge, sel, st, innerWidth)
	}
	return m.renderWorkDetail(label, t, haveTask, age, haveAge, sel, st, innerWidth)
}

// renderWorkDetail: what is in flight, and how long has it been open.
func (m Model) renderWorkDetail(label string, t board.TaskRow, haveTask bool, age time.Duration, haveAge bool, sel lane.Lane, st railStyles, innerWidth int) []string {
	var b []string
	summary := taskSummary(t, haveTask)
	b = append(b, st.legend.Width(innerWidth).Render(truncate(fmt.Sprintf("on:    %s", summary), innerWidth)))
	if haveAge {
		b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("open:  %s", age.Round(time.Second))))
	}
	b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("state: %s", label)))
	b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("idle:  %ds", sel.IdleSeconds)))
	return b
}

// renderStatusDetail: what is healthy, what needs attention, and why.
func (m Model) renderStatusDetail(label string, needsHuman bool, reason string, age time.Duration, haveAge bool, sel lane.Lane, st railStyles, innerWidth int) []string {
	var b []string
	b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("state:  %s", label)))
	if needsHuman {
		b = append(b, st.warn.Width(innerWidth).Render("health: needs human"))
		b = append(b, st.dim.Width(innerWidth).Render(truncate("  "+reason, innerWidth)))
	} else {
		b = append(b, st.legend.Width(innerWidth).Render("health: ok"))
	}
	if haveAge {
		b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("since:  %s", age.Round(time.Second))))
	}
	b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("idle:   %ds", sel.IdleSeconds)))
	return b
}
