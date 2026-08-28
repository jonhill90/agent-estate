package cost

import (
	"strings"
	"testing"
)

// unknownHarness is what a real harness looks like when ccusage ran fine
// but simply has no local quota source for it (codex, pi) -- Limit.Known
// stays false. knownHarness is what Claude looks like with a real
// -claude-block-limit configured.
var (
	knownHarness = Harness{
		Name:      "claude",
		Cost:      KnownFigure(412.98),
		Tokens:    KnownFigure(798318305),
		CacheRead: KnownFigure(787642179),
		Limit:     Limit{Known: true, Percent: 97.68, Label: "active 5h block", Warn: true},
	}
	unknownHarness = Harness{
		Name:      "codex",
		Cost:      KnownFigure(8.49),
		Tokens:    KnownFigure(11484689),
		CacheRead: KnownFigure(11087104),
		Limit:     Limit{}, // ccusage has no codex quota source
	}
)

// TestEveryViewIsHonestAboutUnknownFetch is the panel-level equivalent of
// agent-tui#4's blindness test: every View, given Snapshot{Known: false}
// (what a failed ccusage call produces), must say "unknown" and must never
// print a bare "0" that could read as "nothing spent".
func TestEveryViewIsHonestAboutUnknownFetch(t *testing.T) {
	for _, v := range Views {
		out := v.Render(Unknown(), 80)
		if !strings.Contains(out, "unknown") {
			t.Errorf("view %q: Unknown() snapshot did not render \"unknown\":\n%s", v.ID, out)
		}
		if strings.Contains(out, "$0") || strings.Contains(out, " 0 ") {
			t.Errorf("view %q: Unknown() snapshot rendered something that looks like a zero:\n%s", v.ID, out)
		}
	}
}

// TestEveryViewShowsUnsetLimitAsNotSetNotZeroPercent covers the other half
// of the same rule applied to Limit specifically: a harness ccusage has
// real cost/token figures for, but no local block-limit source (codex, pi,
// or Claude with no -claude-block-limit set), must never render as "0%
// used" -- that reads as "nothing to worry about", the opposite of the
// truth ("we don't know").
//
// agent-tui#56: this used to assert the word "unknown" here, the same word
// renderQuota uses for a genuinely missing quota.sh -- two different fields
// sharing one word made the panel look like it contradicted itself when a
// harness had a real quota line right below an "unknown" limit line. Limit
// now reads "not set" (a configuration state -- see renderLimitBar's own
// doc comment), which this test asserts instead; the never-a-fabricated-
// percentage guarantee below is unchanged.
func TestEveryViewShowsUnsetLimitAsNotSetNotZeroPercent(t *testing.T) {
	snap := Compose([]Harness{unknownHarness}, Limit{})
	for _, v := range Views {
		out := v.Render(snap, 80)
		if !strings.Contains(out, "not set") {
			t.Errorf("view %q: harness with no block-limit source did not render \"not set\":\n%s", v.ID, out)
		}
		if strings.Contains(out, "0.0%") || strings.Contains(out, "0%") {
			t.Errorf("view %q: harness with no block-limit source rendered a 0%% limit:\n%s", v.ID, out)
		}
	}
}

// TestRenderLimitBarNeverReadsAsMissingQuotaSource is agent-tui#56's own
// reproduction, at the function renderQuota's own text is drawn from: an
// unset Limit and a genuinely unavailable Quota used to render the exact
// same "unknown (no quota source)" words, which is what made
//
//	limit:  unknown (no quota source)
//	quota:  session 22% used, weekly 58% used -- 16% in reserve
//
// read as the panel contradicting itself -- only the quota line was ever
// actually answering a "do we have a quota source" question. Checked at the
// renderLimitBar/renderQuota level, not by scanning a composite rendered
// line, because RenderNumeric's single-line-per-harness layout legitimately
// prints both fields' text adjacent to each other and a substring check
// over that combined line cannot tell which field "no quota source" belongs
// to.
func TestRenderLimitBarNeverReadsAsMissingQuotaSource(t *testing.T) {
	got := renderLimitBar(Limit{})
	if strings.Contains(got, "no quota source") {
		t.Errorf("renderLimitBar(unset) = %q, must not borrow renderQuota's \"no quota source\" wording", got)
	}
	if !strings.Contains(got, "not set") {
		t.Errorf("renderLimitBar(unset) = %q, want it to read as a configuration state (\"not set\")", got)
	}
	// renderQuota keeps the honest refusal this fix must not touch: a
	// genuinely missing quota.sh still says exactly this.
	if q := renderQuota(Quota{}); q != "unknown (no quota source)" {
		t.Errorf("renderQuota(unset) = %q, want the unchanged \"no quota source\" honest refusal", q)
	}
}

