package cost

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Quota is codexbar's own view of one harness's session/weekly usage
// window, read through agent-supervisor's quota.sh gate (agent-tui#49 item
// 3) -- never codexbar directly; quota.sh's own header names itself "THE
// SEAM: nothing else in this estate may call `codexbar` directly," and this
// package honors that by shelling quota.sh out, not codexbar. Known is
// false whenever quota.sh could not be read at all: missing binary, an
// exec failure, an unrecognised exit code, or no line for this harness in
// its summary. A harness with Known false must render "unknown", never a
// guessed percentage -- the same discipline Limit.Known already enforces
// for ccusage's own block-limit figure (see this file's ParseQuotaSummary
// and cost.go's doc comment on Limit).
type Quota struct {
	Known          bool
	SessionPercent Figure
	WeeklyPercent  Figure
	Note           string // codexbar's own pace/reset summary text, verbatim -- opaque, never reparsed
}

// QuotaRunner executes quota.sh with args and reports its stdout AND exit
// code. quota.sh's own header is explicit that callers "branch on these
// [codes], never on the text," so this seam preserves the code rather than
// collapsing it into a Go error the way LedgerRunner/cost.Runner do -- only
// a genuine exec failure (binary not found, not executable -- the "quota.sh
// may be untracked on this machine" case, as#227) becomes err.
type QuotaRunner func(args []string) (stdout []byte, exitCode int, err error)

// ExecQuotaRunner shells quota.sh out via os/exec -- the real
// implementation. bin is quota.sh's own path (cmd/keelson resolves it
// relative to -supervisor-repo); quota.sh, not this package, is the one
// place `codexbar` may be invoked.
func ExecQuotaRunner(bin string) QuotaRunner {
	return func(args []string) ([]byte, int, error) {
		cmd := exec.Command(bin, args...)
		out, err := cmd.Output()
		if err == nil {
			return out, 0, nil
		}
		if ee, ok := err.(*exec.ExitError); ok {
			// A real run that exited non-zero: quota.sh's own contract (0
			// proceed, 1 wind down, 2 unavailable, 3 missing codexbar)
			// still applies -- ParseQuotaSummary below is the one place
			// that decides which of those (or anything else, including
			// 127 from a shell "command not found") count as usable.
			return out, ee.ExitCode(), nil
		}
		// Not even a process: bin missing, not executable, etc. Distinct
		// from a nonzero exit -- there is no exit code to report, so
		// FetchQuotaSummary must treat this the same as any other
		// unrecognised outcome: unknown, never a guess.
		return nil, -1, err
	}
}

// quotaLineRE mirrors quota.sh's own `summary` Python f-string exactly:
//
//	f'  {provider:8} session={prim.get("usedPercent","-")}% used  weekly={sec.get("usedPercent","-")}% used  {pace.get("summary","")}'
//
// \s+ absorbs the f-string's fixed 8-column padding without this package
// having to reproduce that width itself -- only the field order and
// literal tokens ("session=", "% used", "weekly=") are load-bearing.
var quotaLineRE = regexp.MustCompile(`^\s*(\S+)\s+session=(\S+)%\s+used\s+weekly=(\S+)%\s+used\s+(.*)$`)

// ParseQuotaSummary reads `quota.sh summary`'s stdout and returns one Quota
// per provider line it can parse, keyed by quota.sh's own provider name
// (codexbar's "claude"/"codex"/... -- the same vocabulary cost.Harness.Name
// already uses, so a caller can merge this straight onto Harnesses by
// name). A provider whose usedPercent came back "-" (codexbar's own
// unavailableReason case, or a provider quota.sh's json simply lacks a
// window for) gets Figure{} -- Known false -- for that one number, not a
// dropped provider entirely: session and weekly are independent unknowns,
// same as Harness.Cost/Tokens/CacheRead already are.
func ParseQuotaSummary(output string) map[string]Quota {
	out := map[string]Quota{}
	for _, line := range strings.Split(output, "\n") {
		m := quotaLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out[m[1]] = Quota{
			Known:          true,
			SessionPercent: parsePercentField(m[2]),
			WeeklyPercent:  parsePercentField(m[3]),
			Note:           strings.TrimSpace(m[4]),
		}
	}
	return out
}

func parsePercentField(s string) Figure {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return Figure{}
	}
	return KnownFigure(v)
}

// FetchQuotaSummary runs `quota.sh summary` through run and parses it,
// collapsing every failure mode the brief calls out into one nil map: a
// genuine exec failure (run's err != nil), or any exit code other than 0.
// quota.sh's own contract documents 0/1/2/3 for its `check` subcommand;
// `summary` has no comparable "proceed/wind down" meaning (it is a
// human-readable dump, not a gate), so this package holds it to the
// simpler and stricter rule the brief states explicitly: only a clean exit
// is trusted, "anything unrecognised, including 127, is UNKNOWN."
func FetchQuotaSummary(run QuotaRunner) map[string]Quota {
	out, code, err := run([]string{"summary"})
	if err != nil || code != 0 {
		return nil
	}
	return ParseQuotaSummary(string(out))
}
