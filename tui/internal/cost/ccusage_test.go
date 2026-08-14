package cost

import "testing"

// realDailySample is `ccusage daily --json --by-agent --since 20260814
// --until 20260814` output, captured live against ccusage 20.0.19 on
// 2026-08-14 -- real numbers, not hand-authored, per agent-tui#4's "read
// ccusage, do not reimplement it; check its --json output before
// assuming a shape" constraint.
const realDailySample = `{
  "daily": [
    {
      "agent": "all",
      "agents": [
        {
          "agent": "claude",
          "cacheCreationTokens": 8679841,
          "cacheReadTokens": 787642179,
          "inputTokens": 7427,
          "outputTokens": 1988858,
          "totalCost": 412.9770475000005,
          "totalTokens": 798318305
        },
        {
          "agent": "codex",
          "cacheCreationTokens": 0,
          "cacheReadTokens": 11087104,
          "inputTokens": 359141,
          "outputTokens": 38444,
          "totalCost": 8.492577,
          "totalTokens": 11484689
        }
      ],
      "cacheCreationTokens": 8679841,
      "cacheReadTokens": 798729283,
      "inputTokens": 366568,
      "outputTokens": 2027302,
      "period": "2026-08-14",
      "totalCost": 421.46962450000046,
      "totalTokens": 809802994
    }
  ],
  "totals": {
    "totalCost": 421.46962450000046,
    "totalTokens": 809802994
  }
}`

// realActiveBlockWarningSample is `ccusage blocks --active --json
// --token-limit 300000000` captured live the same day. This particular
// limit (300,000,000) was chosen by trying several round numbers against
// the real active block's own token count until ccusage's own status
// field came back "warning" rather than "ok" or "exceeds" -- the limit is
// picked, the percentage and status are ccusage's real computation, not
// asserted or synthesized here. See the PR description for how this
// demonstrates the panel's near-limit/warning acceptance case.
const realActiveBlockWarningSample = `{
  "blocks": [
    {
      "isActive": true,
      "tokenLimitStatus": {
        "limit": 300000000,
        "percentUsed": 97.676912,
        "projectedUsage": 293030736,
        "status": "warning"
      }
    }
  ]
}`

func TestParseDailyExtractsPerHarnessFigures(t *testing.T) {
	harnesses, err := ParseDaily([]byte(realDailySample), "2026-08-14")
	if err != nil {
		t.Fatal(err)
	}
	if len(harnesses) != 2 {
		t.Fatalf("got %d harnesses, want 2: %+v", len(harnesses), harnesses)
	}

	byName := map[string]Harness{}
	for _, h := range harnesses {
		byName[h.Name] = h
	}

	claude, ok := byName["claude"]
	if !ok {
		t.Fatal("no claude harness parsed")
	}
	if !claude.Cost.Known || claude.Cost.Value != 412.9770475000005 {
		t.Errorf("claude cost = %+v", claude.Cost)
	}
	if !claude.Tokens.Known || claude.Tokens.Value != 798318305 {
		t.Errorf("claude tokens = %+v", claude.Tokens)
	}
	// The load-bearing field: cache-read must come through as its own
	// figure, distinct from the token total it is folded INTO by ccusage
	// but must never be folded into by this panel (issue #4's #2).
	if !claude.CacheRead.Known || claude.CacheRead.Value != 787642179 {
		t.Errorf("claude cache-read = %+v", claude.CacheRead)
	}

	codex, ok := byName["codex"]
	if !ok {
		t.Fatal("no codex harness parsed")
	}
	if !codex.Cost.Known || codex.Cost.Value != 8.492577 {
		t.Errorf("codex cost = %+v", codex.Cost)
	}
}

func TestParseDailyNoMatchingRowIsEmptyNotError(t *testing.T) {
	// ccusage ran fine and simply has no row for a day nothing happened --
	// that must decode as an empty, non-error result (genuinely zero usage
	// that day), never confused with the fetch itself failing.
	harnesses, err := ParseDaily([]byte(realDailySample), "2099-01-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if harnesses != nil {
		t.Errorf("harnesses = %+v, want nil for a day with no ccusage row", harnesses)
	}
}

func TestParseDailyRejectsGarbage(t *testing.T) {
	if _, err := ParseDaily([]byte("not json"), "2026-08-14"); err == nil {
		t.Error("expected a decode error for unparsable ccusage output")
	}
}

func TestParseActiveBlockLimitReadsRealWarningStatus(t *testing.T) {
	limit, err := ParseActiveBlockLimit([]byte(realActiveBlockWarningSample))
	if err != nil {
		t.Fatal(err)
	}
	if !limit.Known {
		t.Fatal("limit.Known = false, want true")
	}
	if limit.Percent != 97.676912 {
		t.Errorf("limit.Percent = %v, want 97.676912", limit.Percent)
	}
	if !limit.Warn {
		t.Error("limit.Warn = false for ccusage status \"warning\", want true")
	}
}

func TestParseActiveBlockLimitNoActiveBlockIsUnknown(t *testing.T) {
	limit, err := ParseActiveBlockLimit([]byte(`{"blocks": [{"isActive": false}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if limit.Known {
		t.Errorf("limit = %+v, want Known=false with no active block", limit)
	}
}

func TestParseActiveBlockLimitMissingStatusIsUnknown(t *testing.T) {
	// No --token-limit was ever passed to ccusage: tokenLimitStatus is
	// simply absent from an active block's own JSON in that case. This must
	// never be read as 0% used.
	limit, err := ParseActiveBlockLimit([]byte(`{"blocks": [{"isActive": true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if limit.Known {
		t.Errorf("limit = %+v, want Known=false with no tokenLimitStatus", limit)
	}
}

// TestExecRunnerSurfacesAMissingBinary is the unit-level half of the
// blindness test (agent-tui#4 acceptance item 2): pointing ExecRunner at a
// binary that cannot exist must return an error, not silently succeed with
// empty output that a caller could mistake for "zero usage".
func TestExecRunnerSurfacesAMissingBinary(t *testing.T) {
	run := ExecRunner("agent-tui-ccusage-binary-does-not-exist-on-this-path")
	_, err := run([]string{"daily", "--json"})
	if err == nil {
		t.Fatal("expected an error for a nonexistent binary, got nil")
	}
}
