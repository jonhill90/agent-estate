// Command estate is the supervisor.
//
// It dispatches agent turns as subprocesses and records them durably. Delivery
// is a process exit and a parsed result -- never an inference from what a
// terminal pane appears to show. A turn that cannot be observed is recorded
// "unknown", which is not terminal: unknown is not failed, and it keeps
// occupying a slot until something says otherwise.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/corpus"
	"github.com/jonhill90/agent-estate/estate/internal/dispatchid"
	"github.com/jonhill90/agent-estate/estate/internal/gate"
	"github.com/jonhill90/agent-estate/estate/internal/isolate"
	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/pressure"
	"github.com/jonhill90/agent-estate/estate/internal/reclaim"
	"github.com/jonhill90/agent-estate/estate/internal/tick"
	"github.com/jonhill90/agent-estate/estate/internal/verifybranch"
)

// stderrTailLimit bounds how much of a failed child's stderr enters the
// ledger. The ledger is append-only JSONL; an unbounded blob turns one bad
// turn into a file nobody can read back, so we keep a tail and say so.
const stderrTailLimit = 4096

// stderrSegment turns a captured stderr blob into a ledger-note fragment.
// Absence is a typed value, not a bare zero: "captured, and it was blank"
// must read differently from "nobody looked" -- the same discipline this
// repo already applies to cost.Figure.Known and session.Worktree.Clean.
// examined=false is not reachable from main today (cmd.Stderr is always
// wired to a buffer before the child runs) but is kept as an explicit
// parameter, not inferred from a nil/empty slice, so the distinction stays
// checkable by a test even though production always examines.
func stderrSegment(stderr []byte, examined bool) string {
	if !examined {
		return "stderr: not captured"
	}
	s := strings.TrimSpace(string(stderr))
	if s == "" {
		return "stderr: (empty)"
	}
	if len(s) > stderrTailLimit {
		return fmt.Sprintf("stderr (truncated to last %d of %d bytes): %s",
			stderrTailLimit, len(s), s[len(s)-stderrTailLimit:])
	}
	return "stderr: " + s
}

// stdoutDiagnosticSegment extracts a bounded, non-echoing diagnostic
// fragment from a failed child's stdout. agent-estate#955's review: for a
// bad or inaccessible model -- the same class as a quota or entitlement
// error -- `claude -p --output-format json` puts its own structured error
// on STDOUT as {"is_error":true,...,"result":"..."} and leaves stderr
// completely empty, so stderrSegment alone drops the one diagnostic the CLI
// actually produced.
//
// This does NOT dump raw stdout. Two deliberate narrowings, both there to
// keep the operator's raw prompt out of the ledger (a standing hard rule --
// the ledger is durable and append-only):
//
//   - Unparseable JSON is recorded as unparseable, with a byte count, never
//     stored raw. Dumping raw stdout on failure is exactly the path that
//     would leak an echoed prompt if the child ever wrote one there.
//   - Parseable JSON only contributes its `result` text when `is_error` is
//     true. `is_error=true` is the CLI's own classification that this text
//     is ITS diagnostic, not the model's generated content -- and the
//     model's own content is exactly where a partial prompt echo could
//     live. When `is_error` is false (or absent), `result` is left alone.
//
// The extracted result is still passed through the same bounded-tail
// truncation as stderrSegment, for the same append-only-ledger reason.
func stdoutDiagnosticSegment(stdout []byte) string {
	s := strings.TrimSpace(string(stdout))
	if s == "" {
		return "stdout: (empty)"
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return fmt.Sprintf("stdout: unparseable JSON (%d bytes, not recorded)", len(s))
	}
	isError, _ := parsed["is_error"].(bool)
	if !isError {
		// Not the CLI's own error classification -- do not guess at
		// extracting text from it, since it may be the model's own
		// (possibly prompt-echoing) content.
		return "stdout: parsed JSON, is_error=false"
	}
	fields := []string{"is_error=true"}
	if subtype, ok := parsed["subtype"].(string); ok && subtype != "" {
		fields = append(fields, "subtype="+subtype)
	}
	if result, ok := parsed["result"].(string); ok {
		result = strings.TrimSpace(result)
		if result != "" {
			if len(result) > stderrTailLimit {
				result = fmt.Sprintf("%s (truncated, %d bytes total)", result[:stderrTailLimit], len(result))
			}
			fields = append(fields, "result="+result)
		}
	}
	return "stdout: " + strings.Join(fields, ", ")
}

