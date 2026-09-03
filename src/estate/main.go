// Command estate is the supervisor.
//
// It dispatches agent turns as subprocesses and records them durably. Delivery
// is a process exit and a parsed result -- never an inference from what a
// terminal pane appears to show. A turn that cannot be observed is recorded
// "unknown", which is not terminal: unknown is not failed, and it keeps
// occupying a slot until something says otherwise.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/corpus"
	"github.com/jonhill90/agent-estate/estate/internal/gate"
	"github.com/jonhill90/agent-estate/estate/internal/harness"
	"github.com/jonhill90/agent-estate/estate/internal/isolate"
	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/pressure"
	"github.com/jonhill90/agent-estate/estate/internal/tick"
)

func usage() {
	fmt.Fprint(os.Stderr, `estate -- the supervisor

  estate pressure                       report whether the host can take work
  estate dispatch [--harness=NAME] [--role=review] <issue> <brief-file>
                                        run one agent turn, gated and recorded
  estate merge <repo> <pr> <issue> <reviewer-lane>
                                        may this PR merge? checks + independence
  estate corpus-audit [n]               hard parameters least supported by your words
  estate tasks                          latest state of every task
  estate inflight                       tasks still occupying a slot
  estate tick record <phase-item> [artifact]
                                        append this tick to the record
  estate tick check                     has the loop stalled? 1 = yes, escalate
  estate authored <issue> <lane> [note] record who wrote work on an issue

Every gate fails closed: a limit that cannot be measured refuses.
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	l, err := ledger.Open(os.Getenv("ESTATE_LEDGER"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "estate: ledger unavailable:", err)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "pressure":
		v := pressure.Check(l, pressure.Default())
		fmt.Printf("load %.2f/core  free %.0fMB  inflight %d  weekly budget %.0f%% left\n",
			v.Reading.LoadPerCore, v.Reading.FreeMemMB, v.Reading.InFlight, v.Reading.WeeklyRemaining)
		if !v.OK {
			for _, r := range v.Reasons {
				fmt.Fprintln(os.Stderr, "refuse: "+r)
			}
			os.Exit(1)
		}
		fmt.Println("within limits")

	case "corpus-audit":
		fs, err := corpus.Audit()
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		limit := 20
		if len(os.Args) > 2 {
			fmt.Sscanf(os.Args[2], "%d", &limit)
		}
		suspect := 0
		for _, f := range fs {
			if len(f.Invented) > 0 || f.Overlap < 0.25 {
				suspect++
			}
		}
		fmt.Printf("%d hard parameters audited; %d assert more than the prompt behind them\n\n", len(fs), suspect)
		for i, f := range fs {
			if i >= limit {
				break
			}
			fmt.Printf("[%s] overlap %.0f%%", f.ItemID, f.Overlap*100)
			if len(f.Invented) > 0 {
				fmt.Printf("  ADDED OBLIGATION: %s", strings.Join(f.Invented, ", "))
			}
			if f.PromptIsQuestion {
				fmt.Printf("  (source prompt is a QUESTION)")
			}
			fmt.Printf("\n  param:  %s\n  prompt: %.220s\n\n", f.Param, f.Prompt)
		}
		if suspect > 0 {
			os.Exit(1)
		}

	case "merge":
		if len(os.Args) < 6 {
			usage()
			os.Exit(2)
		}
		repo, issue, reviewer := os.Args[2], os.Args[4], os.Args[5]
		var pr int
		if _, err := fmt.Sscanf(os.Args[3], "%d", &pr); err != nil {
			fmt.Fprintln(os.Stderr, "estate: pr must be a number:", os.Args[3])
			os.Exit(2)
		}
		d := gate.Evaluate(repo, pr, reviewer, issue, l)
		fmt.Printf("%s#%d head %s\n", repo, pr, d.HeadOID)
		if !d.Allow {
			for _, r := range d.Reasons {
				fmt.Fprintln(os.Stderr, "refuse: "+r)
			}
			os.Exit(1)
		}
		fmt.Println("may merge: all checks green at head, reviewer is not the author")

	case "tasks", "inflight":
		var rs []ledger.Record
		if os.Args[1] == "tasks" {
			rs, err = l.Current()
		} else {
			rs, err = l.InFlight()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		for _, r := range rs {
			fmt.Printf("%-28s %-10s %s %s\n", r.ID, r.State, r.At.Format(time.RFC3339), r.Issue)
		}
		fmt.Fprintf(os.Stderr, "%d task(s)\n", len(rs))

	case "tick":
		// The Director's loop cannot remember its own history -- every tick is
		// a fresh context -- so the stop condition in the brief only binds if
		// it is a command. See internal/tick's doc comment.
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		path := tick.Path()
		switch os.Args[2] {
		case "record":
			if len(os.Args) < 4 {
				fmt.Fprintln(os.Stderr, "estate: tick record needs a phase item")
				os.Exit(2)
			}
			artifact := ""
			if len(os.Args) > 4 {
				artifact = os.Args[4]
			}
			// src head is read here rather than in internal/tick so that the
			// package stays a pure reader of the record and can be tested
			// without a git repository. (Other packages here do shell out --
			// corpus, pressure, quota and gate all do -- so this is a choice
			// about testability, not a rule about internal/.)
			head, err := exec.Command("git", "log", "-1", "--format=%H", "--", "src/").Output()
			if err != nil {
				fmt.Fprintln(os.Stderr, "estate: cannot read src head, refusing to write a tick that cannot be compared:", err)
				os.Exit(2)
			}
			e := tick.Entry{
				At:        time.Now().UTC(),
				PhaseItem: os.Args[3],
				SrcHead:   strings.TrimSpace(string(head)),
				Artifact:  artifact,
			}
			if err := tick.Record(path, e); err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			// e.SrcHead is empty in a repository with no commit touching
			// src/ -- a real state, and slicing it panicked AFTER the record
			// had already been appended, so the write succeeded while the
			// command crashed. Found by an independent review.
			shortHead := e.SrcHead
			if len(shortHead) > 8 {
				shortHead = shortHead[:8]
			}
			if shortHead == "" {
				shortHead = "(no src commit)"
			}
			if artifact == "" {
				fmt.Printf("recorded %s at %s with no artifact\n", e.PhaseItem, shortHead)
			} else {
				fmt.Printf("recorded %s at %s -> %s\n", e.PhaseItem, shortHead, artifact)
			}

		case "check":
			v, err := tick.Check(path)
			if err != nil {
				// Could not measure. Never clean.
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			if v.Stalled {
				fmt.Fprintln(os.Stderr, "STALLED: "+v.Reason)
				fmt.Fprintln(os.Stderr, "stop ticking and escalate (brief section 3)")
				os.Exit(1)
			}
			fmt.Println("moving: " + v.Reason)

		default:
			usage()
			os.Exit(2)
		}

	case "authored":
		// Records that a lane authored work on an issue. The merge gate reads
		// this to answer "who wrote this?", and refuses when it cannot -- so
		// without a way to WRITE the fact, every PR not produced by a
		// dispatched turn was unmergeable forever. This does not relax the
		// gate; it gives it something true to read.
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "estate: authored needs an issue and a lane")
			os.Exit(2)
		}
		issue, lane := os.Args[2], os.Args[3]
		note := ""
		if len(os.Args) > 4 {
			note = os.Args[4]
		}
		rec := ledger.Record{
			ID:    fmt.Sprintf("authored-%s-%s", strings.TrimPrefix(issue, "#"), lane),
			Issue: issue,
			Lane:  lane,
			State: ledger.Authored,
			Note:  note,
		}
		if err := l.Append(rec); err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		fmt.Printf("recorded %s as an author of issue %s\n", lane, issue)

	case "dispatch":
		if len(os.Args) < 4 {
			usage()
			os.Exit(2)
		}
		rest := os.Args[2:]
		harnessName := os.Getenv("ESTATE_HARNESS")
		if harnessName == "" {
			harnessName = "claude"
		}
		if strings.HasPrefix(rest[0], "--harness=") {
			harnessName = strings.TrimPrefix(rest[0], "--harness=")
			rest = rest[1:]
		}
		// A turn dispatched to review an issue is not an author of it. The
		// merge gate reads this; without it a reviewer is refused as a
		// self-reviewer of the work it was sent to check.
		role := ledger.RoleAuthor
		if len(rest) > 0 && rest[0] == "--role=review" {
			role = ledger.RoleReview
			rest = rest[1:]
		}
		if len(rest) < 2 {
			usage()
			os.Exit(2)
		}
		issue, briefPath := rest[0], rest[1]
		brief, err := os.ReadFile(briefPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate: cannot read brief:", err)
			os.Exit(2)
		}
		// The operator's parameters are law and outrank the brief. If they
		// cannot be read we refuse: an agent working without them is exactly
		// how a month went into a layer the corpus had already ruled out.
		params, err := corpus.Hard()
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate: refusing to dispatch --", err)
			os.Exit(1)
		}
		grounded := corpus.Grounding(issue+" "+string(brief), params) + string(brief)

		if v := pressure.Check(l, pressure.Default()); !v.OK {
			fmt.Fprintln(os.Stderr, "estate: refusing to dispatch --")
			for _, r := range v.Reasons {
				fmt.Fprintln(os.Stderr, "  "+r)
			}
			os.Exit(1)
		}
		// Which harness runs this turn. Refused rather than defaulted when
		// unknown: silently falling back would run the turn somewhere the
		// caller did not choose.
		h, err := harness.Lookup(harnessName)
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate: refusing to dispatch --", err)
			os.Exit(1)
		}
		if ok, _ := harness.Available(harnessName); !ok {
			fmt.Fprintf(os.Stderr, "estate: refusing to dispatch -- harness %q is registered but its binary is not on PATH\n", harnessName)
			os.Exit(1)
		}

		id := fmt.Sprintf("%s-%d", strings.TrimPrefix(issue, "#"), time.Now().UTC().Unix())

		// The turn runs with --dangerously-skip-permissions. Give it a working
		// tree of its own before it starts, or do not start it: inheriting our
		// cwd puts an unattended full-permission agent in the shared checkout.
		// Isolation is established BEFORE the ledger records a dispatch, so a
		// refusal leaves no half-started task behind.
		topOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate: refusing to dispatch -- cannot locate the repository root to isolate the turn:", err)
			os.Exit(1)
		}
		wt, err := isolate.Create(strings.TrimSpace(string(topOut)), id)
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate: "+err.Error())
			os.Exit(1)
		}

		if err := l.Append(ledger.Record{ID: id, Issue: issue, Lane: id, Role: role, State: ledger.Dispatched, Note: "worktree " + wt.Path}); err != nil {
			fmt.Fprintln(os.Stderr, "estate: cannot record dispatch:", err)
			os.Exit(2)
		}
		fmt.Printf("dispatched %s in %s (grounded in %d operator parameters)\n", id, wt.Path, len(params))

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		turn, err := h.Start(ctx, wt.Path, grounded)
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		defer turn.Cleanup()
		out, runErr := turn.Cmd.Output()

		rec := ledger.Record{ID: id, Issue: issue, Lane: id, Role: role}
		switch {
		case ctx.Err() != nil:
			// Timed out. We do not know whether the turn did its work, so we
			// must not say it failed and must not free the slot.
			rec.State, rec.Note = ledger.Unknown, "timed out after 45m; unknown is not failed"
		case runErr != nil:
			rec.State, rec.Note = ledger.Failed, runErr.Error()
		default:
			// Reading the result is the harness's job -- each one emits its
			// own shape. Exit 0 with output we cannot read is NOT a clean
			// completion, and is recorded as unknown rather than failed:
			// the turn may well have done its work.
			res, resErr := turn.Result(out)
			if resErr != nil {
				rec.State, rec.Note = ledger.Unknown, resErr.Error()
			} else {
				rec.State, rec.Result = ledger.Complete, res
			}
		}
		// Tear the worktree down only when it is empty. A turn that left work
		// behind has output nobody has collected, and an isolated worktree that
		// still exists is a report; a deleted one is unrecoverable.
		if err := wt.Remove(); err != nil {
			rec.Note = strings.TrimSpace(rec.Note + "; " + wt.Path + " kept: " + err.Error())
			fmt.Fprintln(os.Stderr, "estate: "+err.Error())
		}

		if err := l.Append(rec); err != nil {
			fmt.Fprintln(os.Stderr, "estate: cannot record outcome:", err)
			os.Exit(2)
		}
		fmt.Printf("%s %s\n", id, rec.State)
		if rec.State != ledger.Complete {
			os.Exit(1)
		}

	default:
		usage()
		os.Exit(2)
	}
}
