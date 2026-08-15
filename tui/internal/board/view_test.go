package board

import (
	"strings"
	"testing"
	"time"
)

func TestRenderIncludesWIPOverCapacity(t *testing.T) {
	wip := []WIP{{Session: "agent-tui", InProgress: 3, Capacity: 2, OverCapacity: true}}
	var b strings.Builder
	writeWIP(&b, wip, 80)
	if !strings.Contains(b.String(), "OVER") {
		t.Errorf("expected an OVER marker for over-capacity WIP:\n%s", b.String())
	}
}

func TestCardLineTruncatesToWidth(t *testing.T) {
	c := Card{Repo: testRepo, Number: 1, Title: strings.Repeat("x", 500)}
	line := cardLine(c, 40)
	if len(line) > 40 {
		t.Errorf("line length %d exceeds width 40: %q", len(line), line)
	}
}

func TestCardLineMarksAgedCard(t *testing.T) {
	c := Card{Repo: testRepo, Number: 95, Title: "as95 conflicting", Age: 3 * time.Hour}
	line := cardLine(c, 80)
	if !strings.HasPrefix(line, "!") {
		t.Errorf("expected an aged-card marker at the start of cardLine's output: %q", line)
	}
}

func TestFormatAgeBuckets(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "-"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h"},
		{25 * time.Hour, "1d"},
	}
	for _, c := range cases {
		if got := formatAge(c.d); got != c.want {
			t.Errorf("formatAge(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}
