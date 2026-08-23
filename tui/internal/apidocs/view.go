package apidocs

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

// unconfiguredNotice is the honest statement this pane shows instead of a
// generic placeholder when no spec is resolvable: it names the document
// that WOULD back this route and how to point at it, because "not built
// yet" tells the next person nothing and this does.
const unconfiguredNotice = "no -openapi (or $HILL90_APP_REPO) configured -- point it at hill90-app's " +
	"services/api/src/openapi/openapi.yaml, the same document the web app's /docs/api page renders"

func selectedStyle(th theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Background(th.Color(theme.RoleSelectedBG))
}

// View renders the operation table: one line per operation, method, path,
// auth and the document's own summary. Every figure on screen is read from
// the document -- there is no sample endpoint anywhere in this package.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("api docs") + "\n")

	if m.unconfigured {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(legendStyle.Render("the estate's own OpenAPI document, as an operation table") + "\n\n")
		b.WriteString(errStyle.Render("! no spec configured") + "\n")
		b.WriteString(legendStyle.Render(unconfiguredNotice) + "\n")
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
	}

	if m.ref.Title != "" {
		head := m.ref.Title
		if m.ref.Version != "" {
			head += " v" + m.ref.Version
		}
		if m.ref.OpenAPI != "" {
			head += "  (openapi " + m.ref.OpenAPI + ")"
		}
		b.WriteString(legendStyle.Render(head) + "\n")
	}
	if m.ref.SourcePath != "" {
		b.WriteString(legendStyle.Render("source: "+m.ref.SourcePath) + "\n")
	}
	b.WriteString("\n")

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! could not read the spec: "+m.fetchErr.Error()) + "\n")
	}

	visible := m.Visible()
	switch {
	case !m.fetchedOnce && m.fetchErr == nil:
		b.WriteString(legendStyle.Render("not fetched yet") + "\n")
	case len(m.ref.Endpoints) == 0 && m.fetchErr == nil:
		b.WriteString(legendStyle.Render("(the document declares no operations)") + "\n")
	case len(visible) == 0:
		b.WriteString(legendStyle.Render(fmt.Sprintf("(no operation matches %q)", m.filter)) + "\n")
	default:
		b.WriteString(legendStyle.Render(fmt.Sprintf("%-7s %-46s %-7s %s", "METHOD", "PATH", "AUTH", "SUMMARY")) + "\n")
		rows := m.listRows()
		end := m.offset + rows
		if end > len(visible) {
			end = len(visible)
		}
		for i := m.offset; i < end; i++ {
			e := visible[i]
			line := fmt.Sprintf("%-7s %-46s %-7s %s",
				e.Method, truncate(e.Path, 46), authLabel(e.Auth), truncate(summaryOf(e), 60))
			if i == m.selected {
				line = selectedStyle(m.theme).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	if m.fetchedOnce {
		b.WriteString(legendStyle.Render(fmt.Sprintf("%d operations across %d paths%s -- read %s ago",
			len(m.ref.Endpoints), m.ref.PathCount, filterSuffix(m.filter, len(visible)),
			time.Since(m.lastFetched).Round(time.Second))) + "\n")
	}
	if m.themeNotice != "" {
		b.WriteString(legendStyle.Render(m.themeNotice) + "\n")
	}
	if m.filtering {
		b.WriteString(legendStyle.Render("filter: "+m.filter+"_") + "  " + legendStyle.Render("[enter] apply  [esc] clear"))
	} else {
		b.WriteString(legendStyle.Render("[j/k] move  [/] filter  [esc] clear  [r] reload  [t] theme  [q] quit"))
	}

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
}

func filterSuffix(filter string, shown int) string {
	if filter == "" {
		return ""
	}
	return fmt.Sprintf(", %d shown for %q", shown, filter)
}

// authLabel renders Endpoint.Auth's three states without collapsing them:
// a required scheme, an explicitly public operation, and one that says
// nothing and inherits the document's own default.
func authLabel(auth *bool) string {
	switch {
	case auth == nil:
		return "default"
	case *auth:
		return "yes"
	default:
		return "public"
	}
}

func summaryOf(e Endpoint) string {
	if e.Summary != "" {
		return e.Summary
	}
	return "(no summary in the document)"
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
