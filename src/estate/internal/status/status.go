// Package status answers "where are we" by deriving it, every time it is
// asked, from the records that already exist.
//
// WHY THIS EXISTS (agent-estate#1012). Four records describe this estate and
// none of them reference each other: the corpus holds what the operator asked
// for, the forge holds open issues and pull requests, the ledger holds what
// actually ran, and docs/phase-plan.md holds the intended shape. Answering
// the question took four queries stitched by hand, so it kept being asked and
// kept not being answered.
//
// DERIVED, NEVER AUTHORITATIVE. Nothing here is cached and nothing here is
// written down. Every figure in a Report is computed at the moment Build is
// called, from a source that owns it. A stored summary is the defect being
// fixed, not the fix: docs/phase-plan.md's own hand-written status strings
// said Phase 0 was `NOT ON MAIN` at b45f917 while `git log origin/main |
// grep -c '(#914)'` returned 1. Three of its four strings were right, which
// is worse than all four being wrong -- a mostly-correct hand-maintained
// field reads as maintained. So this package takes the pull request NUMBERS
// out of the plan (via internal/phaseplan) and asks git whether they are on
// main, and never reads the words beside them.
//
// ABSENCE IS A STATE, EVERYWHERE. Every section of a Report can fail
// independently and says so in its own field: a forge call that could not be
// made yields an error, never an empty list that would print as "nothing is
// waiting". A phase whose pull requests cannot be resolved -- because the
// plan names none, or because git could not be read -- is PhaseUnknown, never
// "not started". A ledger record with no phase attribution is counted as
// unattributed and named as such, never distributed across the phases to make
// the totals look complete. A tick log entry whose phase item the plan does
// not name is reported under its own literal text as unknown, never bucketed
// into the nearest real phase.
//
// The ledger is only ever read here (Current/InFlight both open it O_RDONLY).
package status

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/phaseplan"
)

// Item is one open thing on the forge -- a pull request or an issue.
type Item struct {
	Number    int
	Title     string
	CreatedAt time.Time
	// Draft is meaningful for pull requests only.
	Draft bool
}

// Turn is one in-flight ledger record with the age that makes it a signal.
// `estate inflight` already lists these; a list without ages cannot tell a
// turn that started ninety seconds ago from one wedged since Tuesday.
type Turn struct {
	Record ledger.Record
	Age    time.Duration
}

// PhaseState is what can honestly be said about one phase's progress.
type PhaseState int

const (
	// PhaseUnknown: the phase's progress could not be resolved. Either the
	// plan names no pull request for it, or git could not be read. It is
	// NEVER a synonym for "nothing done" -- see the package doc comment.
	PhaseUnknown PhaseState = iota
	// PhaseNoneOnMain: every pull request the plan names for this phase was
	// looked for in main's history and none is there.
	PhaseNoneOnMain
	// PhaseSomeOnMain: some of the named pull requests are on main, some are
	// not.
	PhaseSomeOnMain
	// PhaseAllOnMain: every pull request the plan names for this phase is on
	// main. That is a statement about those pull requests, not a claim the
	// phase's intent is fulfilled -- the plan may simply not name everything.
	PhaseAllOnMain
)

func (s PhaseState) String() string {
	switch s {
	case PhaseNoneOnMain:
		return "NOT ON MAIN"
	case PhaseSomeOnMain:
		return "PARTLY ON MAIN"
	case PhaseAllOnMain:
		return "ON MAIN"
	default:
		return "UNKNOWN"
	}
}

// Phase is one phase of the plan, joined to what actually happened.
type Phase struct {
	Plan phaseplan.Phase
	// Merged and Unmerged partition Plan.PRs by whether main's own history
	// carries them. Both empty when State is PhaseUnknown.
	Merged   []int
	Unmerged []int
	State    PhaseState
	// Why states, in one sentence, what the state was derived from --
	// including why it could not be derived, when it could not.
	Why string
	// LedgerTurns is how many ledger records (latest per task) were
	// dispatched against this phase. Zero is a real measurement -- no turn
	// named this phase -- and must be read alongside Report.Unattributed,
	// which is how many turns named no phase at all.
	LedgerTurns int
}

