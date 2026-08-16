package cost

import (
	"errors"
	"strings"
	"testing"
)

// TestParseQuotaSummary_ParsesEveryLine reproduces quota.sh summary's real
// output shape (its own Python f-string, quota.go's doc comment) with two
// providers, one of which has an unresolved usedPercent ("-", codexbar's
// own default when a window can't be read) -- session and weekly must be
// independently Known, not both dropped because one of them is unknown.
func TestParseQuotaSummary_ParsesEveryLine(t *testing.T) {
	out := "  claude   session=42% used  weekly=10% used  resets in 3h\n" +
		"  codex    session=-% used  weekly=5% used  \n"

	got := ParseQuotaSummary(out)

	claude, ok := got["claude"]
	if !ok || !claude.Known {
		t.Fatalf("claude: got %+v, want Known", claude)
	}
	if !claude.SessionPercent.Known || claude.SessionPercent.Value != 42 {
		t.Errorf("claude session: got %+v, want Known 42", claude.SessionPercent)
	}
	if !claude.WeeklyPercent.Known || claude.WeeklyPercent.Value != 10 {
		t.Errorf("claude weekly: got %+v, want Known 10", claude.WeeklyPercent)
	}
	if claude.Note != "resets in 3h" {
		t.Errorf("claude note: got %q, want %q", claude.Note, "resets in 3h")
	}

	codex, ok := got["codex"]
	if !ok || !codex.Known {
		t.Fatalf("codex: got %+v, want Known (line still parses even with a \"-\" field)", codex)
	}
	if codex.SessionPercent.Known {
		t.Errorf("codex session: got %+v, want NOT Known (source was \"-\")", codex.SessionPercent)
	}
	if !codex.WeeklyPercent.Known || codex.WeeklyPercent.Value != 5 {
		t.Errorf("codex weekly: got %+v, want Known 5", codex.WeeklyPercent)
	}
}

func TestParseQuotaSummary_NoMatchingLinesIsEmptyNotNil(t *testing.T) {
	got := ParseQuotaSummary("quota: unavailable\n")
	if len(got) != 0 {
		t.Errorf("got %+v, want no entries", got)
	}
}

// TestFetchQuotaSummary_ExecFailureIsUnknown covers "quota.sh may be
// untracked on this machine" (as#227, the brief's own citation): a runner
// that cannot even exec the binary must not crash keelson, and must not be
// mistaken for a harness with legitimately zero quota pressure.
func TestFetchQuotaSummary_ExecFailureIsUnknown(t *testing.T) {
	run := func(args []string) ([]byte, int, error) {
		return nil, -1, errors.New("exec: \"quota.sh\": file does not exist")
	}
	got := FetchQuotaSummary(run)
	if got != nil {
		t.Errorf("got %+v, want nil map on exec failure", got)
	}
}

// TestFetchQuotaSummary_UnrecognisedExitCodeIsUnknown pins the brief's own
// rule verbatim: "anything unrecognised, including 127, is UNKNOWN" --
// applied here even though 127 is not one of quota.sh's own documented
// `check` codes (0/1/2/3), because this package holds ALL non-zero exits
// from `summary` to the same strict standard.
func TestFetchQuotaSummary_UnrecognisedExitCodeIsUnknown(t *testing.T) {
	for _, code := range []int{1, 2, 3, 127} {
		run := func(args []string) ([]byte, int, error) {
			return []byte("  claude   session=99% used  weekly=99% used  \n"), code, nil
		}
		got := FetchQuotaSummary(run)
		if got != nil {
			t.Errorf("code %d: got %+v, want nil map (never a guessed percentage)", code, got)
		}
	}
}

func TestFetchQuotaSummary_CleanExitParses(t *testing.T) {
	run := func(args []string) ([]byte, int, error) {
		return []byte("  claude   session=42% used  weekly=10% used  resets in 3h\n"), 0, nil
	}
	got := FetchQuotaSummary(run)
	if !got["claude"].Known {
		t.Fatalf("got %+v, want claude Known on clean exit", got)
	}
}

func TestRenderQuota_UnknownNeverGuesses(t *testing.T) {
	got := renderQuota(Quota{})
	if got != "unknown (no quota source)" {
		t.Errorf("got %q, want the unknown sentinel", got)
	}
}

func TestRenderQuota_KnownRendersBothPercentages(t *testing.T) {
	q := Quota{Known: true, SessionPercent: KnownFigure(42), WeeklyPercent: KnownFigure(10), Note: "resets in 3h"}
	got := renderQuota(q)
	want := "session 42% used, weekly 10% used -- resets in 3h"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestUnknownWithQuota_RendersQuotaEvenWhenCcusageUnknown reproduces the
// reopened half of agent-tui#49 item 3: PR #50's review found a real
// machine with a working quota.sh but no working ccusage, and the cost
// panel showed "cost: unknown (ccusage unreadable)" with NO quota line at
// all -- the old code returned before quota.sh was ever consulted whenever
// ccusage failed. UnknownWithQuota must carry quota.sh's real data through
// that same failure.
func TestUnknownWithQuota_RendersQuotaEvenWhenCcusageUnknown(t *testing.T) {
	quotas := map[string]Quota{
		"claude": {Known: true, SessionPercent: KnownFigure(18), WeeklyPercent: KnownFigure(9), Note: "resets in 2h"},
	}
	snap := UnknownWithQuota(quotas)
	if snap.Known {
		t.Fatal("UnknownWithQuota(...).Known = true, want false -- ccusage itself is still unknown")
	}
	for _, v := range Views {
		out := v.Render(snap, 80)
		if !strings.Contains(out, "unknown") {
			t.Errorf("view %q: did not render \"unknown\" for the ccusage-unreadable cost figures:\n%s", v.ID, out)
		}
		if !strings.Contains(out, "18%") || !strings.Contains(out, "9%") {
			t.Errorf("view %q: did not render the real quota percentages even though ccusage was unreadable:\n%s", v.ID, out)
		}
	}
}

// TestUnknownWithQuota_NoQuotaDataRendersExactlyAsUnknown pins that the new
// quota-only render path adds NOTHING when there is genuinely no quota
// data either (quota.sh not configured/unreachable) -- must read identical
// to the pre-#49 "cost: unknown (ccusage unreadable)\n", not a dangling
// empty quota line.
func TestUnknownWithQuota_NoQuotaDataRendersExactlyAsUnknown(t *testing.T) {
	for _, v := range Views {
		got := v.Render(UnknownWithQuota(nil), 80)
		want := v.Render(Unknown(), 80)
		if got != want {
			t.Errorf("view %q: UnknownWithQuota(nil) = %q, want identical to Unknown() = %q", v.ID, got, want)
		}
	}
}
