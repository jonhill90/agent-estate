package agents

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/src/tui/internal/estatus"
	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

// unknown is what Row.Model/Row.Cost render as -- never "0" or blank, the
// same "unknown, not zero" discipline internal/cost's Figure.Known already
// enforces for the harness-wide totals this package's own Cost field
// cannot attribute per lane (Row's own doc comment).
const unknown = "unknown"

// View renders a flat table -- ID | STATE | MODE | MODEL | TASK | COST --
// one row per Row (Derive's output, via m.Rows()). MODE (SPEC-shell.md
// S12) reads like MODEL and COST now: "unknown" for a nil Row.Mode,
// never a guessed value -- see Row.Mode's and modeFor's own doc comments
// for exactly when that is. A quitting Model renders nothing, the same
// convention internal/cost.Model.View follows.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("agents") + "\n")

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! sessions unavailable: "+m.fetchErr.Error()) + "\n")
		// agent-tui#175: a failed refresh no longer clears m.sessions (see
		// Update's fetchResultMsg case), so rows below may be the last GOOD
		// fetch rather than current data -- say so explicitly rather than
		// let a reader mistake an unlabelled table for a fresh one.
		if !m.lastFetched.IsZero() {
			age := time.Since(m.lastFetched).Round(time.Second)
			b.WriteString(legendStyle.Render(fmt.Sprintf("(showing last good data, age: %s)", age)) + "\n")
		}
	}

	rows := m.Rows()
	if len(rows) == 0 {
		b.WriteString(legendStyle.Render("(no agents)") + "\n")
	} else {
		b.WriteString(legendStyle.Render(fmt.Sprintf("%-28s %-10s %-9s %-10s %-16s %s", "ID", "STATE", "MODE", "MODEL", "TASK", "COST")) + "\n")
		for _, r := range rows {
			mode := unknown
			if r.Mode != nil {
				mode = string(*r.Mode)
			}
			model := unknown
			if r.Model != nil {
				model = *r.Model
			}
			cost := unknown
			if r.Cost != nil {
				cost = *r.Cost
			}
			b.WriteString(fmt.Sprintf("%-28s %-10s %-9s %-10s %-16s %s", truncate(r.ID, 28), truncate(r.State, 10), truncate(mode, 9), truncate(model, 10), truncate(r.Task, 16), cost) + "\n")
		}
	}

	b.WriteString(ledgerSection(m))

	b.WriteString("\n")
	if m.notice != "" {
		b.WriteString(legendStyle.Render(m.notice) + "\n")
	}
	b.WriteString(legendStyle.Render("[n] thread (S7)  [r] refresh  [t] theme  [q] quit"))

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
}

// ledgerSection renders agent-estate#930's own addition: what src/estate's
// Go dispatch ledger says is in flight, ADDITIVE to the table above rather
// than a replacement for it -- see ledger.go's own package doc comment.
// Renders nothing at all when no LedgerFetcher was wired (WithLedger never
// called) or none has answered yet, matching this package's existing
// "notice only appears when there is something to say" convention.
//
// The three ledger.Availability branches below render VISIBLY differently
// (agent-estate#930's own "second-order lesson"): Absent says a dispatch
// has never been recorded (first-run, not a fault), Unreadable warns this
// is not zero, and Present with zero in-flight turns says so as a real
// answer -- never collapsed into one "no agents" line that could mean any
// of the three.
func ledgerSection(m Model) string {
	if m.ledgerFetch == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n" + titleStyle.Render("from the dispatch ledger") + "\n")

	if !m.ledgerFetched {
		b.WriteString(legendStyle.Render("not fetched yet") + "\n")
		return b.String()
	}

	switch m.ledgerStatus.Ledger {
	case estatus.Absent:
		b.WriteString(legendStyle.Render("no dispatch has ever been recorded at "+m.ledgerStatus.LedgerPath) + "\n")
		return b.String()
	case estatus.Unreadable:
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		reason := "no reason recorded"
		if m.ledgerStatus.LedgerErr != nil {
			reason = m.ledgerStatus.LedgerErr.Error()
		}
		b.WriteString(errStyle.Render("! ledger UNREADABLE, this is not zero: "+reason) + "\n")
		return b.String()
	}

	rows := DeriveLedger(m.ledgerStatus)
	if len(rows) == 0 {
		b.WriteString(legendStyle.Render("0 turns in flight") + "\n")
		return b.String()
	}
	b.WriteString(legendStyle.Render(fmt.Sprintf("%-28s %-10s %-9s %-11s %-25s %s", "ID", "ISSUE", "ROLE", "STATE", "STARTED", "PID")) + "\n")
	for _, r := range rows {
		pid := unknown
		if r.PID != nil {
			pid = fmt.Sprintf("%d", *r.PID)
		}
		b.WriteString(fmt.Sprintf("%-28s %-10s %-9s %-11s %-25s %s", truncate(r.ID, 28), truncate(r.Issue, 10), truncate(r.Role, 9), truncate(r.State, 11), r.Started, pid) + "\n")
	}
	return b.String()
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