// TestEveryViewMarksAWarningState is agent-tui#4 acceptance item 3: a
// near-limit harness must be visibly distinguishable (the literal "WARN"
// marker Model.colorizeWarn highlights) in every layout, not just one.
func TestEveryViewMarksAWarningState(t *testing.T) {
	snap := Compose([]Harness{knownHarness}, knownHarness.Limit)
	for _, v := range Views {
		out := v.Render(snap, 80)
		if !strings.Contains(out, "WARN") {
			t.Errorf("view %q: a Limit with Warn=true did not render a WARN marker:\n%s", v.ID, out)
		}
	}
}

// TestEveryViewBreaksOutCacheRead is agent-tui#4's agent-tui#2: cache-read must
// appear as its own labeled figure, distinct from the token total, in
// every layout -- not folded silently into "tokens".
func TestEveryViewBreaksOutCacheRead(t *testing.T) {
	snap := Compose([]Harness{knownHarness}, knownHarness.Limit)
	for _, v := range Views {
		out := v.Render(snap, 80)
		if !strings.Contains(out, "cache-read") {
			t.Errorf("view %q: no \"cache-read\" label in output:\n%s", v.ID, out)
		}
		if !strings.Contains(out, "787,642,179") {
			t.Errorf("view %q: cache-read value not rendered:\n%s", v.ID, out)
		}
	}
}

func TestRenderEmptyHarnessesIsNotBlank(t *testing.T) {
	snap := Compose(nil, Limit{})
	for _, v := range Views {
		out := v.Render(snap, 80)
		if strings.TrimSpace(out) == "" {
			t.Errorf("view %q: rendered blank for a real, empty (no usage today) snapshot", v.ID)
		}
		if !strings.Contains(out, "no usage") {
			t.Errorf("view %q: empty snapshot did not say why it's empty:\n%s", v.ID, out)
		}
	}
}

// TestFormatFigureRendersUnknown exercises formatFigure's Known guard
// directly. Neither RenderBars nor RenderNumeric can reach this branch from
// real ccusage output (ParseDaily's parse is all-or-nothing -- see
// ccusage.go), so this is the fixture the guard needs to be reachable at
// all rather than dead code no test ever touches.
func TestFormatFigureRendersUnknown(t *testing.T) {
	if got := formatFigure(Figure{}, "%.2f"); got != "unknown" {
		t.Errorf("formatFigure(unknown Figure) = %q, want %q", got, "unknown")
	}
	if got := formatFigure(KnownFigure(1.5), "%.2f"); got != "1.50" {
		t.Errorf("formatFigure(known Figure) = %q, want %q", got, "1.50")
	}
}

// TestRenderCompactIsHonestAboutUnknownFetch mirrors
// TestEveryViewIsHonestAboutUnknownFetch for internal/rail's compact line:
// a failed fetch must say "unknown", never a bare 0.
func TestRenderCompactIsHonestAboutUnknownFetch(t *testing.T) {
	lines := RenderCompact(Unknown(), 24)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "unknown") {
		t.Errorf("RenderCompact(Unknown()) = %q, want it to contain \"unknown\"", joined)
	}
}

// TestRenderCompactPrefersLimitOverCost checks RenderCompact leads with
// quota pressure when known (agent-tui#4: "percentage of budget beats
// dollars, because the failure is quota exhaustion, not a bill") and falls
// back to cost only when there is no quota source.
func TestRenderCompactPrefersLimitOverCost(t *testing.T) {
	snap := Compose([]Harness{knownHarness, unknownHarness}, knownHarness.Limit)
	lines := RenderCompact(snap, 24)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "98%") && !strings.Contains(joined, "97%") {
		t.Errorf("RenderCompact did not lead claude's line with its limit percentage:\n%s", joined)
	}
	if !strings.Contains(joined, "$8.49") {
		t.Errorf("RenderCompact did not fall back to cost for codex (no quota source):\n%s", joined)
	}
}

func TestCommaIntFormatsLargeCounts(t *testing.T) {
	cases := map[int64]string{
		0:         "0",
		798318305: "798,318,305",
		-1234:     "-1,234",
		999:       "999",
	}
	for in, want := range cases {
		if got := commaInt(in); got != want {
			t.Errorf("commaInt(%d) = %q, want %q", in, got, want)
		}
	}
}
