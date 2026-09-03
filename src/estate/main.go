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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/corpus"
	"github.com/jonhill90/agent-estate/estate/internal/gate"
	"github.com/jonhill90/agent-estate/estate/internal/isolate"
	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/pressure"
	"github.com/jonhill90/agent-estate/estate/internal/tick"
	"github.com/jonhill90/agent-estate/estate/internal/verifybranch"
)

func usage() {
	fmt.Fprint(os.Stderr, `estate -- the supervisor

  estate pressure                       report whether the host can take work
  estate dispatch [--role=review] <issue> <brief-file>
                                        run one agent turn, gated and recorded
  estate merge <repo> <pr> <issue> <reviewer-lane>
                                        may this PR merge? checks + independence
  estate corpus-audit [n]               hard parameters least supported by your words
  estate authored <issue> <lane> [note] record who wrote work on an issue
  estate tasks                          latest state of every task
  estate inflight                       tasks still occupying a slot
  estate tick record <phase-item> [artifact]
                                        append this tick to the record
  estate tick check                     has the loop stalled? 1 = yes, escalate
  estate verify-branch <branch>         build and test a branch in its OWN tree

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

	case "authored":
		// Records that a lane authored work on an issue. The gate treats a
		// hand-written record as an ASSERTION, not evidence: it narrows who
		// may review, and cannot permit a merge.
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "estate: authored needs an issue and a lane")
			os.Exit(2)
		}
		aIssue, aLane := os.Args[2], os.Args[3]
		aNote := ""
		if len(os.Args) > 4 {
			aNote = os.Args[4]
		}
		if err := l.Append(ledger.Record{
			ID:    fmt.Sprintf("authored-%s-%s", strings.TrimPrefix(aIssue, "#"), aLane),
			Issue: aIssue, Lane: aLane, State: ledger.Authored, Note: aNote,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		fmt.Printf("recorded %s as an author of issue %s\n", aLane, aIssue)

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
			// The phase item must be one the plan names. A stray write --
			// a probe, a typo, a label invented on the spot -- was
			// otherwise indistinguishable from a tick.
			known, err := tick.KnownPhases("docs/phase-plan.md")
			if err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			if err := tick.CheckPhaseItem(os.Args[3], known); err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			artifact := ""
			if len(os.Args) > 4 {
				artifact = os.Args[4]
			}
			// src head is read here rather than in internal/tick so that the
			// package stays a pure reader of the record and can be tested
			// without a git repository.
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
			// Recency, not existence. In a real repository almost
			// everything already exists, so "does this resolve" was
			// satisfied by naming any old file. The question is whether
			// this tick MADE something, so each token is judged against
			// when the previous tick ended. See internal/tick.Validate.
			produced := func(tok string, since time.Time) bool {
				switch {
				case strings.HasPrefix(tok, "#"):
					// A pull request or issue, checked for real rather than
					// assumed. gh unavailable or the number missing means we
					// could not confirm it, which is not confirmation.
					out, err := exec.Command("gh", "pr", "view", strings.TrimPrefix(tok, "#"),
						"--json", "createdAt", "-q", ".createdAt").Output()
					if err != nil {
						out, err = exec.Command("gh", "issue", "view", strings.TrimPrefix(tok, "#"),
							"--json", "createdAt", "-q", ".createdAt").Output()
					}
					if err != nil {
						return false
					}
					at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
					return err == nil && (since.IsZero() || at.After(since))
				default:
					if fi, err := os.Stat(tok); err == nil {
						return since.IsZero() || fi.ModTime().After(since)
					}
					// A commit: use its committer date.
					out, err := exec.Command("git", "log", "-1", "--format=%cI", tok).Output()
					if err != nil {
						return false
					}
					at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
					return err == nil && (since.IsZero() || at.After(since))
				}
			}
			if err := tick.Record(path, e, produced); err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			// SrcHead is empty in a repository with no commit touching src/ --
			// a real state, and slicing it panicked AFTER the record had been
			// appended, so the write succeeded while the command crashed.
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
			// Before reading the record, confirm the record is still there.
			// It lives in a file the Director can delete, and deleting it
			// used to turn a real stall into "no tick log yet".
			committed := -1
			if out, err := exec.Command("git", "show", "HEAD:"+path).Output(); err == nil {
				committed = 0
				for _, ln := range strings.Split(string(out), "\n") {
					if strings.TrimSpace(ln) != "" {
						committed++
					}
				}
			} else if exec.Command("git", "cat-file", "-e", "HEAD:"+path).Run() != nil {
				// Not tracked yet: nothing committed to compare against, which
				// is a legitimate first run rather than a failure to measure.
				committed = 0
			}
			if err := tick.CheckAgainstCommitted(path, committed); err != nil {
				fmt.Fprintln(os.Stderr, "estate: "+err.Error())
				os.Exit(2)
			}
			// Re-apply the writer's rules at read time. Record only guards
			// entries written through this CLI, and entries arrive by other
			// routes -- a hand-edit, a merge, or a probe run without
			// ESTATE_TICK_LOG, which has already landed one line in the
			// production log.
			if known, err := tick.KnownPhases("docs/phase-plan.md"); err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			} else if err := tick.AuditWindow(path, known); err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
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
			// Say what this verdict does NOT establish. The artifact field is
			// written by the agent being measured; five successive rules to
			// validate it were each defeated by an independent reviewer. What
			// is checked on read is shape and phase membership, not that the
			// tick caused the thing it names.
			fmt.Println("note: artifacts are self-reported. This confirms they name something " +
				"openable and sit under a real phase -- not that this loop produced them.")

		default:
			usage()
			os.Exit(2)
		}

	case "verify-branch":
		// Checks a branch where its own contents are the only contents. The
		// caller's working tree carries state from every branch worked on,
		// so a check run there answers a question about that tree, not this
		// branch. See internal/verifybranch's doc comment for the four
		// failures that produced this command.
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "estate: verify-branch needs a branch name")
			os.Exit(2)
		}
		top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate: cannot find the repository root:", err)
			os.Exit(2)
		}
		res, err := verifybranch.Verify(strings.TrimSpace(string(top)), os.Args[2], []string{"src/estate", "src/tui"})
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		for _, st := range res.Steps {
			mark := "ok  "
			if st.Err != nil {
				mark = "FAIL"
			}
			fmt.Printf("%s %s: %s\n", mark, st.Module, st.Cmd)
		}
		if !res.OK() {
			fmt.Fprintf(os.Stderr, "\nrefuse: %s failed on branch %s\n", res.Failed, res.Branch)
			if n := len(res.Steps); n > 0 {
				fmt.Fprintln(os.Stderr, strings.TrimSpace(res.Steps[n-1].Output))
			}
			fmt.Fprintln(os.Stderr, "\nworktree kept for inspection:", res.Worktree)
			os.Exit(1)
		}
		fmt.Printf("\n%s builds, vets and tests clean in its own tree\n", res.Branch)

	case "dispatch":
		if len(os.Args) < 4 {
			usage()
			os.Exit(2)
		}
		rest := os.Args[2:]
		// A turn dispatched to REVIEW an issue is not an author of it. The
		// merge gate requires this record; without a writer the exemption is
		// dead code and every real review is refused as self-review.
		role := ledger.RoleAuthor
		if rest[0] == "--role=review" {
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
		id := fmt.Sprintf("%s-%d", strings.TrimPrefix(issue, "#"), time.Now().UTC().Unix())

		// The turn runs with --dangerously-skip-permissions. Give it a
		// working tree of its own before it starts, or do not start it:
		// inheriting our cwd puts an unattended full-permission agent in the
		// shared checkout. Isolation is established BEFORE the ledger records
		// a dispatch, so a refusal leaves no half-started task behind.
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
		cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "json",
			"--dangerously-skip-permissions")
		cmd.Dir = wt.Path
		cmd.Stdin = strings.NewReader(grounded)
		out, runErr := cmd.Output()

		rec := ledger.Record{ID: id, Issue: issue, Lane: id, Role: role}
		switch {
		case ctx.Err() != nil:
			// Timed out. We do not know whether the turn did its work, so we
			// must not say it failed and must not free the slot.
			rec.State, rec.Note = ledger.Unknown, "timed out after 45m; unknown is not failed"
		case runErr != nil:
			rec.State, rec.Note = ledger.Failed, runErr.Error()
		default:
			rec.State = ledger.Complete
			var parsed map[string]any
			if json.Unmarshal(out, &parsed) == nil {
				if s, ok := parsed["result"].(string); ok {
					rec.Result = s
				}
			} else {
				// Exited 0 but we could not parse what it said. That is not a
				// clean completion; record it honestly.
				rec.State, rec.Note = ledger.Unknown, "exit 0 but result was not parseable JSON"
			}
		}
		// Tear the worktree down only when it is empty. A turn that left work
		// behind has output nobody has collected, and an isolated worktree
		// that still exists is a report; a deleted one is unrecoverable.
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
