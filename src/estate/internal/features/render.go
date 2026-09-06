package features

import (
	"fmt"
	"strings"
)

// Render turns a slice of Feature rows into a plain-text table: one row per
// feature, columns for status and evidence, a caveats line appended under
// any row that carries one. It is a pure function of its input specifically
// so tests can assert on rendered output without needing the real Registry
// or a running command (see render_test.go).
func Render(fs []Feature) string {
	var b strings.Builder

	nameW := len("CAPABILITY")
	statusW := len("STATUS")
	for _, f := range fs {
		if len(f.Name) > nameW {
			nameW = len(f.Name)
		}
		if len(f.Status) > statusW {
			statusW = len(f.Status)
		}
	}

	fmt.Fprintf(&b, "%-*s  %-*s  %s\n", nameW, "CAPABILITY", statusW, "STATUS", "EVIDENCE")
	for _, f := range fs {
		evidence := f.Evidence
		if evidence == "" {
			evidence = "-"
		}
		fmt.Fprintf(&b, "%-*s  %-*s  %s\n", nameW, f.Name, statusW, string(f.Status), evidence)
		if f.Caveats != "" {
			fmt.Fprintf(&b, "%*s  caveats: %s\n", nameW, "", f.Caveats)
		}
	}
	return b.String()
}