// reviewerContract states the second-source verdict contract the merge gate
// (internal/gate) already enforces, filled in with the values the estate
// itself knows at dispatch time -- the lane's own dispatch id and the PR
// number it was dispatched against -- rather than left for a brief to
// restate and inevitably drift (agent-estate#949). Every failure named in
// #949 was an agent inventing or reformatting one of these two values:
// a Review-Lane: trailer that was branch-prefixed or an invented label
// instead of the bare id, and a ledger Result with no parsable Verdict:
// line at all. Stating the contract here, with the id and PR number already
// filled in, removes the inventing.
func reviewerContract(id string, pr int) string {
	return "\n\n## Reviewer contract (agent-estate#949 -- read this before posting a verdict)\n" +
		"You are reviewing PR #" + strconv.Itoa(pr) + " as lane `" + id + "`. The merge gate " +
		"cross-checks two independent sources and refuses the merge if either is missing or " +
		"unparsable -- comply with both exactly:\n\n" +
		"1. **Your PR comment** must contain a line reading exactly `Verdict: APPROVE` or " +
		"`Verdict: REQUEST CHANGES`, plus `Review-Lane: " + id + "` and `Reviewed-SHA: <the sha you reviewed>` " +
		"trailers. State `Review-Lane:` as `" + id + "` -- the bare id, not `" + gate.DispatchBranchPrefix + id + "` " +
		"or any other label. (The gate strips one leading `" + gate.DispatchBranchPrefix + "` before comparing, " +
		"so the branch-prefixed form would still pass -- but the bare id is what's asked for here, and " +
		"an invented label is not accepted at all.)\n" +
		"2. **Your own final result text** -- what you return when you finish, not only the PR " +
		"comment -- must ALSO contain a `Verdict: APPROVE` or `Verdict: REQUEST CHANGES` line. A " +
		"prose summary such as \"Review posted: **APPROVE**\" does not parse, and the gate will " +
		"refuse a genuine approval it cannot read.\n"
}

