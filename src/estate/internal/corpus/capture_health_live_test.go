package corpus

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestLiveCorpusCaptureIsNotStale is agent-estate#1357's detector, the
// first concrete slice of agent-estate#1139's "per-source capture health"
// ask. WHY THIS EXISTS: prompt_capture_hook.py wrote every live prompt into
// the wrong database for 8 days (2026-09-02 to 2026-09-10) and nobody
// noticed, because both writers -- the buggy hook and the corpus's own
// judging pipeline -- succeeded on every call and neither reported which
// file it had actually written to. A capture pipeline writing to the wrong
// place looks EXACTLY like one that works; recency was the only signal that
// would have told the two apart, and nothing was checking it.
//
// WHERE THIS FIRES, and why that answers the brief's own question ("a check
// living in a PR template nobody reads is the fifth 'guard nothing calls'
// this week"): this is an ordinary Go test in the package every dispatch's
// own grounding already imports, so it runs as part of the SAME
// `go test ./src/estate/...` every task's Verify section already runs
// unconditionally -- not a new cron job, not a new invocation point that
// itself could go uncalled, not a dashboard nobody opens. The very next
// dispatch after a capture regression fails here, by name, instead of
// discovering it by accident days later (the same "genuinely fires" shape
// TestLiveStandingLawMemberHashHasNotDrifted (agent-estate#1286,
// standinglaw_live_test.go) and cmd/goldenquery's live_ratchet_test.go
// (agent-estate#1335) already establish for this repo).
//
// Skips loudly, never silently, when there is no live corpus to read (e.g.
// a CI runner, which never has ~/corpus) -- same shape as
// TestHardCountMatchesLiveCorpusAcrossAllThreeKinds above. A skip here means
// "this run checked nothing", not "the corpus is fine".
func TestLiveCorpusCaptureIsNotStale(t *testing.T) {
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no live corpus at %s -- skipping capture-staleness check: %v", p, err)
	}

	out, err := exec.Command("sqlite3", "file:"+p+"?mode=ro&immutable=1",
		"select max(at) from prompts;").Output()
	if err != nil {
		t.Fatalf("reading live corpus's newest prompt timestamp: %v", err)
	}
	maxAtStr := strings.TrimSpace(string(out))
	if maxAtStr == "" {
		t.Fatal("live corpus's prompts table has no rows at all -- that is a " +
			"different, worse failure than staleness, and this check is not the " +
			"one that should discover an empty table silently")
	}
	maxAt, err := strconv.ParseInt(maxAtStr, 10, 64)
	if err != nil {
		t.Fatalf("live corpus's max(at) %q did not parse as an integer: %v", maxAtStr, err)
	}

	age := time.Since(time.Unix(maxAt, 0))

	// THRESHOLD, by measurement, not a round number (agent-estate#1357's
	// own brief: "attack your own threshold"). Measured directly against
	// the live corpus (30-day window, post-migration so the 2026-09-02..10
	// incident's own gap is already closed and cannot inflate this number):
	// the largest gap between two consecutive real prompts in ordinary
	// operation was 72.4 hours (one weekend-shaped quiet stretch); the next
	// largest was 46.0 hours. 6,436 prompts were captured in that same
	// 30-day window.
	//
	// captureStaleThreshold is deliberately set to 48 hours -- BELOW the
	// largest observed legitimate quiet gap (72.4h), not above it. That is
	// a conscious choice, not an oversight: this issue's own brief names
	// 2026-09-04 (48 hours after the 2026-09-02 regression) as the date a
	// working check should have caught this by, and a threshold generous
	// enough to never false-positive against a 72-hour quiet weekend would
	// not have caught the real incident until day 3, missing that target.
	// The tradeoff is stated plainly, not hidden: this check WILL
	// occasionally fail on a genuinely quiet 2-3 day stretch with no real
	// prompts submitted anywhere. That is accepted -- a stale corpus that
	// silently loses a week of captures (this issue's actual cost) is worse
	// than an occasional false alarm a human re-runs the suite to clear.
	const captureStaleThresholdHours = 48
	threshold := captureStaleThresholdHours * time.Hour

	if age > threshold {
		t.Fatalf(
			"live corpus's newest prompt is %s old (threshold %dh) -- the corpus "+
				"has not received a capture in over %d hours. This is exactly the "+
				"agent-estate#1357 failure shape: a capture writer can succeed on "+
				"every call while writing to the wrong place, and recency is the "+
				"only signal that tells the two apart. Check: is "+
				"prompt_capture_hook.py actually registered in .claude/settings.json "+
				"for this session? Does its Ledger root resolve to %s (echo "+
				"$AGENT_CORPUS_DIR, or its default ~/corpus)? Has AGENT_SUPERVISOR_STATE_DIR "+
				"been set somewhere that would override a script back onto the dead "+
				"~/.local/state/agent-dotfiles-supervisor path? Or is this a genuine, "+
				"legitimate quiet stretch longer than %dh -- in which case this is an "+
				"accepted false positive, re-run once real activity resumes.",
			age.Round(time.Second), captureStaleThresholdHours, int(age.Hours()), p,
			captureStaleThresholdHours,
		)
	}
	t.Logf("live corpus's newest prompt is %s old (threshold %s) -- capture is current",
		age.Round(time.Second), threshold)
}