// LabelCount is one phase item found in the tick log and how often.
type LabelCount struct {
	Label string
	Count int
}

// Report is the whole answer. Every section carries its own error, so one
// unreachable source narrows the report instead of emptying it.
type Report struct {
	Now time.Time

	InFlight    []Turn
	InFlightErr error

	OpenPRs       []Item
	OpenPRsErr    error
	OpenIssues    []Item
	OpenIssuesErr error

	// MainRef names which ref main's history was read from, so a reader can
	// tell an origin/main answer from a local-main one that may be behind.
	MainRef   string
	MergedErr error
	PlanErr   error
	Phases    []Phase

	// Unattributed is how many ledger records (latest per task) carry no
	// phase at all -- every record written before ledger.Record.Phase
	// existed, plus every dispatch that named no phase since. Named out loud
	// because the per-phase counts above are only as complete as this number
	// is small.
	Unattributed int
	// LedgerRecords is how many records the per-phase counts were drawn from.
	LedgerRecords int
	LedgerErr     error

	// TickKnown and TickUnknown are the tick log's own phase items, split by
	// whether the plan names them. TickUnknown keeps each unrecognised label
	// verbatim -- `ph` (a typo), `delivery-unblock` and `phase-plan` (not
	// phases) are all on record and history is not rewritten to hide them.
	TickKnown   []LabelCount
	TickUnknown []LabelCount
	TickErr     error
}

// Config supplies every outside-world read as a function, the same adapter
// discipline internal/tick.Resolve already uses: this package makes no
// process, opens no socket, and tests with no git checkout, no gh and no
// network.
type Config struct {
	Now time.Time
	// PlanPath is docs/phase-plan.md.
	PlanPath string
	// InFlight and Current read the ledger, read-only.
	InFlight func() ([]ledger.Record, error)
	Current  func() ([]ledger.Record, error)
	// MergedPRs answers "which pull request numbers does main's history
	// carry", returning the ref it actually read. An error here makes every
	// phase PhaseUnknown -- it must never make them all look unstarted.
	MergedPRs func() (prs map[int]bool, ref string, err error)
	// OpenPRs and OpenIssues ask the forge. An error is carried into the
	// report as an error; it never becomes an empty list.
	OpenPRs    func() ([]Item, error)
	OpenIssues func() ([]Item, error)
	// TickPhaseItems returns the phase item of every entry in the tick log,
	// in file order.
	TickPhaseItems func() ([]string, error)
}

