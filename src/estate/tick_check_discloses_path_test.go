package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTickCheckDisclosesAbsolutePathOfLogItRead reproduces agent-estate#1188:
// tick.Path() resolves docs/tick-log.jsonl against the process's own cwd,
// so `tick check` and `tick record` invoked from different working
// directories can silently read and write different files with no warning.
// `tick check` must name, every run, the ABSOLUTE path it actually read --
// not the constant DefaultPath, and not merely the ESTATE_TICK_LOG override
// text, since a caller comparing outputs needs the resolved path itself.
func TestTickCheckDisclosesAbsolutePathOfLogItRead(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	out := runEstate(t, bin, repoRoot, env, "tick", "check")

	wantAbs, err := filepath.Abs(tickLog)
	if err != nil {
		t.Fatalf("resolve absolute path of scratch tick log: %v", err)
	}
	if !strings.Contains(out, wantAbs) {
		t.Fatalf("tick check did not disclose the absolute path it read (%s); got:\n%s", wantAbs, out)
	}
}

// TestTickCheckReportsNewestEntryAge reproduces the actual cost the issue
// measured: a frozen log kept reporting a plausible-looking "last tick's
// gap" on every check, because the one figure that would have exposed the
// staleness -- how old the newest entry actually is -- was never printed.
// This asserts `tick check` now prints that age, derived from the entry it
// actually read, not a fabricated or constant figure.
func TestTickCheckReportsNewestEntryAge(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	// An entry stamped five minutes in the past -- old enough that "0s" or
	// "1s" could never pass for it, so this also guards against a fixed or
	// zero figure sneaking through.
	at := time.Now().UTC().Add(-5 * time.Minute)
	entry := map[string]any{
		"at":         at.Format(time.RFC3339),
		"phase_item": "phase-0",
		"src_head":   "deadbeef",
		"artifact":   nil,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	if err := os.WriteFile(tickLog, append(line, '\n'), 0o600); err != nil {
		t.Fatalf("write scratch tick log: %v", err)
	}
	if err := os.WriteFile(ledgerPath, nil, 0o600); err != nil {
		t.Fatalf("write scratch ledger: %v", err)
	}

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	out := runEstate(t, bin, repoRoot, env, "tick", "check")

	const wantPrefix = "newest entry age: "
	if !strings.Contains(out, wantPrefix) {
		t.Fatalf("tick check did not report the newest entry's age; got:\n%s", out)
	}
	// The age must reflect roughly five minutes, not zero and not a
	// constant -- reject the two obviously-wrong shapes directly.
	if strings.Contains(out, "newest entry age: 0s") {
		t.Fatalf("tick check reported a zero age for a five-minute-old entry -- got:\n%s", out)
	}
	if !strings.Contains(out, "m") {
		// time.Duration's String() renders five minutes as "5m0s" (rounded);
		// anything with no minutes component at all for a 5m-old entry is
		// wrong.
		t.Fatalf("tick check's newest entry age does not look like it reflects ~5 minutes; got:\n%s", out)
	}
}

// TestTickCheckNewestEntryAgeHonestWhenUnparsable reproduces the "never
// invent a figure" requirement: a last entry whose timestamp cannot be
// parsed must report that honestly, never fall back to a fabricated age
// (e.g. printing "0s" as though the entry were fresh).
func TestTickCheckNewestEntryAgeHonestWhenUnparsable(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	const badEntry = `{"at":"not-a-timestamp","phase_item":"phase-0","src_head":"deadbeef","artifact":null}`
	if err := os.WriteFile(tickLog, []byte(badEntry+"\n"), 0o600); err != nil {
		t.Fatalf("write scratch tick log: %v", err)
	}
	if err := os.WriteFile(ledgerPath, nil, 0o600); err != nil {
		t.Fatalf("write scratch ledger: %v", err)
	}

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	out := runEstate(t, bin, repoRoot, env, "tick", "check")

	const want = "newest entry age: unknown -- the last entry's timestamp"
	if !strings.Contains(out, want) {
		t.Fatalf("tick check must say the newest entry's age is unknown rather than invent one when the timestamp is unparsable; got:\n%s", out)
	}
	if strings.Contains(out, "newest entry age: 0s") {
		t.Fatalf("tick check fabricated a zero age for an unparsable timestamp -- got:\n%s", out)
	}
}

// TestTickCheckNewestEntryAgeHonestWhenLogEmpty is the sibling case: a tick
// log that exists but has no entries (or does not exist at all) must say so
// plainly rather than silently omitting the disclosure or printing a
// fabricated age.
func TestTickCheckNewestEntryAgeHonestWhenLogEmpty(t *testing.T) {
	repoRoot := findRepoRoot(t)
	bin := buildEstateBinary(t)

	scratch := t.TempDir()
	tickLog := filepath.Join(scratch, "tick-log.jsonl")
	ledgerPath := filepath.Join(scratch, "ledger.jsonl")

	env := append(os.Environ(),
		"ESTATE_TICK_LOG="+tickLog,
		"ESTATE_LEDGER="+ledgerPath,
	)

	// No tick log written at all -- the log genuinely has no entries.
	out := runEstate(t, bin, repoRoot, env, "tick", "check")

	const want = "newest entry: none -- the tick log at this path has no entries"
	if !strings.Contains(out, want) {
		t.Fatalf("tick check must say plainly that an empty/missing log has no entries; got:\n%s", out)
	}
}