// TestLiveCorpusHasNoUnexplainedGapWiderThanThreshold is a second angle on
// the same signal: not just "is the NEWEST prompt fresh" (which a single
// prompt submitted seconds before this test runs would satisfy even if the
// pipeline had been broken for days beforehand and something else happened
// to write one row), but "was there ever a gap of this size ANYWHERE in the
// recent window" -- catching a pipeline that broke, then got fixed, then
// broke again, or one where a single manual write masks an otherwise-dead
// stretch. Same threshold, same live-or-skip precondition, same reasoning.
func TestLiveCorpusHasNoUnexplainedGapWiderThanThreshold(t *testing.T) {
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no live corpus at %s -- skipping gap check: %v", p, err)
	}

	const windowDays = 7
	windowStart := time.Now().Add(-windowDays * 24 * time.Hour).Unix()

	out, err := exec.Command("sqlite3", "file:"+p+"?mode=ro&immutable=1",
		fmt.Sprintf("select at from prompts where at > %d order by at;", windowStart)).Output()
	if err != nil {
		t.Fatalf("reading live corpus's recent prompt timestamps: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 || lines[0] == "" {
		t.Skipf("fewer than 2 prompts in the last %d days -- not enough data to measure a gap "+
			"(this is itself worth a human's attention if unexpected, but this test only checks "+
			"the space BETWEEN captured prompts, not their count)", windowDays)
	}

	const captureStaleThresholdHours = 48
	threshold := captureStaleThresholdHours * time.Hour

	var prev int64 = -1
	var maxGap time.Duration
	var maxGapAt time.Time
	for _, line := range lines {
		at, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			t.Fatalf("unparseable timestamp %q in live corpus prompts.at: %v", line, err)
		}
		if prev >= 0 {
			gap := time.Duration(at-prev) * time.Second
			if gap > maxGap {
				maxGap = gap
				maxGapAt = time.Unix(prev, 0)
			}
		}
		prev = at
	}

	if maxGap > threshold {
		t.Fatalf(
			"live corpus has a %s gap with no captured prompt, starting %s, inside the last "+
				"%d-day window (threshold %s) -- a capture pipeline that stops and later resumes "+
				"looks exactly like one that never broke, unless something checks for the gap "+
				"itself, not only current recency (agent-estate#1357)",
			maxGap.Round(time.Second), maxGapAt.Format(time.RFC3339), windowDays, threshold,
		)
	}
	t.Logf("largest gap between consecutive prompts in the last %d days: %s (threshold %s)",
		windowDays, maxGap.Round(time.Second), threshold)
}