// Build derives the whole report. It never returns an error: a source that
// could not be read is a fact about the estate, recorded in the section it
// belongs to, not a reason to answer nothing.
func Build(cfg Config) Report {
	now := cfg.Now
	if now.IsZero() {
		now = time.Now()
	}
	r := Report{Now: now}

	if cfg.InFlight != nil {
		recs, err := cfg.InFlight()
		r.InFlightErr = err
		for _, rec := range recs {
			r.InFlight = append(r.InFlight, Turn{Record: rec, Age: now.Sub(rec.At)})
		}
		// Oldest first: the turn most likely to be stuck reads first.
		sort.SliceStable(r.InFlight, func(i, j int) bool { return r.InFlight[i].Age > r.InFlight[j].Age })
	}

	if cfg.OpenPRs != nil {
		r.OpenPRs, r.OpenPRsErr = cfg.OpenPRs()
	}
	if cfg.OpenIssues != nil {
		r.OpenIssues, r.OpenIssuesErr = cfg.OpenIssues()
	}

	// Phase attribution from the ledger: what actually ran, grouped by what
	// it was trying to do. Records with no phase are counted separately, not
	// spread across the phases.
	byPhase := map[string]int{}
	if cfg.Current != nil {
		recs, err := cfg.Current()
		r.LedgerErr = err
		r.LedgerRecords = len(recs)
		for _, rec := range recs {
			if strings.TrimSpace(rec.Phase) == "" {
				r.Unattributed++
				continue
			}
			byPhase[rec.Phase]++
		}
	}

	var plan []phaseplan.Phase
	if cfg.PlanPath != "" {
		plan, r.PlanErr = phaseplan.Parse(cfg.PlanPath)
	}

	var merged map[int]bool
	if cfg.MergedPRs != nil {
		merged, r.MainRef, r.MergedErr = cfg.MergedPRs()
	}

	for _, p := range plan {
		ph := Phase{Plan: p, LedgerTurns: byPhase[p.ID]}
		switch {
		case len(p.PRs) == 0:
			ph.State = PhaseUnknown
			ph.Why = "the plan names no pull request for this phase, so there is nothing to look for in main -- unknown, not unstarted"
		case r.MergedErr != nil || merged == nil:
			ph.State = PhaseUnknown
			ph.Why = "main's history could not be read, so whether these pull requests landed is unmeasured, not unfinished"
		default:
			for _, pr := range p.PRs {
				if merged[pr] {
					ph.Merged = append(ph.Merged, pr)
				} else {
					ph.Unmerged = append(ph.Unmerged, pr)
				}
			}
			switch {
			case len(ph.Unmerged) == 0:
				ph.State = PhaseAllOnMain
				ph.Why = fmt.Sprintf("every pull request the plan names (%s) is in %s's history", numbers(ph.Merged), r.MainRef)
			case len(ph.Merged) == 0:
				ph.State = PhaseNoneOnMain
				ph.Why = fmt.Sprintf("none of the pull requests the plan names (%s) is in %s's history", numbers(ph.Unmerged), r.MainRef)
			default:
				ph.State = PhaseSomeOnMain
				ph.Why = fmt.Sprintf("%s in %s's history, %s not", numbers(ph.Merged), r.MainRef, numbers(ph.Unmerged))
			}
		}
		r.Phases = append(r.Phases, ph)
	}

	// A phase named by a ledger record but absent from the plan is not
	// silently dropped: it becomes a phase-less bucket the reader can see.
	for id, n := range byPhase {
		if phaseplan.Known(plan, id) {
			continue
		}
		r.Phases = append(r.Phases, Phase{
			Plan:        phaseplan.Phase{ID: id, Title: "(not a phase in the plan)"},
			State:       PhaseUnknown,
			Why:         "ledger records name this phase but the plan does not -- either the plan moved or the dispatch named something the plan never had",
			LedgerTurns: n,
		})
	}

	if cfg.TickPhaseItems != nil {
		items, err := cfg.TickPhaseItems()
		r.TickErr = err
		known, unknown := map[string]int{}, map[string]int{}
		for _, it := range items {
			if phaseplan.Known(plan, it) {
				known[it]++
			} else {
				unknown[it]++
			}
		}
		r.TickKnown = counts(known)
		r.TickUnknown = counts(unknown)
	}
	return r
}

