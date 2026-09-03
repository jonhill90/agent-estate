// Package reclaim frees a dispatch slot only on a positive observation that
// the process it launched cannot still be running.
//
// WHY. "unknown is not failed" (ledger.State.Terminal) is correct: a turn we
// could not observe may still be running, and treating it as finished is how
// the cap fails open. But that rule has a cost of its own -- a turn whose
// process actually died still holds its slot forever, because nothing ever
// revisits it. internal/isolate's teardown refusal (protecting uncollected
// work, correctly) means "the worktree is gone" can never be that signal: a
// dead turn's worktree survives exactly as long as a live one's, since
// neither is ever cleaned up by anything but a human.
//
// The instrument this package uses instead is the pid ledger.Record already
// declared and that `estate dispatch` now records the moment it knows it
// (see main.go). A pid that is not running is a positive, checkable fact,
// independent of the worktree.
//
// PID REUSE. "is pid N alive" is not the same question as "is the process I
// launched still alive" -- operating systems recycle pids. A live pid is
// therefore treated as inconclusive (not reclaimed) UNLESS something about
// it actively contradicts being the turn that was dispatched: its command
// name no longer resembles the one launched, or it started measurably after
// the ledger recorded the pid (a later start time means some other process
// took the number after ours had already exited). Both checks are
// best-effort — ps output, not /proc — and Assess's own doc comment says
// plainly what a verdict does and does not establish.
//
// REBOOT. A record older than the host's own boot time cannot name a still
// running process: pids do not survive a reboot. That is checked first and
// does not depend on the process probe at all -- it is also what covers a
// turn dispatched by a supervisor process that has itself since died across
// a restart: whatever pid was recorded, this boot never assigned it.
package reclaim

import (
	"fmt"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

// ProcessInfo is what can be observed about a pid right now.
type ProcessInfo struct {
	// Exists is whether the OS currently has a process at this pid.
	Exists bool
	// StartedAt is when the OS reports that process started, if it could be
	// determined. Zero means unknown, never "at the epoch".
	StartedAt time.Time
	// Comm is the running process's command name, if it could be determined.
	Comm string
}

// Probe answers "what does the OS say about this pid right now". The real
// implementation (PSProbe) shells out to ps; tests supply a fake so the
// decision logic in Assess can be driven without a real process to kill.
type Probe func(pid int) (ProcessInfo, error)

// Assessment is one in-flight record's reclaim verdict.
type Assessment struct {
	Record ledger.Record
	// Reclaimable is true only on a positive observation that the process
	// cannot still be the one dispatched. Anything else -- no pid, a probe
	// failure, an ambiguous live pid -- is false, however old the record.
	// Age is not evidence.
	Reclaimable bool
	// Reason is always populated, reclaimable or not -- reporting is the
	// default behaviour this package exists to provide, and a silent "no"
	// is exactly as unhelpful as a silent "yes".
	Reason string
}

// wantComm is what the launched process's command name should contain. Not
// an exact match -- ps's comm field is truncated and platform-dependent --
// only a check that a plainly different program hasn't taken the pid.
const wantComm = "claude"

// reuseTolerance absorbs the gap between the OS starting the process and
// this program observing and recording its pid: Start() returns before the
// ledger Append() that records it runs, and clocks disagree by small
// amounts even on one host. A live pid that started more than this long
// after the record's own timestamp is judged a later, different process
// reusing the number -- not clock noise.
const reuseTolerance = 10 * time.Second

// Assess decides one record's fate. boot is a parameter, never read
// internally, so this stays a pure function a test can drive exactly --
// see reclaim_test.go. Only ledger.Record values whose state is not already
// terminal make sense to pass in; the caller (Report) is expected to have
// filtered to InFlight already, but Assess does not re-check that itself --
// it answers the pid question for whatever record it is given.
func Assess(rec ledger.Record, boot time.Time, probe Probe) Assessment {
	a := Assessment{Record: rec}

	if rec.PID == 0 {
		a.Reason = "no pid recorded -- cannot observe, leaving in place"
		return a
	}

	if !boot.IsZero() && rec.At.Before(boot) {
		a.Reclaimable = true
		a.Reason = fmt.Sprintf(
			"host booted at %s, after this turn's pid %d was recorded at %s -- pids do not survive a reboot, so the process cannot still be that one",
			boot.Format(time.RFC3339), rec.PID, rec.At.Format(time.RFC3339))
		return a
	}

	info, err := probe(rec.PID)
	if err != nil {
		a.Reason = fmt.Sprintf("could not check pid %d: %s -- leaving in place, blindness is not evidence", rec.PID, err)
		return a
	}

	if !info.Exists {
		a.Reclaimable = true
		a.Reason = fmt.Sprintf("pid %d is not running", rec.PID)
		return a
	}

	if info.Comm != "" && !strings.Contains(info.Comm, wantComm) {
		a.Reclaimable = true
		a.Reason = fmt.Sprintf(
			"pid %d is now %q, not the %q process dispatched here -- the number has been reused",
			rec.PID, info.Comm, wantComm)
		return a
	}

	if !info.StartedAt.IsZero() && info.StartedAt.After(rec.At.Add(reuseTolerance)) {
		a.Reclaimable = true
		a.Reason = fmt.Sprintf(
			"pid %d started at %s, more than %s after this turn's own dispatch record at %s -- a later process reused the number",
			rec.PID, info.StartedAt.Format(time.RFC3339), reuseTolerance, rec.At.Format(time.RFC3339))
		return a
	}

	a.Reason = fmt.Sprintf("pid %d is alive (comm=%q) -- still running, or at least a plausible continuation of the turn dispatched", rec.PID, info.Comm)
	return a
}

// Report assesses every in-flight record without mutating anything. This is
// the function `estate reclaim` calls with no flag -- reporting is the
// default, reclaiming is opt-in (see Apply).
func Report(records []ledger.Record, boot time.Time, probe Probe) []Assessment {
	out := make([]Assessment, 0, len(records))
	for _, r := range records {
		out = append(out, Assess(r, boot, probe))
	}
	return out
}

// Apply appends a terminal Failed record for every reclaimable assessment,
// freeing its slot. It is the only function in this package that writes to
// the ledger -- Assess and Report never do, so a bare `estate reclaim` can
// never mutate state by accident, only `--apply` can.
//
// The terminal state written is Failed, not a new state of its own: this
// package cannot tell whether the turn actually succeeded, only that its
// process is gone, and Failed is the honest label for "we do not know that
// it finished" that already exists and already frees the slot.
func Apply(l *ledger.Ledger, assessments []Assessment) (int, error) {
	n := 0
	for _, a := range assessments {
		if !a.Reclaimable {
			continue
		}
		rec := a.Record
		rec.State = ledger.Failed
		rec.Note = "reclaimed: " + a.Reason
		if err := l.Append(rec); err != nil {
			return n, fmt.Errorf("reclaim: could not record %s as reclaimed: %w", rec.ID, err)
		}
		n++
	}
	return n, nil
}