// roleGrounding is what dispatch appends to the prompt based on role alone
// -- the author's branch-discipline block (agent-estate#940, text
// unchanged by #949) or the reviewer's verdict contract (reviewerContract,
// #949). Neither role gets both, and a role that is neither (there is none
// today) gets nothing.
func roleGrounding(role ledger.Role, id string, reviewPR int, branch string) string {
	switch role {
	case ledger.RoleAuthor:
		return "\n\n## Branch discipline (agent-estate#940 -- read this before opening a PR)\n" +
			"Your worktree's branch is already `" + branch + "` -- created by the estate " +
			"itself, not by you. Commit your work on THIS branch and push it AS-IS:\n\n" +
			"    git push -u origin " + branch + "\n\n" +
			"Open your pull request FROM `" + branch + "`. Do not `git checkout -b` a " +
			"hand-named branch (e.g. `feat/...`, `fix/...`) and open the PR from that instead --" +
			" the merge gate (`estate merge`) derives authorship by reading the PR's own head " +
			"ref back and joining it to this exact dispatch's ledger record. A PR opened from " +
			"any other branch carries no evidence the estate produced, and the gate refuses it " +
			"structurally, with no override.\n"
	case ledger.RoleReviewer:
		return reviewerContract(id, reviewPR)
	default:
		return ""
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `estate -- the supervisor

  estate pressure                       report whether the host can take work
  estate dispatch <issue> <brief-file>  run one agent turn (role=author), gated and recorded
  estate dispatch review <pr> <issue> <brief-file>
                                        run one review turn (role=reviewer) against a PR
  estate merge <repo> <pr> <reviewer-lane>
                                        may this PR merge? identity comes from the PR's
                                        own head ref (must be a dispatch/<id> branch),
                                        never a caller argument and never a closing issue
                                        -- checks green, author != reviewer, reviewer
                                        actually completed a review, and posted APPROVE
  estate corpus-audit [n]               hard parameters least supported by your words
  estate knowledge                      regenerate the compiled, read-only index over
                                         GitHub stars, the memory vault, the corpus and
                                         Loops-Research -- derived, never authoritative
  estate tasks                          latest state of every task
  estate inflight                       tasks still occupying a slot
  estate reclaim [--apply]              report in-flight turns and whether their
                                        process is still alive; --apply frees the
                                        slot for any turn positively observed dead
  estate tick record <phase-item> [artifact]
                                        append this tick to the record
  estate tick check                     has the loop stalled? resolves each
                                        artifact in the window for real
                                        (a request, a status) rather than
                                        trusting its shape; 1 = yes, escalate
  estate tick verify                    sweep the whole log and report how
                                        many artifacts resolve, how many
                                        don't, and how many could not be
                                        checked -- diagnostic, does not gate
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

	case "knowledge":
		cfg, err := knowledge.DefaultConfig()
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		out, err := knowledge.DefaultOutputPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		res := knowledge.Generate(cfg, time.Now())
		if err := knowledge.Write(out, res); err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		unreadable := 0
		for _, s := range res.Sources {
			mark := "ok  "
			extra := fmt.Sprintf("%d item(s)", s.Count)
			if !s.OK {
				mark = "FAIL"
				extra = s.Reason
				unreadable++
			}
			fmt.Printf("%s %-18s %s\n", mark, s.Name, extra)
		}
		fmt.Printf("\n%d item(s) written to %s\n", len(res.Items), out)
		fmt.Println("derived, never authoritative -- see the file's own \"note\" field")
		if unreadable > 0 {
			fmt.Printf("%d source(s) could not be read; see FAIL lines above\n", unreadable)
			os.Exit(1)
		}

	case "merge":
		if len(os.Args) < 5 {
			usage()
			os.Exit(2)
		}
		// No <issue> argument: identity comes from the PR's own head ref
		// (must be a dispatch/<id> branch -- read inside gate.Evaluate),
		// never a caller-supplied number. Taking issue from the caller let
		// an author name an unrelated issue with a clean author/reviewer
		// pair and merge anything (agent-estate#926 constraint 6); taking
		// identity from closingIssuesReferences instead of the head ref had
		// its own gap, closed by agent-estate#940.
		repo, reviewer := os.Args[2], os.Args[4]
		var pr int
		if _, err := fmt.Sscanf(os.Args[3], "%d", &pr); err != nil {
			fmt.Fprintln(os.Stderr, "estate: pr must be a number:", os.Args[3])
			os.Exit(2)
		}
		d := gate.Evaluate(repo, pr, reviewer, l)
		fmt.Printf("%s#%d head %s\n", repo, pr, d.HeadOID)
		if !d.Allow {
			for _, r := range d.Reasons {
				fmt.Fprintln(os.Stderr, "refuse: "+r)
			}
			os.Exit(1)
		}
		fmt.Println("may merge: checks green at head, reviewer completed an independent review and approved at the current head")

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

	case "reclaim":
		apply := false
		for _, a := range os.Args[2:] {
			if a == "--apply" {
				apply = true
			}
		}
		inflight, err := l.InFlight()
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		// A boot time that cannot be read is not fatal to the rest of this
		// command -- Assess treats a zero boot time as "skip that check",
		// not as evidence either way -- but it silently narrows what this
		// run can catch, so say so.
		boot, err := reclaim.BootTime()
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate: could not read host boot time, the reboot check is disabled this run:", err)
		}
		assessments := reclaim.Report(inflight, boot, reclaim.PSProbe)
		reclaimable := 0
		for _, a := range assessments {
			mark := "keep"
			if a.Reclaimable {
				mark, reclaimable = "RECLAIM", reclaimable+1
			}
			fmt.Printf("%-28s pid=%-8d %-8s %s\n", a.Record.ID, a.Record.PID, mark, a.Reason)
		}
		fmt.Fprintf(os.Stderr, "%d in flight, %d reclaimable\n", len(assessments), reclaimable)
		if apply {
			n, err := reclaim.Apply(l, assessments)
			if err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			fmt.Printf("reclaimed %d slot(s)\n", n)
		} else if reclaimable > 0 {
			fmt.Println("report only -- re-run with --apply to free these slots")
		}

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
			// agent-estate#931: shape alone accepted two fabricated artifacts
			// in one session, one of them a plausible GitHub comment id that
			// simply did not exist. Resolve each artifact in the window for
			// real instead of trusting it looks openable. See
			// tick.CheckWithResolver's doc comment for why this lives here
			// and not in `tick record`.
			v, err := tick.CheckWithResolver(path, newResolver(defaultHTTPStatus, defaultGHAPI))
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
			if v.Unverifiable > 0 {
				// Distinct third state, said out loud rather than folded into
				// "moving": this run did not confirm every artifact in the
				// window, and that is not the same as confirming them.
				fmt.Printf("note: %d artifact(s) in the window could not be checked (network or gh unreachable) -- "+
					"not treated as fabricated, not treated as confirmed\n", v.Unverifiable)
			}
			fmt.Println("note: artifacts that resolved are confirmed to name something real as of this check, " +
				"not that this loop is what produced them.")

		case "verify":
			// A diagnostic sweep of the whole log, not just the window Check
			// reads -- see agent-estate#931's request to "report how many
			// existing entries resolve, how many do not, and how many could
			// not be checked" honestly, rather than tuning until the number
			// looks good. This command never gates anything; it only reports.
			entries, err := tick.VerifyAll(path, newResolver(defaultHTTPStatus, defaultGHAPI))
			if err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			var valid, invalid, unknown int
			for _, e := range entries {
				switch e.Resolution {
				case tick.ResolveValid:
					valid++
				case tick.ResolveInvalid:
					invalid++
					fmt.Printf("INVALID  %s  %s -- %s\n", e.At, e.Artifact, e.Detail)
				default:
					unknown++
					fmt.Printf("UNKNOWN  %s  %s -- %s\n", e.At, e.Artifact, e.Detail)
				}
			}
			fmt.Printf("%d artifact(s) checked: %d resolve, %d do not, %d could not be checked\n",
				len(entries), valid, invalid, unknown)
			if invalid > 0 {
				os.Exit(1)
			}

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
		// A review turn is dispatched against the same issue as the work it
		// reviews, so the issue alone can never tell the merge gate apart
		// from an authoring turn (agent-estate#926). Role, recorded here at
		// dispatch time, is what removes the ambiguity -- never inferred
		// later from what a lane or a PR comment claims about itself.
		role := ledger.RoleAuthor
		reviewPR := 0
		issueIdx, briefIdx := 2, 3
		if os.Args[2] == "review" {
			if len(os.Args) < 6 {
				fmt.Fprintln(os.Stderr, "estate: dispatch review needs <pr> <issue> <brief-file>")
				os.Exit(2)
			}
			role = ledger.RoleReviewer
			if _, err := fmt.Sscanf(os.Args[3], "%d", &reviewPR); err != nil {
				fmt.Fprintln(os.Stderr, "estate: pr must be a number:", os.Args[3])
				os.Exit(2)
			}
			issueIdx, briefIdx = 4, 5
		}
		issue, briefPath := os.Args[issueIdx], os.Args[briefIdx]
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
		// Second precision let two turns dispatched together share an id --
		// internal/isolate then correctly refused the second one, and a
		// parallel council silently became a smaller one (agent-estate#938).
		// dispatchid mints with nanosecond precision plus the OS pid, which
		// needs no filesystem coordination: see its doc comment for why that
		// is what actually holds under a real race.
		id := dispatchid.New(issue, time.Now())

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

		// The merge gate (agent-estate#940) derives authorship from the PR's
		// own head ref, joined to this exact dispatch id's role=author
		// ledger record -- never from an issue number the brief or the PR
		// body asserts. That join only exists if the PR is opened FROM
		// wt.Branch. A brief that instead tells (or lets) the lane invent
		// its own feature branch produces a PR the gate cannot join to
		// anything, refused with no override. Author turns only: a
		// role=reviewer turn never opens a PR, so it has nothing to state
		// here. Reviewer turns instead get the second-source verdict
		// contract (agent-estate#949), with this dispatch's own id and PR
		// number already filled in so it cannot be forgotten, reworded, or
		// invented by whatever brief the Director happened to write.
		// roleGrounding is the one place both blocks are produced, so a
		// test can exercise the role switch directly without running the
		// rest of dispatch.
		grounded += roleGrounding(role, id, reviewPR, wt.Branch)

		// The Dispatched record is appended once the pid is known (below,
		// right after cmd.Start()), not here -- agent-estate#948 wires up
		// ledger.Record.PID, and a dead turn can only be reclaimed by a pid
		// that was actually recorded. Appending here, before the process
		// exists, would leave the earlier PID-less shape and give
		// internal/reclaim nothing to check.

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "json",
			"--dangerously-skip-permissions")
		cmd.Dir = wt.Path
		cmd.Stdin = strings.NewReader(grounded)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		// exec.Cmd.Output() only captures the child's stderr into the
		// returned *exec.ExitError when cmd.Stderr is nil -- and this path
		// does not use Output() at all (cmd.Start()/cmd.Wait() below, so the
		// pid is known before the ledger records Dispatched). Wiring our own
		// buffer here is what makes that stderr readable at all instead of
		// being lost with no capture path (agent-estate#950). The buffer
		// fills as the child runs, so it holds whatever the child wrote even
		// if the run times out below, not only on a clean exit.
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf

		// Started, not merely queued, before the ledger records it: only a
		// process that actually exists has a pid to record. A dispatch that
		// fails to start leaves nothing running and is recorded Failed
		// directly, never Dispatched with no pid to ever positively resolve.
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "estate: cannot start turn:", err)
			if aerr := l.Append(ledger.Record{ID: id, Issue: issue, Lane: id, Role: role, PR: reviewPR, State: ledger.Failed, Note: "failed to start: " + err.Error()}); aerr != nil {
				fmt.Fprintln(os.Stderr, "estate: cannot record dispatch failure:", aerr)
			}
			os.Exit(1)
		}
		pid := cmd.Process.Pid
		// The pid is recorded the moment it is known -- before the turn does
		// anything else -- because it is the one fact that later lets a dead
		// turn's slot be reclaimed without guessing. See internal/reclaim.
		if err := l.Append(ledger.Record{ID: id, Issue: issue, Lane: id, Role: role, PR: reviewPR, State: ledger.Dispatched, PID: pid, Note: "worktree " + wt.Path}); err != nil {
			fmt.Fprintln(os.Stderr, "estate: cannot record dispatch:", err)
			os.Exit(2)
		}
		fmt.Printf("dispatched %s (role=%s, pid=%d) in %s (grounded in %d operator parameters)\n", id, role, pid, wt.Path, len(params))

		runErr := cmd.Wait()
		out := stdout.Bytes()

		rec := ledger.Record{ID: id, Issue: issue, Lane: id, Role: role, PR: reviewPR, PID: pid}
		switch {
		case ctx.Err() != nil:
			// Timed out. We do not know whether the turn did its work, so we
			// must not say it failed and must not free the slot. We still
			// capture stderr for free either way, and a partial write made
			// before the kill can be the only clue toward re-dispatching
			// versus waiting -- worth keeping even though the state stays
			// Unknown.
			rec.State = ledger.Unknown
			rec.Note = "timed out after 45m; unknown is not failed; " + stderrSegment(stderrBuf.Bytes(), true)
		case runErr != nil:
			rec.State = ledger.Failed
			// stdout is examined here too (agent-estate#955): a bad model or
			// entitlement failure often puts claude -p's own JSON diagnostic
			// on stdout with stderr left empty, and stderrSegment alone would
			// silently drop it. stdoutDiagnosticSegment never dumps raw
			// stdout -- see its own doc comment for why.
			rec.Note = runErr.Error() + "; " + stderrSegment(stderrBuf.Bytes(), true) + "; " + stdoutDiagnosticSegment(out)
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

		// Record what the worktree's branch actually points at now, read
		// directly by the estate via git -- never anything the subprocess
		// above said about itself. This is the fix for agent-estate#940's
		// follow-up review: a branch NAME (dispatch/<id>) is written once,
		// at isolate.Create time, but anyone with push access can later
		// rename a different branch to the same name and push different
		// content under it. internal/gate's structural join now requires
		// the PR's own headRefOid to equal THIS value, so identity is bound
		// to a commit the estate itself observed inside this specific
		// worktree, not to a string. Only role=author turns open PRs, so
		// only they carry this; a lookup failure is recorded as an empty
		// HeadSHA rather than aborting the turn -- the gate treats a
		// missing HeadSHA as "cannot establish provenance" and refuses,
		// which is the correct fail-closed direction here.
		if role == ledger.RoleAuthor {
			if head, herr := wt.Head(); herr == nil {
				rec.HeadSHA = head
			} else {
				rec.Note = strings.TrimSpace(rec.Note + "; could not read worktree HEAD for provenance: " + herr.Error())
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