func counts(m map[string]int) []LabelCount {
	out := make([]LabelCount, 0, len(m))
	for k, v := range m {
		out = append(out, LabelCount{Label: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func numbers(ns []int) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, fmt.Sprintf("#%d", n))
	}
	return strings.Join(parts, ", ")
}

// Age renders a duration the way a human reads a backlog: "4m", "3h12m",
// "2d4h". A negative age (a record stamped in the future, clock skew) is
// reported as such rather than rendered as a plausible small number.
func Age(d time.Duration) string {
	if d < 0 {
		return "future?"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// Unresolved is how many sections of this report could not be read. It is
// what a caller turns into an exit code: a report that could not ask must
// never exit the same way as one that asked and found nothing.
func (r Report) Unresolved() int {
	n := 0
	for _, err := range []error{r.InFlightErr, r.OpenPRsErr, r.OpenIssuesErr, r.MergedErr, r.PlanErr, r.LedgerErr, r.TickErr} {
		if err != nil {
			n++
		}
	}
	return n
}

// Render writes the report. Every "could not" is printed where the figure it
// replaces would have gone, so a narrowed report cannot be skimmed as a
// complete one.
func Render(w io.Writer, r Report) {
	fmt.Fprintf(w, "estate status -- derived at %s, nothing cached\n\n", r.Now.UTC().Format(time.RFC3339))

	fmt.Fprintln(w, "IN FLIGHT NOW (ledger, non-terminal state)")
	switch {
	case r.InFlightErr != nil:
		fmt.Fprintf(w, "  could not read the ledger: %v\n", r.InFlightErr)
	case len(r.InFlight) == 0:
		fmt.Fprintln(w, "  nothing in flight -- the ledger was read and holds no non-terminal turn")
	default:
		for _, t := range r.InFlight {
			phase := t.Record.Phase
			if phase == "" {
				phase = "no phase"
			}
			fmt.Fprintf(w, "  %-30s %-10s age %-8s issue %-6s role=%-8s %s\n",
				t.Record.ID, t.Record.State, Age(t.Age), t.Record.Issue, t.Record.EffectiveRole(), phase)
		}
		fmt.Fprintf(w, "  %d turn(s) in flight. `unknown` is not terminal and is not failure -- it is a turn nobody could observe.\n", len(r.InFlight))
	}

	fmt.Fprintln(w, "\nWAITING (forge, open now)")
	renderItems(w, "pull request", r.OpenPRs, r.OpenPRsErr, r.Now)
	renderItems(w, "issue", r.OpenIssues, r.OpenIssuesErr, r.Now)

	fmt.Fprintln(w, "\nPHASE PROGRESS (plan's PR numbers vs main's own history -- never the plan's status strings)")
	if r.PlanErr != nil {
		fmt.Fprintf(w, "  could not read the plan: %v\n", r.PlanErr)
	}
	if r.MergedErr != nil {
		fmt.Fprintf(w, "  could not read main's history: %v\n", r.MergedErr)
	}
	for _, p := range r.Phases {
		title := p.Plan.Title
		if title != "" {
			title = " -- " + title
		}
		fmt.Fprintf(w, "  %-9s %-15s%s\n", p.Plan.ID, p.State, title)
		fmt.Fprintf(w, "            %s\n", p.Why)
		fmt.Fprintf(w, "            ledger: %d turn(s) dispatched against this phase\n", p.LedgerTurns)
	}
	if r.LedgerErr != nil {
		fmt.Fprintf(w, "  phase attribution unavailable: %v\n", r.LedgerErr)
	} else {
		fmt.Fprintf(w, "  %d of %d ledger record(s) carry no phase at all (every record predating the field, plus every dispatch that named none) --\n"+
			"  the per-phase counts above are complete only to the extent this number is small.\n", r.Unattributed, r.LedgerRecords)
	}

	fmt.Fprintln(w, "\nTICK LOG PHASE ITEMS (history is not rewritten; unrecognised labels are shown as themselves)")
	if r.TickErr != nil {
		fmt.Fprintf(w, "  could not read the tick log: %v\n", r.TickErr)
	}
	for _, c := range r.TickKnown {
		fmt.Fprintf(w, "  %-20s %d\n", c.Label, c.Count)
	}
	for _, c := range r.TickUnknown {
		fmt.Fprintf(w, "  %-20s %d   UNKNOWN -- not a phase the plan names\n", c.Label, c.Count)
	}
	if len(r.TickKnown) == 0 && len(r.TickUnknown) == 0 && r.TickErr == nil {
		fmt.Fprintln(w, "  no tick entries on record")
	}

	if n := r.Unresolved(); n > 0 {
		fmt.Fprintf(w, "\n%d source(s) could not be read; the sections above say which. This report is narrower than it looks.\n", n)
	}
}

func renderItems(w io.Writer, kind string, items []Item, err error, now time.Time) {
	if err != nil {
		fmt.Fprintf(w, "  could not ask the forge for open %ss: %v\n", kind, err)
		fmt.Fprintf(w, "  (this is not zero open %ss -- nobody asked successfully)\n", kind)
		return
	}
	if len(items) == 0 {
		fmt.Fprintf(w, "  no open %ss -- asked, and there are none\n", kind)
		return
	}
	for _, it := range items {
		age := "age unknown"
		if !it.CreatedAt.IsZero() {
			age = "age " + Age(now.Sub(it.CreatedAt))
		}
		draft := ""
		if it.Draft {
			draft = " [draft]"
		}
		fmt.Fprintf(w, "  %-6s #%-6d %-10s %.70s%s\n", kind[:2], it.Number, age, it.Title, draft)
	}
	fmt.Fprintf(w, "  %d open %s(s)\n", len(items), kind)
}
