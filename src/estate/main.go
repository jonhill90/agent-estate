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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/corpus"
	"github.com/jonhill90/agent-estate/estate/internal/dispatchid"
	"github.com/jonhill90/agent-estate/estate/internal/gate"
	"github.com/jonhill90/agent-estate/estate/internal/harness"
	"github.com/jonhill90/agent-estate/estate/internal/isolate"
	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/mirror"
	"github.com/jonhill90/agent-estate/estate/internal/pressure"
	"github.com/jonhill90/agent-estate/estate/internal/reclaim"
	"github.com/jonhill90/agent-estate/estate/internal/spend"
	"github.com/jonhill90/agent-estate/estate/internal/tick"
	"github.com/jonhill90/agent-estate/estate/internal/toolusage"
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

// prHeadBranch reads a pull request's own CURRENT head branch name straight
// from GitHub -- never a caller's guess, never whatever a brief happens to
// say -- so a fix pass (estate dispatch fix) checks out and continues
// exactly the branch the PR is actually pointing at right now, not a stale
// or invented one. Same "gh pr view", no "-R" (repo inferred from cwd)
// convention this file already uses elsewhere (see the tick-record
// "produced" closure above).
func prHeadBranch(pr int) (string, error) {
	out, err := exec.Command("gh", "pr", "view", strconv.Itoa(pr), "--json", "headRefName", "-q", ".headRefName").Output()
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "", fmt.Errorf("gh reported an empty head branch for PR #%d", pr)
	}
	return branch, nil
}

// knowledgeGrounding is the one paragraph every dispatched turn receives
// telling it the compiled knowledge index (agent-estate#1019 onward) exists
// at all -- appended after whatever role-specific block roleGrounding
// already returns, never in place of it (agent-estate#1049: nine knowledge
// PRs merged and zero mentions of "knowledge" in this file's grounding).
//
// It names `go run ./src/estate knowledge ...`, not a bare `estate` binary,
// because that is what was actually verified from inside a dispatch
// worktree: `estate` is not on PATH there (`which estate` fails, checked
// from this exact worktree layout), but `go run ./src/estate knowledge
// query "test"` succeeds when run from the worktree root, which is where
// h.Start (see dispatch below) starts every turn's process. Anything
// telling a turn to invoke a bare `estate` would be untested and, per the
// PATH check above, false.
//
// Deliberately NOT a pre-fetch: no query result is ever injected here, only
// the fact that querying is possible (agent-estate#1049's "no automatic
// pre-fetch" -- injecting results into every prompt would recreate the
// fixed-payload problem this issue exists to fix, one layer up).
//
// Also names `source:<name>` scoping (agent-estate#1081): #1087's own
// knowledge doc, while it retrieves, only surfaces the flag to a lane whose
// query already contains the word "source"; a lane asking how to get fewer
// irrelevant results does not find it by searching. This one sentence is
// the only path that reaches the lane that has not yet learned the
// vocabulary; the doc stays the explainer, pointed at rather than
// duplicated here.
//
// The scoped-vs-unscoped benefit named below used to be stated as a
// stratum hit-rate pair (agent-estate#1168 fix pass: "4/12 vs 1/12 at
// top-3, 8/12 vs 5/12 at top-10"). agent-estate#1166 found those four
// numbers stale -- agent-estate#1172 grew the natural-language stratum
// 12 -> 21 cases after they were written, and two of the four no longer
// matched `goldenquery`'s own output at all. agent-estate#1173 had already
// settled the identical question for docs/knowledge-system.md's own
// scoping section: it removed that doc's stratum table rather than
// re-measuring and re-stamping it, arguing that re-stamping fixes today's
// staleness and does nothing about tomorrow's -- nothing tells a doc (or
// this paragraph) when the fixture it quotes has moved.
//
// That argument transfers here, and applies MORE strongly, not equally:
// docs/knowledge-system.md at least carried a "Measured on <sha>" stamp, so
// a reader could date it; this paragraph carried no stamping convention at
// all, so the same staleness was both unbounded and silent, and it reaches
// every dispatched turn rather than every reader who happens to follow a
// link. One difference cuts the other way -- the doc could tell a human
// reader to go run `go run ./src/estate/cmd/goldenquery` and let them judge
// whether that's worth their time; telling a dispatched lane, mid-task, to
// go run a benchmark is a cost this paragraph should not impose on every
// turn. So the fix below is not "point at the same command instead" -- it
// drops the stratum numbers entirely and keeps only the two claims that do
// not move when the fixture grows: which sources are reachable in each mode
// (structural: public mode has exactly two publishable sources and
// `github-stars` has never outranked `repo-docs` on a repo-oriented
// question, so `source:repo-docs` scoping there removes nothing that would
// have won anyway) and which direction a wrong guess costs you (also
// structural, not fixture-dependent). A reader who wants today's number
// runs `go run ./src/estate/cmd/goldenquery` or reads
// `docs/knowledge-system.md`'s own scoping section; neither number is
// restated here to go stale a second time. (Independently re-verified on
// this same head while writing this comment: public unscoped and public
// scoped both 13/21 top-3, 17/21 top-10; `--private` scoped 13/21 top-3,
// 17/21 top-10; `--private` unscoped 10/21 top-3, 14/21 top-10 -- the
// qualitative claim still holds on the current fixture, which is exactly
// the number this comment declines to put in the paragraph below.)
func knowledgeGrounding() string {
	return "\n\n## Knowledge retrieval exists (agent-estate#1049)\n" +
		"A fresh dispatch worktree starts with no index at all -- `knowledge query` " +
		"will exit non-zero until one exists. Before asking a question, run " +
		"`go run ./src/estate knowledge` once with no subcommand, FROM THIS " +
		"WORKTREE'S ROOT: run from there, it builds your turn's own index at its " +
		"own path (agent-estate#1048), never the shared index every other lane " +
		"reads -- run it from anywhere else (your home directory, another repo, " +
		"wherever this session's cwd has drifted to) and it builds the SHARED " +
		"index instead, which must never happen. Running the same command again " +
		"later refreshes it -- one verb for both a turn that never had an index " +
		"and one whose index has gone stale -- but only if you are still at this " +
		"worktree's root when you run it. `estate` itself is not on PATH here.\n" +
		"Then ask: `go run ./src/estate knowledge query [--private] \"<question>\"` " +
		"returns small, ranked, cited pointers, and `go run ./src/estate knowledge " +
		"get [--private] <id>` returns one pointer's full body. The index is " +
		"derived, never authoritative, and may be reported stale. A query can come " +
		"back five ways: `no_match` (no item fits) and `withheld_private` (matches " +
		"exist but are private, `--private` lifts that filter) mean change your " +
		"question -- but `index_missing`, `index_unreadable`, and an index that " +
		"reads back with 0 items all mean the index itself needs (re)building: run " +
		"`estate knowledge` again, the same command as above; rephrasing will not " +
		"help. Do not paste private material into anything public. Add `--json` to " +
		"either command for the full machine-readable result, Coverage " +
		"(agent-estate#1065's trustworthiness signal, itself reported not_applicable " +
		"when the index couldn't be read) included, instead of parsing the prose. " +
		"This is a tool, not a required step: use it if it helps, and the turn " +
		"still works when the index is missing. If results look noisy or " +
		"off-topic, prefix the query with `source:<name>` -- e.g. `source:repo-docs` " +
		"for how this repo works, `source:corpus-directive` for what the operator " +
		"has decided -- to narrow to one source. Under `--private`, do so whenever " +
		"you already know which source holds the answer: `--private` mode has " +
		"several competing sources, and scoping to the right one measurably " +
		"improves the hit rate there (agent-estate#1162; run " +
		"`go run ./src/estate/cmd/goldenquery`, or see `docs/knowledge-system.md`'s " +
		"own scoping section, for the current number -- neither is restated here " +
		"on purpose, agent-estate#1166: the checked-in fixture this would-be " +
		"number is measured against grows over time, and a figure quoted in this " +
		"paragraph would go stale silently, on every dispatched turn, with no " +
		"stamp to date it by). In public mode this comparison does not exist: " +
		"only two of the index's sources are publishable (`repo-docs`, " +
		"`github-stars`), and `github-stars` has never outranked `repo-docs` on a " +
		"repo-oriented question, so `source:repo-docs` scoping there can only " +
		"ever remove a source that was not going to outrank the answer anyway -- " +
		"structurally inert, not measurably inert, so this claim does not drift " +
		"with the fixture the way a number would. A wrong " +
		"guess is a different cost by mode. In public mode it is cheap: a wrong scope surfaces as `no_match` or " +
		"`withheld_private`, visibly, and is fixed by re-running the same question " +
		"unscoped. Under `--private` it is not cheap: a wrong scope can instead " +
		"return ranked, cited, scored results from the WRONG source that look " +
		"exactly like a right answer, with nothing in the score to tell them apart " +
		"(agent-estate#1099, agent-estate#1166) -- so only scope a `--private` " +
		"query against a source you already know is correct (agent-estate#1081; " +
		"see `docs/knowledge-system.md` for the full scoping rules).\n"
}

// roleGrounding is what dispatch appends to the prompt based on role alone
// -- the author's branch-discipline block (agent-estate#940, text
// unchanged by #949), the fix-pass's branch-CONTINUATION block
// (fixPassGrounding, agent-estate#940's "does not survive a fix pass"
// follow-up), or the reviewer's verdict contract (reviewerContract, #949) --
// plus, since agent-estate#1049, knowledgeGrounding appended to every one of
// them: the one block that does not vary by role.
func roleGrounding(role ledger.Role, id string, reviewPR int, branch string, fixPass bool) string {
	switch role {
	case ledger.RoleAuthor:
		if fixPass {
			return fixPassGrounding(reviewPR, branch) + knowledgeGrounding()
		}
		return "\n\n## Branch discipline (agent-estate#940 -- read this before opening a PR)\n" +
			"Your worktree's branch is already `" + branch + "` -- created by the estate " +
			"itself, not by you. Commit your work on THIS branch and push it AS-IS:\n\n" +
			"    git push -u origin " + branch + "\n\n" +
			"Open your pull request FROM `" + branch + "`. Do not `git checkout -b` a " +
			"hand-named branch (e.g. `feat/...`, `fix/...`) and open the PR from that instead --" +
			" the merge gate (`estate merge`) derives authorship by reading the PR's own head " +
			"ref back and joining it to this exact dispatch's ledger record. A PR opened from " +
			"any other branch carries no evidence the estate produced, and the gate refuses it " +
			"structurally, with no override.\n" +
			knowledgeGrounding()
	case ledger.RoleReviewer:
		return reviewerContract(id, reviewPR) + knowledgeGrounding()
	default:
		return ""
	}
}

// fixPassGrounding is a role=author turn's grounding when it is dispatched
// to CONTINUE an existing pull request (`estate dispatch fix`) rather than
// open a new one. The turn's own worktree (internal/isolate.CreateOnBranch)
// is already checked out, detached, at that PR's own current head, fetched
// fresh from origin by the estate -- not asserted by the agent and not
// assumed from a possibly-stale local ref. The turn must push its commits
// BACK onto that same branch, never invent a new one: the merge gate
// (internal/gate) accepts a fix pass by chaining this dispatch's own
// recorded Base/HeadSHA back to the PR's ORIGINAL dispatch, one completed,
// PR-scoped ledger record at a time (see gate's package doc, "does not
// survive a fix pass"). A push to any other branch, or under any other PR
// number, breaks that chain and the gate refuses it structurally.
func fixPassGrounding(pr int, branch string) string {
	return "\n\n## Fix-pass discipline (agent-estate#940 -- read this before pushing)\n" +
		"You are a FIX PASS on an existing pull request, PR #" + strconv.Itoa(pr) + ". Your " +
		"worktree is already checked out -- in a DETACHED state, fetched fresh from origin by " +
		"the estate itself, not by you -- at that PR's own current head, on branch `" + branch +
		"`. Do NOT create a new branch and do NOT open a new pull request. Commit your fixes on " +
		"top of what is already checked out, then push them back to the SAME existing branch:\n\n" +
		"    git push origin HEAD:" + branch + "\n\n" +
		"The merge gate (`estate merge`) joins this turn back to PR #" + strconv.Itoa(pr) +
		" by reading the commit your OWN worktree ends up at once this turn exits -- never " +
		"anything you say about it -- and chaining that commit back to the PR's original " +
		"dispatch. Pushing anywhere else, or under a different PR number, carries no evidence " +
		"the estate produced, and the gate refuses it structurally, with no override.\n"
}

func usage() {
	fmt.Fprint(os.Stderr, `estate -- the supervisor

  estate pressure                       report whether the host can take work
  estate dispatch [--harness=NAME] <issue> <brief-file>
                                        run one agent turn (role=author), gated and recorded.
                                        harness defaults to $ESTATE_HARNESS or "claude"; see
                                        internal/harness for what's registered
  estate dispatch [--harness=NAME] review <pr> <issue> <brief-file>
                                        run one review turn (role=reviewer) against a PR
  estate dispatch fix <pr> <issue> <brief-file>
                                        run one fix-pass turn (role=author, PR-scoped)
                                        continuing an EXISTING PR's own branch, fetched
                                        fresh from origin -- not a fresh dispatch branch
  estate merge <repo> <pr> <reviewer-lane>
                                        may this PR merge? identity comes from the PR's
                                        own head ref (must be a dispatch/<id> branch),
                                        joined either directly or through a chain of
                                        completed fix-pass turns for that PR -- never a
                                        caller argument and never a closing issue --
                                        checks green, author != reviewer, reviewer
                                        actually completed a review, and posted APPROVE
  estate corpus-audit [n]               hard parameters least supported by your words
  estate knowledge                      regenerate the compiled, read-only index over
                                         GitHub stars, the memory vault, the corpus and
                                         Loops-Research -- derived, never authoritative;
                                         refuses (exit 1) to overwrite an index already
                                         on disk if this run would flip any source from
                                         ok=true to ok=false relative to it
                                         (agent-estate#1123) -- pass --allow-coverage-loss
                                         to write the degraded index anyway
  estate knowledge query [--private] [--json] <question>
                                         small, ranked, cited pointers into the compiled
                                         index -- never bodies; publishable-only by
                                         default (agent-estate#1033), --private lifts
                                         that filter and says so in the output; states
                                         matched, no_match, index_missing,
                                         index_unreadable or withheld_private, never one
                                         collapsed "nothing" answer; --json emits the
                                         full QueryResult, Coverage included, as JSON on
                                         stdout instead of prose (agent-estate#1068); an
                                         unrecognised flag is refused, never folded into
                                         the question
  estate knowledge get [--private] [--json] <id>
                                         the one item Tier1/Tier2/Tier3 body a query
                                         match pointed at -- the second half of
                                         progressive disclosure; refuses a private id
                                         without --private (agent-estate#1033); --json
                                         emits {ok, reason, item} on stdout instead of
                                         prose (agent-estate#1068)
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
                                        trusting its shape.
                                        exit 0 = moving
                                        exit 1 = STALLED, unacknowledged --
                                          stop and run 'tick escalate'
                                        exit 3 = STALLED, escalated -- may
                                          continue on OTHER work; this
                                          phase item/src head is still stuck
  estate tick escalate <phase-item> <where>
                                        record that a human was told about
                                        the current stall. Never counts as
                                        an artifact and never clears the
                                        stall by itself -- it only changes
                                        the next 'tick check' exit code from
                                        1 to 3 once the stall it names matches
  estate tick verify                    sweep the whole log and report how
                                        many artifacts resolve, how many
                                        don't, and how many could not be
                                        checked -- diagnostic, does not gate
  estate verify-branch <branch>         build and test a branch in its OWN tree
  estate spend                          per-harness turn spend from the
                                        ledger's own recorded fields --
                                        claude reports dollars, codex only
                                        ever reports tokens, and this never
                                        sums a dollar figure across harnesses
                                        that don't all report one
  estate toolusage <turn-id>             which tools a dispatched turn actually
                                        invoked, structurally walked from its
                                        harness transcript (joined via the
                                        ledger's own recorded session id,
                                        agent-estate#990) -- names and counts
                                        only, never transcript content
  estate toolusage --recent [n]          same, aggregated across the n most
                                        recently completed turns that carry a
                                        recorded session id (default 20)

Every gate fails closed: a limit that cannot be measured refuses.

Watching a dispatched turn. Each turn is mirrored into a tmux window running
'tail -f' on its own transcript (~/.local/state/estate/mirror/<id>.log). That
pane is a VIEWER: nothing typed there reaches the agent, and killing it does
not touch the turn. Windows are bounded by the same in-flight cap that bounds
concurrent turns; a turn that cannot get one runs unmirrored rather than
waiting. Environment:

  ESTATE_MIRROR=0                       switch mirroring off entirely
  ESTATE_MIRROR_SESSION=<name>          tmux session to open windows in
                                        (default "estate")
  ESTATE_MIRROR_DIR=<path>              where transcripts are written
  ESTATE_MIRROR_TMUX_TMPDIR=<path>      scope every tmux call to a PRIVATE
                                        socket directory, for a demo or an
                                        end-to-end check that must not touch
                                        the operator's own tmux server. The
                                        directory must already exist: tmux
                                        silently falls back to the default
                                        socket otherwise, so a missing one is
                                        refused rather than obeyed
`)
}

// printToolUsage renders a toolusage.Counts tally -- tool names and integers
// only, never transcript content (agent-estate#1096's hard rule: a turn's
// transcript may carry operator material, so nothing beyond a count is ever
// printed here).
func printToolUsage(c toolusage.Counts) {
	names := make([]string, 0, len(c.Tools))
	for n := range c.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Printf("%-24s %d\n", n, c.Tools[n])
	}
	if len(names) == 0 {
		fmt.Println("no tool_use blocks found")
	}
	fmt.Printf("\nknowledge query: %d (source: scoped: %d)\n", c.KnowledgeQuery, c.KnowledgeQueryScoped)
	if c.Malformed > 0 {
		fmt.Printf("%d of %d transcript line(s) could not be parsed\n", c.Malformed, c.Lines)
	}
}

// printKnowledgeQuery renders a knowledge.QueryResult for a terminal --
// the six distinguishable states agent-estate#1019/#1033/#1052 require
// (matched, matched_withheld_majority, no_match, index_missing,
// index_unreadable, withheld_private) each print visibly differently,
// never collapsing to the same "nothing here" shape. When PrivateIncluded
// is set, that is stated in the output itself (agent-estate#1028's point
// 3) -- not only in a doc comment or a flag the reader of the printed
// text cannot see.
func printKnowledgeQuery(qr knowledge.QueryResult) {
	switch qr.State {
	case knowledge.StateIndexMissing:
		fmt.Fprintln(os.Stderr, "estate: "+qr.Reason)
		return
	case knowledge.StateIndexUnreadable:
		fmt.Fprintln(os.Stderr, "estate: index unreadable: "+qr.Reason)
		return
	}

	// agent-estate#1036: every state that got this far read a real,
	// successfully-parsed index (IndexGeneratedAt is set for all three
	// remaining states -- see Query in query.go), so the index's own age
	// and whether it has fallen behind its sources is printed once, here,
	// before any of the three shapes below. #1045's reviewer hit this
	// blind exactly once for real: a stale index answered silently, and
	// only manual regeneration caught it.
	printIndexFreshness(qr.IndexGeneratedAt)
	printBuildCommitMismatch(qr.IndexGeneratedBy, currentBuildCommit())

	switch qr.State {
	case knowledge.StateNoMatch:
		fmt.Printf("no item matches %q\n", qr.Question)
		if qr.Reason != "" {
			fmt.Println(qr.Reason)
		}
		// agent-estate#1124: printed unconditionally, not only when
		// suspiciously low -- a real answer of "0 items" against a broken
		// index and "3959 items, none of them relevant" against a healthy
		// one must never render as the same line. See IndexItemCount's own
		// doc comment.
		fmt.Printf("index contains %d item(s)\n", qr.IndexItemCount)
		printSourceStatuses(qr.SourceStatuses)
		return
	case knowledge.StateWithheldPrivate:
		fmt.Printf("no PUBLISHABLE item matches %q\n", qr.Question)
		fmt.Println(qr.Reason)
		printSourceStatuses(qr.SourceStatuses)
		return
	}

	if qr.PrivateIncluded {
		fmt.Println("*** PRIVATE MODE -- private items may be included below ***")
	}
	if qr.State == knowledge.StateMatchedWithheldMajority {
		// agent-estate#1052: exit 0 and "matched" alone read as plain
		// success -- this banner is the loud, hard-to-miss signal that a
		// caller reading the state word (not just $?) needs, since the
		// exit code deliberately does not change here (see
		// knowledgeQueryExitCode's own comment for why).
		fmt.Println("*** MOSTLY WITHHELD -- most matching items are private; the results below are a minority of the answer ***")
	}
	printContradictions(qr.Contradictions)
	printSourceStatuses(qr.SourceStatuses)
	fmt.Printf("%d match(es) for %q (showing %d, %d not returned, %d withheld as private)\n\n",
		qr.TotalMatched, qr.Question, len(qr.Matches), qr.NotReturned, qr.WithheldPrivate)
	for _, m := range qr.Matches {
		// The header line's shape ("[id] source (score N: terms)") is a
		// parsed format -- cmd/goldenquery's matchHeader regex reads it
		// back verbatim -- so the [PRIVATE] marker, and now the
		// weight/status marker (agent-estate#1128), go on the Tier1 line
		// instead of appended here, where either would break that regex's
		// end-of-line anchor for every match it applies to.
		tier1 := m.Tier1
		if !m.Publishable {
			tier1 = "[PRIVATE] " + tier1
		}
		// weight/status print unconditionally whenever the source item
		// carried them (every corpus-* source; every other source leaves
		// both "" and prints nothing here) -- never only when non-default.
		// A reader who sees no marker on a non-corpus item already knows
		// why (that source has no weight/status at all); a reader who saw
		// a marker only for "unusual" values would have no way to tell
		// "this item is weight=hard/status=acted" apart from "this source
		// doesn't carry the concept", which is exactly the ambiguity
		// #1128 exists to remove.
		if m.Weight != "" || m.Status != "" {
			tags := []string{}
			if m.Weight != "" {
				tags = append(tags, "weight="+m.Weight)
			}
			if m.Status != "" {
				tags = append(tags, "status="+m.Status)
			}
			tier1 = "[" + strings.Join(tags, " ") + "] " + tier1
		}
		tieNote := ""
		if m.TiedOnScore > 0 {
			// agent-estate#1046: this item's UNROUNDED score exactly ties
			// m.TiedOnScore other candidates, so its position relative to
			// them was decided by the item-id fallback, not by relevance --
			// appended to the header line, after the printed (rounded)
			// score, never in place of it.
			tieNote = fmt.Sprintf(", tied with %d other(s) on unrounded score", m.TiedOnScore)
		}
		fmt.Printf("[%s] %s (score %d: %s%s)\n  %s\n  %s\n\n",
			m.ID, m.Source, m.Score, strings.Join(m.MatchedTerms, ", "), tieNote, tier1, m.Permalink)
	}
	fmt.Println("ranking: " + qr.RankingBasis)
	fmt.Println("ask `estate knowledge get <id>` for one item's full tier2/tier3")
	printRepoDocsRootIfAny(matchSources(qr.Matches))
}

// matchSources collects the distinct Source values across a set of
// query matches -- printRepoDocsRootIfAny's own input, so it only prints
// the note when a repo-docs match is actually present.
func matchSources(matches []knowledge.Match) []string {
	sources := make([]string, len(matches))
	for i, m := range matches {
		sources[i] = m.Source
	}
	return sources
}

// printRepoDocsRootIfAny prints this checkout's own repo root once, iff
// "repo-docs" appears among sources -- agent-estate#1072: repo-docs
// permalinks are repo-relative (AGENTS.md#anchor, not an absolute path)
// so the same section gets the same id from any checkout, but a reader
// still needs to know what to join a relative permalink to before it can
// actually open the file. Resolved fresh via knowledge.DefaultConfig(),
// the same repo-root logic every other knowledge command already uses --
// this is the *current* checkout's root, which is what a reader sitting
// at this terminal actually wants, not whatever checkout originally
// generated the index.
func printRepoDocsRootIfAny(sources []string) {
	hasRepoDocs := false
	for _, s := range sources {
		if s == "repo-docs" {
			hasRepoDocs = true
			break
		}
	}
	if !hasRepoDocs {
		return
	}
	cfg, err := knowledge.DefaultConfig()
	if err != nil || cfg.RepoRoot == "" {
		fmt.Println("repo-docs permalinks are relative to this checkout's repo root, which could not be resolved here -- open them from a checkout containing AGENTS.md")
		return
	}
	fmt.Println("repo-docs permalinks are relative to this checkout's repo root: " + cfg.RepoRoot)
}

// knowledgeQueryExitCode maps a QueryResult's typed State to the exit code
// `estate knowledge query` reports -- one code per state that a script or
// agent branching on $? needs to tell apart, never two states sharing a
// code by omission.
//
//	0  StateMatched,
//	   StateMatchedWithheldMajority -- at least one publishable item answers
//	                                   this and was returned
//	1  StateNoMatch          -- the index was read fine; nothing answers this
//	2  StateIndexMissing,
//	   StateIndexUnreadable  -- the index itself could not be read at all
//	3  StateWithheldPrivate  -- something answers this, but it is private and
//	                            this call did not ask for private material
//
// 3 was picked, not 1, because collapsing withheld_private into no_match is
// the exact error this function exists to prevent -- see agent-estate#1037's
// review comment on PR #1037: "there is nothing" (no_match) and "there is
// something you may not see" (withheld_private) are different answers, and a
// caller branching only on $? could not tell them apart under the prior
// two-code mapping. 3 is unused by every other `estate` exit path this
// command can reach (0/1/2 are the pre-existing, load-bearing codes named in
// #1033 and are never renumbered here), so it cannot silently collide with
// an existing caller's expectations.
//
// StateMatchedWithheldMajority (agent-estate#1052) deliberately shares
// StateMatched's exit code rather than getting a new one. It was measured
// against a real index and every golden-set query that returns it still
// carries a real, citable public answer (see StateMatchedWithheldMajority's
// own doc comment for the numbers) -- treating it as a non-zero exit would
// have turned those into runner failures, moving the golden score, which
// #1052 explicitly forbids. A caller that inspects the state word itself,
// not just $?, is the one this state is for.
func knowledgeQueryExitCode(state knowledge.QueryState) int {
	switch state {
	case knowledge.StateIndexMissing, knowledge.StateIndexUnreadable:
		return 2
	case knowledge.StateWithheldPrivate:
		return 3
	case knowledge.StateNoMatch:
		return 1
	default: // StateMatched, StateMatchedWithheldMajority
		return 0
	}
}

// parseKnowledgeArgs splits a `knowledge query`/`knowledge get` argument
// list into --private (agent-estate#1033's explicit, opt-in private mode),
// --json (agent-estate#1068's structured output mode), and the remaining
// positional arguments. Neither flag is ever inferred from context.
//
// unknown carries the first argument that looks like a flag (starts with
// "--") but is neither of the two recognised ones -- agent-estate#1068
// Finding 2: before this, any unrecognised flag fell into rest and was
// silently joined into the question text itself (`--json` became a search
// term, scoring items that merely mention "json"), giving a confident
// wrong answer to a different question instead of an error. The caller
// must check unknown and refuse before running any query -- see
// `case "knowledge":` in main.
func parseKnowledgeArgs(args []string) (includePrivate, asJSON bool, rest []string, unknown string) {
	for _, a := range args {
		switch {
		case a == "--private":
			includePrivate = true
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "--"):
			if unknown == "" {
				unknown = a
			}
		default:
			rest = append(rest, a)
		}
	}
	return includePrivate, asJSON, rest, unknown
}

// printKnowledgeQueryJSON renders a knowledge.QueryResult as JSON on
// stdout -- agent-estate#1068 Finding 1: QueryResult (including Coverage,
// the machine-readable trustworthiness signal #1065 landed) is already
// fully JSON-tagged, so this is transport only, never a second
// computation of what Query decided. Every state (matched, no_match,
// index_missing, index_unreadable, withheld_private,
// matched_withheld_majority) is emitted the same way, unlike prose mode's
// early stderr returns for the two index-read failures -- a JSON caller
// reads State, not which stream the process wrote to.
func printKnowledgeQueryJSON(qr knowledge.QueryResult) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(qr); err != nil {
		fmt.Fprintln(os.Stderr, "estate: encode json:", err)
		os.Exit(2)
	}
}

// knowledgeGetJSON is `knowledge get --json`'s stdout shape -- Get itself
// returns (Item, bool, string), so this wraps that same triple as JSON
// rather than inventing a new field set. Item is nil (omitted) whenever
// ok is false, so a JSON caller never has to distinguish a genuine
// zero-value Item from "there is no item" -- Reason already carries why.
//
// Disclosure and DisclosureError are agent-estate#1061's own addition,
// sitting BESIDE Item rather than inside it -- the same placement #1104
// chose for Contradictions beside Coverage on QueryResult, for the same
// reason: this is a signal ABOUT the item's own prompt_id, computed by a
// live corpus read at `get` time, not a field the compiled index itself
// carries. Both are omitted together (an unresolvable prompt_id, e.g.
// item.PromptID == "") -- a JSON caller sees neither key rather than a
// null Disclosure, matching Item's own omit-on-absence convention above.
type knowledgeGetJSON struct {
	OK              bool                  `json:"ok"`
	Reason          string                `json:"reason,omitempty"`
	Item            *knowledge.Item       `json:"item,omitempty"`
	Disclosure      *knowledge.Disclosure `json:"disclosure,omitempty"`
	DisclosureError string                `json:"disclosure_error,omitempty"`
}

// printKnowledgeGetJSON renders one knowledge.Get result as JSON on
// stdout -- the structured counterpart to the prose block later in
// `case "knowledge":`, applied to `get` as the one-line extension #1068
// asked for explicitly. disc/discErr are the caller's own already-
// resolved agent-estate#1061 disclosure result (or check failure) --
// this function only transports them, it never calls ResolveDisclosure
// itself.
func printKnowledgeGetJSON(item knowledge.Item, ok bool, reason string, disc *knowledge.Disclosure, discErr string) {
	out := knowledgeGetJSON{OK: ok, Reason: reason, Disclosure: disc, DisclosureError: discErr}
	if ok {
		out.Item = &item
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "estate: encode json:", err)
		os.Exit(2)
	}
}

// printContradictions renders knowledge.Query's own agent-estate#1051
// finding: a corpus-question and a vault-fact/corpus-directive that both
// landed in this result set on the same matched terms. Printed above the
// match list (never inside it, never reordering anything) because it is
// a property of the SET, not of either individual match -- see
// Contradiction's own doc comment in contradiction.go for why this is
// its own signal rather than a Coverage reason. Never says which side is
// right; names only the two ids and the shared terms, and tells the
// reader to look.
func printContradictions(cs []knowledge.Contradiction) {
	for _, c := range cs {
		fmt.Printf("note: [%s] (%s) and [%s] (%s) disagree on %s -- a question on this subject appears unresolved; read both before acting\n",
			c.QuestionID, c.QuestionSource, c.AssertionID, c.AssertionSource, strings.Join(c.SharedTerms, ", "))
	}
	if len(cs) > 0 {
		fmt.Println()
	}
}

// printSourceStatuses renders any source that failed to read when the index
// was BUILT (as opposed to no item matching, or the index itself being
// unreadable NOW -- see printKnowledgeQuery's own doc comment). Printed
// ABOVE the match list in the matched path (never after it, alongside
// printContradictions) because agent-estate#1116: a degraded source is a
// property of the SET the same way a contradiction is -- it says part of
// the answer was never in the running -- and burying it below ten
// confident-looking matches let a reader act on an answer that was
// silently incomplete. In the two early-return states (StateNoMatch,
// StateWithheldPrivate) it already ran first, since there is no match list
// to bury it under in the first place; this only changes the matched path
// to the same position.
//
// A "note:" line, not a "*** ***" banner: whether the degraded source is
// one the query would actually have drawn from isn't something a
// QueryResult can currently answer -- SourceStatuses names which of the
// four build-time readers failed, but nothing here ties a failed source to
// which matched terms it would have scored, so a caller can't tell "this
// gap is unrelated to your question" from "this gap is exactly your
// question" from this record alone. Coverage.State already carries the
// louder machine-readable signal (CoverageDegraded/CoverageMixed) for a
// caller that wants to branch on it; this is the prose surface, kept at
// the same visibility as the contradiction notes it sits beside rather
// than invented as a signal this package cannot actually back.
func printSourceStatuses(sources []knowledge.SourceResult) {
	for _, s := range sources {
		if !s.OK {
			fmt.Printf("note: source %s was unavailable when the index was built: %s\n", s.Name, s.Reason)
		}
	}
}

// indexSourceMtime is one observed reading against one of the four
// sources knowledge.Generate compiles from (see knowledge.go's package
// comment) -- either a real mtime, or a reason it could not be read.
// Unknown is its own field, deliberately not a zero time.Time, so
// "could not check" is never rendered as if it were "checked and fresh"
// (agent-estate#1036: absence of evidence is not evidence of freshness).
type indexSourceMtime struct {
	name   string
	mtime  time.Time
	known  bool
	reason string
}

// indexSourceMtimes reads this stat is a read, nothing more, matching
// this task's "sources read-only" constraint -- three of the four
// sources knowledge.Generate reads (see knowledge.go's package comment)
// have a real path on disk to stat. The fourth, GitHub stars, is read
// live via `gh api user/starred` with no local cache file this CLI can
// stat (agent-estate#942's "measured, not assumed" discipline applies
// here too: no such file exists to check, so it is reported unknown
// rather than guessed at).
func indexSourceMtimes(cfg knowledge.Config) []indexSourceMtime {
	statNewest := func(name, dir string) indexSourceMtime {
		if dir == "" {
			return indexSourceMtime{name: name, reason: "path not configured"}
		}
		fi, err := os.Stat(dir)
		if err != nil {
			return indexSourceMtime{name: name, reason: err.Error()}
		}
		newest := fi.ModTime()
		// A directory's own mtime only moves when an entry is added or
		// removed, not when an existing file's content changes -- so
		// editing an existing vault fact or research note in place
		// would look fresh under the directory mtime alone. Reading
		// each entry's own mtime too (one extra stat each, still
		// read-only) catches that case.
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				if info.ModTime().After(newest) {
					newest = info.ModTime()
				}
			}
		}
		return indexSourceMtime{name: name, mtime: newest, known: true}
	}

	return []indexSourceMtime{
		statNewest("agent-memory-vault", filepath.Join(cfg.VaultDir, "agent", "facts")),
		statFile("corpus-db", cfg.CorpusDBPath),
		statNewest("loops-research", cfg.LoopsResearch),
		{
			name:   "github-stars",
			reason: "read live via `gh api user/starred`, no local cache file to stat",
		},
	}
}

// statFile stats a single file (the corpus database, unlike the vault
// and research directories, is one file with no entries to widen the
// reading over) and reports its mtime, or the reason it could not be
// read -- never a zero time standing in silently for "unknown".
func statFile(name, path string) indexSourceMtime {
	if path == "" {
		return indexSourceMtime{name: name, reason: "path not configured"}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return indexSourceMtime{name: name, reason: err.Error()}
	}
	return indexSourceMtime{name: name, mtime: fi.ModTime(), known: true}
}

// freshnessFindings compares generatedAt against every source
// knowledge.Generate reads (indexSourceMtimes) and splits the result into
// sources demonstrably newer than the index (stale) and sources whose
// freshness could not be determined at all (unknown, e.g. github-stars --
// no local file to stat). This is the single read-only measurement both
// printIndexFreshness's prose (agent-estate#1036) and
// foldFreshnessIntoCoverage's structure (agent-estate#1080) are derived
// from, so the two can never disagree about what was actually observed --
// one measurement, two renderings, never a second copy of the comparison.
// cfgErr non-nil means the comparison could not run at all (source paths
// themselves could not be resolved); both callers treat that as its own
// finding, not a silent skip.
func freshnessFindings(generatedAt time.Time) (stale, unknown []indexSourceMtime, cfgErr error) {
	cfg, err := knowledge.DefaultConfig()
	if err != nil {
		return nil, nil, err
	}
	for _, s := range indexSourceMtimes(cfg) {
		if !s.known {
			unknown = append(unknown, s)
			continue
		}
		if s.mtime.After(generatedAt) {
			stale = append(stale, s)
		}
	}
	return stale, unknown, nil
}

// printIndexFreshness prints the compiled index's own age, then names any
// source freshnessFindings reports as changed since or uncheckable --
// agent-estate#1036. It never regenerates anything; freshnessFindings'
// own stat calls are the only filesystem access this performs.
// generatedAt.IsZero() means the caller has no successfully-read index to
// report on (StateIndexMissing/StateIndexUnreadable already returned
// before this is called), so it is a silent no-op.
func printIndexFreshness(generatedAt time.Time) {
	if generatedAt.IsZero() {
		return
	}
	fmt.Printf("index built %s ago (%s)\n", formatAge(time.Since(generatedAt)), generatedAt.Format(time.RFC3339))

	stale, unknown, err := freshnessFindings(generatedAt)
	if err != nil {
		fmt.Printf("note: could not resolve source paths to check staleness against: %s\n", err)
		return
	}

	if len(stale) > 0 {
		names := make([]string, len(stale))
		for i, s := range stale {
			names[i] = fmt.Sprintf("%s (changed %s ago)", s.name, formatAge(time.Since(s.mtime)))
		}
		fmt.Printf("index is BEHIND its sources: %s -- regenerate with `estate knowledge` before trusting this over a live read\n",
			strings.Join(names, ", "))
	}
	for _, u := range unknown {
		fmt.Printf("note: staleness against %s (%s) could not be checked -- reported as unknown, not assumed fresh\n", u.name, u.reason)
	}
}

// foldFreshnessIntoCoverage folds freshnessFindings' result into qr's own
// Coverage as structured CoverageStale/CoverageUnknownFreshness reasons --
// agent-estate#1080's fold-in of #1047's staleness comparison, so a
// machine caller reading only Coverage (never the prose printed alongside
// it) still learns what printIndexFreshness already told a human: a
// confirmed-newer source names itself as CoverageStale, and a source whose
// freshness could not be determined at all -- github-stars, standingly --
// reports CoverageUnknownFreshness rather than silently reading as fresh.
// generatedAt.IsZero() mirrors printIndexFreshness's own no-op guard: no
// successfully-read index means nothing to compare, and Coverage is
// already the zero value for those two states (see QueryResult's own doc
// comment). A cfg resolution failure is itself folded in as
// CoverageUnknownFreshness with no named source -- every source's
// freshness is equally uncheckable when the comparison can't run at all,
// which is at least as severe as any single source being uncheckable.
func foldFreshnessIntoCoverage(cov knowledge.Coverage, generatedAt time.Time) knowledge.Coverage {
	if generatedAt.IsZero() {
		return cov
	}
	stale, unknown, err := freshnessFindings(generatedAt)
	if err != nil {
		return cov.WithFreshnessReason(knowledge.CoverageUnknownFreshness, "",
			"could not resolve source paths to check staleness against: "+err.Error())
	}
	for _, s := range stale {
		cov = cov.WithFreshnessReason(knowledge.CoverageStale, s.name,
			fmt.Sprintf("changed %s ago, after the index was built", formatAge(time.Since(s.mtime))))
	}
	for _, u := range unknown {
		cov = cov.WithFreshnessReason(knowledge.CoverageUnknownFreshness, u.name,
			"staleness could not be checked: "+u.reason)
	}
	return cov
}

// shortCommit renders a commit for terminal output -- the first 12 hex
// characters, the same truncation `git rev-parse --short` and this repo's
// own tick.go already use elsewhere, or the value verbatim (currently only
// ever "unknown") when it is shorter than that.
func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// currentBuildCommit resolves the commit of the checkout THIS invocation of
// `estate` is running from -- knowledge.DefaultConfig's own RepoRoot
// resolution, passed straight to knowledge.ResolveBuildCommit so this is
// the exact same commit-resolution logic Generate itself uses (one
// implementation, agent-estate#1082), applied to the query side instead of
// the build side. A DefaultConfig failure (home dir unresolvable) folds in
// as unknown, never a fatal error -- this is a caveat on an otherwise-real
// answer, never a reason to refuse the query itself.
func currentBuildCommit() string {
	cfg, err := knowledge.DefaultConfig()
	if err != nil {
		return "unknown"
	}
	return knowledge.ResolveBuildCommit(cfg)
}

// foldGeneratedByIntoCoverage folds an index-vs-binary comparison
// (agent-estate#1082) into qr's own Coverage. current is the CURRENTLY
// RUNNING checkout's own commit, resolved once by the caller (via
// currentBuildCommit) and passed in here rather than re-resolved, so this
// function and printBuildCommitMismatch below always compare against the
// exact same reading. Both sides must be positively resolved ("unknown" on
// either side means no comparison could be made at all, not a mismatch --
// #1082's own "not a guess" requirement: an unknown commit is never
// treated as matching OR as differing). A real mismatch folds in as
// CoverageBinaryMismatch, naming both commits; a real match folds in
// nothing at all, the same "no reason needed when everything is fine"
// shape coverageFromSources already uses. generatedBy.Commit == "" covers
// an index written before this field existed -- treated the same as
// "unknown", never as a false mismatch or a false match.
func foldGeneratedByIntoCoverage(cov knowledge.Coverage, generatedBy knowledge.GeneratedBy, current string) knowledge.Coverage {
	indexCommit := generatedBy.Commit
	if indexCommit == "" {
		indexCommit = "unknown"
	}
	if indexCommit == "unknown" || current == "unknown" || current == "" {
		return cov
	}
	if indexCommit == current {
		return cov
	}
	return cov.WithFreshnessReason(knowledge.CoverageBinaryMismatch, "",
		fmt.Sprintf("index built by %s, this checkout is at %s -- usually fine, not a refusal; regenerate with `estate knowledge` if this query needs the newer commit's own changes reflected",
			shortCommit(indexCommit), shortCommit(current)))
}

// printBuildCommitMismatch prints the same index-vs-binary finding
// foldGeneratedByIntoCoverage folds into Coverage, so a human reading
// prose sees what a machine reading Coverage sees -- the same
// one-measurement-two-renderings discipline printIndexFreshness/
// foldFreshnessIntoCoverage already established for source staleness.
// current is the same resolved value the caller passes to
// foldGeneratedByIntoCoverage. Silent when generatedBy carries no
// resolvable commit (nothing to compare) or the two commits match.
func printBuildCommitMismatch(generatedBy knowledge.GeneratedBy, current string) {
	indexCommit := generatedBy.Commit
	if indexCommit == "" {
		indexCommit = "unknown"
	}
	if indexCommit == "unknown" || current == "unknown" || current == "" || indexCommit == current {
		return
	}
	fmt.Printf("note: index built by commit %s, this checkout is at %s -- not a problem by itself, just what built what\n",
		shortCommit(indexCommit), shortCommit(current))
}

// formatAge renders a duration the way a human reading terminal output
// wants it -- one unit, rounded down, never a Go-native
// "3h24m10.001s". A negative duration (a source's clock skewed ahead of
// the index's own, or the reverse) still renders as a magnitude with its
// sign preserved by the caller's own wording, never a panic or a bare
// "-3h".
func formatAge(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
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
		// Swapouts and worktrees print on the PASSING path too. Reasons only
		// print on refusal, so before this line an operator reading "within
		// limits" saw output byte-identical in shape to the pre-fix gate that
		// misled everyone on 2026-09-03 -- no way to tell whether paging was
		// measured or whether these limits were wired at all.
		fmt.Printf("load %.2f/core  free %.0fMB  swapouts %.0f/%s  worktrees %d  inflight %d  weekly budget %.0f%% left\n",
			v.Reading.LoadPerCore, v.Reading.FreeMemMB, v.Reading.SwapoutRate, pressure.SampleWindow(),
			v.Reading.Worktrees, v.Reading.InFlight, v.Reading.WeeklyRemaining)
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
		if len(os.Args) > 2 && os.Args[2] == "query" {
			includePrivate, asJSON, rest, unknown := parseKnowledgeArgs(os.Args[3:])
			if unknown != "" {
				fmt.Fprintf(os.Stderr, "estate: unrecognised flag %q for knowledge query -- valid: --private, --json\n", unknown)
				os.Exit(2)
			}
			if len(rest) == 0 {
				fmt.Fprintln(os.Stderr, "usage: estate knowledge query [--private] [--json] <question>")
				os.Exit(2)
			}
			out, err := knowledge.DefaultOutputPath()
			if err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			question := strings.Join(rest, " ")
			qr := knowledge.Query(out, question, 0, includePrivate)
			// agent-estate#1080: fold #1047's staleness comparison into
			// Coverage itself, not just the prose printIndexFreshness
			// prints below -- a machine caller reading only Coverage must
			// see the same freshness finding a human reading the note
			// sees. No-op when IndexGeneratedAt is zero (the two
			// index-read-failure states already returned above).
			qr.Coverage = foldFreshnessIntoCoverage(qr.Coverage, qr.IndexGeneratedAt)
			// agent-estate#1082: fold the index-vs-binary comparison in
			// the same way -- detection, not prevention or refusal (see
			// foldGeneratedByIntoCoverage's own doc comment).
			qr.Coverage = foldGeneratedByIntoCoverage(qr.Coverage, qr.IndexGeneratedBy, currentBuildCommit())
			if asJSON {
				printKnowledgeQueryJSON(qr)
			} else {
				printKnowledgeQuery(qr)
			}
			os.Exit(knowledgeQueryExitCode(qr.State))
			return
		}
		if len(os.Args) > 2 && os.Args[2] == "get" {
			includePrivate, asJSON, rest, unknown := parseKnowledgeArgs(os.Args[3:])
			if unknown != "" {
				fmt.Fprintf(os.Stderr, "estate: unrecognised flag %q for knowledge get -- valid: --private, --json\n", unknown)
				os.Exit(2)
			}
			if len(rest) == 0 {
				fmt.Fprintln(os.Stderr, "usage: estate knowledge get [--private] [--json] <id>")
				os.Exit(2)
			}
			out, err := knowledge.DefaultOutputPath()
			if err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			item, ok, reason := knowledge.Get(out, rest[0], includePrivate)
			// agent-estate#1061: resolve the disclosure state for this
			// item's own prompt_id before printing anything -- only when
			// Get actually succeeded and the item carries one at all (the
			// same gate the prompt_id print line below already applies;
			// the four non-corpus sources never set PromptID). A failure
			// to CHECK (corpus unreadable, path unresolvable) is carried
			// as discErr and reported as "could not check", never
			// silently promoted into one of the four real states.
			var disc *knowledge.Disclosure
			var discErr string
			if ok && item.PromptID != "" {
				cfg, cfgErr := knowledge.DefaultConfig()
				if cfgErr != nil {
					discErr = cfgErr.Error()
				} else if d, rerr := knowledge.ResolveDisclosure(cfg.CorpusDBPath, item, includePrivate); rerr != nil {
					discErr = rerr.Error()
				} else {
					disc = &d
				}
			}
			if asJSON {
				printKnowledgeGetJSON(item, ok, reason, disc, discErr)
				if !ok {
					os.Exit(1)
				}
				return
			}
			if !ok {
				fmt.Fprintln(os.Stderr, "estate: "+reason)
				os.Exit(1)
			}
			if !item.Publishable {
				fmt.Println("*** PRIVATE MODE -- this item is private ***")
			}
			fmt.Printf("id:        %s\nsource:    %s\npermalink: %s\ntier1:     %s\ntier2:     %s\ntier3:     %s\n",
				item.ID, item.Source, item.Permalink, item.Tier1, item.Tier2, item.Tier3)
			printRepoDocsRootIfAny([]string{item.Source})
			// structural_tags carries kind: (agent-estate#1035, alongside
			// the weight:/status: tags that were already being compiled
			// but never actually printed here) -- this line is what makes
			// the advisor's "as a question, not as law" test visible: a
			// question item's tags read "kind:question, weight:hard,
			// status:resolved", never rendered identically to a hard
			// parameter's "kind:parameter, weight:hard, status:acted".
			if len(item.StructuralTags) > 0 {
				fmt.Printf("tags:      %s\n", strings.Join(item.StructuralTags, ", "))
			}
			if item.PromptID != "" {
				fmt.Printf("prompt_id: %s\n", item.PromptID)
				// agent-estate#1061: state whether this prompt_id actually
				// leads anywhere a caller may open -- never the text
				// itself (see disclosure.go's own "never the text" note).
				switch {
				case disc != nil:
					fmt.Printf("provenance: %s -- %s\n", disc.State, disc.Detail)
				case discErr != "":
					fmt.Printf("provenance: could not check -- %s\n", discErr)
				}
			}
			return
		}

		// agent-estate#1061 Finding 3: any other os.Args[2] is a typo, not a
		// request to regenerate. A bare `estate knowledge` (os.Args[2]
		// absent) still falls through to generation below -- that is the
		// documented behaviour -- but an unrecognised subcommand must be
		// refused before it reaches knowledge.Generate/Write, which writes to
		// whatever path DefaultOutputPath resolves: this turn's own per-turn
		// index from inside a dispatch worktree, or the shared index every
		// other lane reads from anywhere else (#1048).
		//
		// --allow-coverage-loss (agent-estate#1123) is the one flag this
		// path accepts rather than a subcommand: it does not change what
		// gets generated, only whether a regeneration that would make a
		// source strictly worse than what's already on disk is allowed to
		// proceed. See the coverage-regression block below for what it
		// gates.
		allowCoverageLoss := len(os.Args) > 2 && os.Args[2] == "--allow-coverage-loss"
		if len(os.Args) > 2 && !allowCoverageLoss {
			fmt.Fprintf(os.Stderr, "estate: unrecognised knowledge subcommand %q -- valid: query, get, --allow-coverage-loss, or no subcommand to regenerate\n", os.Args[2])
			os.Exit(2)
		}

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
		// agent-estate#1123: refuse to silently overwrite a healthier index
		// already on disk with a degraded one. If out does not exist yet or
		// is not a valid compiled index, there is nothing to regress
		// against -- knowledge.Read's own error is not this guard's
		// concern, so it is deliberately ignored here rather than surfaced.
		if existing, readErr := knowledge.Read(out); readErr == nil {
			if regressions := knowledge.CoverageRegressions(existing, res); len(regressions) > 0 {
				fmt.Fprintln(os.Stderr, "estate: this regeneration would reduce coverage relative to the index already on disk:")
				for _, r := range regressions {
					fmt.Fprintln(os.Stderr, "  "+r)
				}
				if !allowCoverageLoss {
					fmt.Fprintln(os.Stderr, "estate: refusing to write -- pass `estate knowledge --allow-coverage-loss` to write it anyway")
					os.Exit(1)
				}
				fmt.Fprintln(os.Stderr, "estate: --allow-coverage-loss set -- writing the degraded index anyway")
			}
		}
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

	case "toolusage":
		// agent-estate#1096: which tools did a dispatched turn actually
		// invoke, read from its harness transcript via the session id join
		// #990 built. Read-only, counts-only -- see internal/toolusage's
		// package doc for the three parses this replaces and why they were
		// wrong.
		root, terr := toolusage.DefaultTranscriptsRoot()
		if terr != nil {
			fmt.Fprintln(os.Stderr, "estate:", terr)
			os.Exit(2)
		}
		cur, err := l.Current()
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		if len(os.Args) > 2 && os.Args[2] == "--recent" {
			n := 20
			if len(os.Args) > 3 {
				fmt.Sscanf(os.Args[3], "%d", &n)
			}
			recs := toolusage.RecentWithSession(cur, n)
			if len(recs) == 0 {
				fmt.Println("no completed turn in this ledger has a recorded session id")
				break
			}
			var all []toolusage.Counts
			resolved, missing := 0, 0
			for _, r := range recs {
				path, ferr := toolusage.FindTranscript(root, *r.SessionID)
				if ferr != nil {
					missing++
					continue
				}
				c, perr := toolusage.Parse(path)
				if perr != nil {
					fmt.Fprintln(os.Stderr, "estate: could not read transcript for", r.ID, ":", perr)
					continue
				}
				all = append(all, c)
				resolved++
			}
			printToolUsage(toolusage.Merge(all))
			fmt.Printf("\n%d turn(s) considered, %d transcript(s) resolved, %d not found\n", len(recs), resolved, missing)
			break
		}
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: estate toolusage <turn-id> | estate toolusage --recent [n]")
			os.Exit(2)
		}
		rec, rerr := toolusage.Resolve(cur, os.Args[2])
		if rerr != nil {
			fmt.Fprintln(os.Stderr, "estate:", rerr)
			os.Exit(1)
		}
		if rec.SessionID == nil || *rec.SessionID == "" {
			fmt.Println("no session id recorded for this turn -- see ledger.Record.SessionID's doc comment for when a harness reports none")
			break
		}
		path, ferr := toolusage.FindTranscript(root, *rec.SessionID)
		if ferr != nil {
			fmt.Fprintln(os.Stderr, "estate:", ferr)
			os.Exit(1)
		}
		c, perr := toolusage.Parse(path)
		if perr != nil {
			fmt.Fprintln(os.Stderr, "estate:", perr)
			os.Exit(2)
		}
		printToolUsage(c)

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
			// since is the PREVIOUS tick's own `at`, read before this entry is
			// appended -- the zero value means this is the first tick ever
			// recorded, in which case there is no window to bound either the
			// gap or observed spend against, and both stay nil (agent-estate#982).
			since := tick.LastAt(path)
			if !since.IsZero() {
				gap := int64(e.At.Sub(since).Seconds())
				e.GapSeconds = &gap

				// Observed spend for the window between the previous tick
				// and this one: every task whose outcome became known
				// (reached a terminal ledger state) in that window, keyed on
				// the terminal record's own At rather than when the task was
				// dispatched (agent-estate#989 -- dispatch-time keying let a
				// slow task's cost fall permanently behind every later
				// window's `since` and never be counted at all). This is
				// NOT this tick's cost -- the Director's own turn is never
				// dispatched by the estate, so no harness result envelope
				// for it ever reaches the ledger, and that gap cannot be
				// closed from here. See tick.Entry.ObservedSpendUSD's doc
				// comment.
				cur, err := l.Current()
				if err != nil {
					fmt.Fprintln(os.Stderr, "estate: cannot read the ledger to attribute observed spend for this window, recording the tick without it:", err)
				} else {
					turns, turnsWithCost, total := spend.WindowedByObservation(cur, since, e.At)
					t := int64(turns)
					e.ObservedTurns = &t
					if turnsWithCost > 0 {
						e.ObservedSpendUSD = &total
						twc := int64(turnsWithCost)
						e.ObservedTurnsWithCost = &twc
					}
				}
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
			if e.GapSeconds == nil {
				fmt.Println("gap since previous tick: none -- this is the first tick this log has recorded")
			} else {
				fmt.Printf("gap since previous tick: %ds (cron cadence, not work duration -- see docs/director-loop.md)\n", *e.GapSeconds)
			}
			// Classified by tick.ReadSpend rather than branched on the
			// pointers directly, so this printer cannot dereference a field
			// the entry does not carry -- see tick.SpendKind for why the
			// pairing is checked at every read instead of trusted.
			switch r := e.Spend(); r.Kind {
			case tick.SpendNoWindow:
				fmt.Println("observed spend this window: not measured -- no previous tick to bound a window against")
			case tick.SpendReported:
				fmt.Printf("observed spend this window: $%.4f across %d observed turn(s) that reported a cost, excludes the Director's own turn cost (not observable from here) -- see tick.Entry.ObservedSpendUSD\n", r.USD, r.TurnsWithCost)
			case tick.SpendNoneReported:
				fmt.Printf("observed spend this window: not reported by any of %d turn(s) that FINISHED this window -- not pending, these are done; their harness (e.g. codex) reports no dollar figure at all -- excludes the Director's own turn cost either way\n", r.Turns)
			case tick.SpendUnreadable:
				fmt.Printf("observed spend this window: could not be read honestly -- %s; no figure is shown rather than a made-up one\n", r.Why)
			default:
				fmt.Println("observed spend this window: no turns finished this window -- excludes the Director's own turn cost either way")
			}

		case "escalate":
			// agent-estate#923: the stop condition has no way to represent
			// "stalled, escalated, awaiting a response" -- so this records
			// exactly that, in its own log, never the tick log. See
			// tick.RecordEscalation's doc comment for why an escalation must
			// never be written as a tick's artifact.
			if len(os.Args) < 5 {
				fmt.Fprintln(os.Stderr, "estate: tick escalate needs a phase item and who was told")
				os.Exit(2)
			}
			known, err := tick.KnownPhases("docs/phase-plan.md")
			if err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			if err := tick.CheckPhaseItem(os.Args[3], known); err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			where := strings.TrimSpace(strings.Join(os.Args[4:], " "))
			head, err := exec.Command("git", "log", "-1", "--format=%H", "--", "src/").Output()
			if err != nil {
				fmt.Fprintln(os.Stderr, "estate: cannot read src head, refusing to write an escalation that cannot be compared:", err)
				os.Exit(2)
			}
			ee := tick.EscalationEntry{
				At:        time.Now().UTC(),
				PhaseItem: os.Args[3],
				SrcHead:   strings.TrimSpace(string(head)),
				Where:     where,
			}
			if err := tick.RecordEscalation(tick.EscalationPath(), ee); err != nil {
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			fmt.Printf("escalation recorded for %s -- told %s\n", ee.PhaseItem, where)
			fmt.Println("this does not clear the stall; it lets the next `tick check` report STALLED, escalated instead of STALLED, unacknowledged")

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
			//
			// agent-estate#923: a stalled tick records nothing, so the
			// window never changes and the same stall reports forever with
			// no way to say "a human was told". CheckWithEscalation layers
			// that acknowledgment on top without letting it clear the
			// stall itself -- see its own doc comment.
			v, err := tick.CheckWithEscalation(path, tick.EscalationPath(), newResolver(defaultHTTPStatus, defaultGHAPI))
			if err != nil {
				// Could not measure. Never clean.
				fmt.Fprintln(os.Stderr, "estate:", err)
				os.Exit(2)
			}
			// Surface the most recently recorded tick's own gap and
			// dispatch-spend figures here -- this is a place a human already
			// looks, every tick, per docs/director-loop.md step 1 -- rather
			// than a new view (agent-estate#982). Printed regardless of the
			// verdict below, so it shows on a stalled tick too.
			if last, ok, lerr := tick.LastEntry(path); lerr != nil {
				fmt.Fprintln(os.Stderr, "estate: cannot read the last tick's gap/spend figures:", lerr)
			} else if ok {
				if last.GapSeconds == nil {
					fmt.Println("last tick's gap: none recorded (its first-ever tick, or predates agent-estate#982)")
				} else {
					fmt.Printf("last tick's gap: %ds (cron cadence, not work duration)\n", *last.GapSeconds)
				}
				// These figures came off disk, written by some earlier
				// process this command cannot vouch for, so they are
				// classified before they are printed -- a broken pair here
				// used to panic and take `tick check`, the loop's own stop
				// condition, down with it. See tick.SpendKind.
				switch r := last.Spend(); r.Kind {
				case tick.SpendNoWindow:
					fmt.Println("last tick's observed spend: not recorded (no previous tick to bound a window against, or predates agent-estate#982)")
				case tick.SpendReported:
					fmt.Printf("last tick's observed spend: $%.4f across %d observed turn(s) that reported a cost, excludes the Director's own turn cost (not observable from here)\n", r.USD, r.TurnsWithCost)
				case tick.SpendNoneReported:
					fmt.Printf("last tick's observed spend: not reported by any of %d turn(s) that FINISHED that window -- not pending, these are done; their harness (e.g. codex) reports no dollar figure at all -- excludes the Director's own turn cost either way\n", r.Turns)
				case tick.SpendUnreadable:
					fmt.Printf("last tick's observed spend: could not be read honestly -- %s; no figure is shown rather than a made-up one\n", r.Why)
				default:
					fmt.Println("last tick's observed spend: no turns finished that window -- excludes the Director's own turn cost either way")
				}
			}
			if v.Stalled && v.Escalated {
				// A different exit code, not a clean one: the phase item and
				// src head named in v.Reason are STILL stalled. What changed
				// is that a human has been told, so the loop may spend this
				// tick on OTHER work instead of stopping outright -- it must
				// not spend it pretending this stall resolved.
				fmt.Fprintln(os.Stderr, "STALLED, escalated: "+v.Reason)
				if v.EscalationCount > 1 {
					fmt.Fprintf(os.Stderr, "repeated escalation: %d escalation(s) recorded against this same stall -- this is not a healthy loop, it is a stuck one that keeps saying so\n", v.EscalationCount)
				}
				fmt.Fprintln(os.Stderr, "may continue on other work while the escalation stands (brief section 3); this phase item/src head remains stalled")
				os.Exit(3)
			}
			if v.Stalled {
				fmt.Fprintln(os.Stderr, "STALLED: "+v.Reason)
				fmt.Fprintln(os.Stderr, "stop ticking and escalate: estate tick escalate <phase-item> <where>, then re-check")
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

	case "spend":
		// Current(), not the raw append log: Spend fields are only ever set
		// on a turn's terminal record (main.go's dispatch case, right before
		// it appends rec), and Current() collapses to exactly that record
		// per task id -- so this never double-counts a turn's earlier
		// Dispatched record, which never carries a Spend field at all.
		rs, err := l.Current()
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(2)
		}
		rep := spend.Aggregate(rs)
		if rep.TotalTurns == 0 {
			fmt.Println("no tasks recorded")
			break
		}
		for _, g := range rep.ByHarness {
			fmt.Printf("%s -- %d turn(s)\n", g.Harness, g.Turns)
			if g.TurnsWithCost > 0 {
				fmt.Printf("  cost:   $%.4f across %d turn(s) that reported one\n", g.TotalCostUSD, g.TurnsWithCost)
			} else {
				fmt.Println("  cost:   not reported by this harness")
			}
			if g.TurnsWithTokens > 0 {
				fmt.Printf("  tokens: in=%d out=%d cache_read=%d cache_creation=%d, across %d turn(s) that reported any\n",
					g.InputTokens, g.OutputTokens, g.CacheReadTokens, g.CacheCreationTokens, g.TurnsWithTokens)
			} else {
				fmt.Println("  tokens: none recorded")
			}
			if g.TurnsWithNeither > 0 {
				fmt.Printf("  %d turn(s) recorded neither a cost nor tokens -- absent, not free (predates #977 or unreadable harness output)\n", g.TurnsWithNeither)
			}
			if g.TurnsWithModelBreakdown > 0 {
				fmt.Printf("  by model, from %d of those %d turn(s) that reported a per-model breakdown:\n", g.TurnsWithModelBreakdown, g.Turns)
				for _, m := range g.ByModel {
					if m.TurnsWithCost > 0 {
						fmt.Printf("    %s: $%.4f across %d turn(s)\n", m.Model, m.TotalCostUSD, m.TurnsWithCost)
					} else {
						fmt.Printf("    %s: cost not reported\n", m.Model)
					}
				}
			} else {
				fmt.Println("  by model: not reported by this harness (agent-estate#981)")
			}
		}
		fmt.Printf("\n%d task(s) total, %d with some spend recorded, %d with none\n",
			rep.TotalTurns, rep.TurnsWithAnySpend, rep.TotalTurns-rep.TurnsWithAnySpend)
		switch {
		case len(rep.HarnessesReportingCost) == 0:
			fmt.Println("no harness in this ledger has reported a dollar cost -- no total is printed")
		case len(rep.HarnessesReportingTokensOnly) > 0:
			fmt.Printf("dollar cost is only available for %s -- %s report tokens only, never dollars; there is no combined total across them\n",
				strings.Join(rep.HarnessesReportingCost, ", "), strings.Join(rep.HarnessesReportingTokensOnly, ", "))
		default:
			fmt.Printf("dollar cost is available for every harness in this ledger (%s) -- see per-harness figures above, still never summed into one line\n",
				strings.Join(rep.HarnessesReportingCost, ", "))
		}

	case "dispatch":
		if len(os.Args) < 4 {
			usage()
			os.Exit(2)
		}
		// Which harness runs this turn. A --harness= token can appear
		// anywhere in the argument list (it is not positional, unlike
		// everything else here), so it is stripped out before role/issue/
		// brief parsing looks at fixed positions. Defaults to $ESTATE_HARNESS,
		// then "claude", so every dispatch site that predates internal/harness
		// keeps behaving exactly as before without being touched.
		harnessName := os.Getenv("ESTATE_HARNESS")
		if harnessName == "" {
			harnessName = "claude"
		}
		rest := make([]string, 0, len(os.Args)-2)
		for _, a := range os.Args[2:] {
			if strings.HasPrefix(a, "--harness=") {
				harnessName = strings.TrimPrefix(a, "--harness=")
				continue
			}
			rest = append(rest, a)
		}
		// A review turn is dispatched against the same issue as the work it
		// reviews, so the issue alone can never tell the merge gate apart
		// from an authoring turn (agent-estate#926). Role, recorded here at
		// dispatch time, is what removes the ambiguity -- never inferred
		// later from what a lane or a PR comment claims about itself.
		role := ledger.RoleAuthor
		reviewPR := 0
		// fixPass marks a role=author turn dispatched to CONTINUE an
		// existing pull request (agent-estate#940's "does not survive a fix
		// pass" follow-up) rather than open a new one. It reuses reviewPR
		// to carry the PR number -- the two are mutually exclusive by
		// rest[0], never both set -- and is recorded on the ledger via
		// the SAME PR field a role=reviewer turn already uses, so the gate
		// can scope a fix-pass lookup to "completed role=author records for
		// THIS PR" without a new record shape.
		fixPass := false
		issueIdx, briefIdx := 0, 1
		switch {
		case len(rest) > 0 && rest[0] == "review":
			if len(rest) < 4 {
				fmt.Fprintln(os.Stderr, "estate: dispatch review needs <pr> <issue> <brief-file>")
				os.Exit(2)
			}
			role = ledger.RoleReviewer
			if _, err := fmt.Sscanf(rest[1], "%d", &reviewPR); err != nil {
				fmt.Fprintln(os.Stderr, "estate: pr must be a number:", rest[1])
				os.Exit(2)
			}
			issueIdx, briefIdx = 2, 3
		case len(rest) > 0 && rest[0] == "fix":
			if len(rest) < 4 {
				fmt.Fprintln(os.Stderr, "estate: dispatch fix needs <pr> <issue> <brief-file>")
				os.Exit(2)
			}
			fixPass = true
			if _, err := fmt.Sscanf(rest[1], "%d", &reviewPR); err != nil {
				fmt.Fprintln(os.Stderr, "estate: pr must be a number:", rest[1])
				os.Exit(2)
			}
			issueIdx, briefIdx = 2, 3
		default:
			if len(rest) < 2 {
				usage()
				os.Exit(2)
			}
		}
		issue, briefPath := rest[issueIdx], rest[briefIdx]
		// Refused rather than defaulted when unknown: silently falling back
		// to claude would run the turn on a harness the caller did not
		// choose. Checked here, before the brief is even read, so an unknown
		// or uninstalled harness never gets as far as touching pressure,
		// ledger, or worktree state.
		h, err := harness.Lookup(harnessName)
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate: refusing to dispatch --", err)
			os.Exit(1)
		}
		if ok, _ := harness.Available(harnessName); !ok {
			fmt.Fprintf(os.Stderr, "estate: refusing to dispatch -- harness %q is registered but its binary is not on PATH\n", harnessName)
			os.Exit(1)
		}
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
		repoRoot := strings.TrimSpace(string(topOut))

		var wt *isolate.Worktree
		if fixPass {
			// A fix pass continues the PR's OWN branch, not a fresh one --
			// agent-estate#940's "does not survive a fix pass" gap. The
			// branch name is read from GitHub itself (never a caller
			// argument, never a brief's guess), and internal/isolate fetches
			// and checks it out fresh, so this dispatch's worktree starts
			// from what the PR's head genuinely is right now, not a
			// possibly-stale local ref.
			branch, berr := prHeadBranch(reviewPR)
			if berr != nil {
				fmt.Fprintln(os.Stderr, "estate: refusing to dispatch fix pass -- cannot read PR #"+strconv.Itoa(reviewPR)+"'s own head branch:", berr)
				os.Exit(1)
			}
			wt, err = isolate.CreateOnBranch(repoRoot, id, branch)
		} else {
			wt, err = isolate.Create(repoRoot, id)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate: "+err.Error())
			os.Exit(1)
		}

		// The merge gate (agent-estate#940) derives authorship from the PR's
		// own head ref, joined to this exact dispatch id's role=author
		// ledger record -- never from an issue number the brief or the PR
		// body asserts. That join only exists if the PR is opened FROM
		// wt.Branch (a fresh dispatch) or continues onto it (a fix pass,
		// joined by chaining Base/HeadSHA back to the PR's original
		// dispatch -- see internal/gate's package doc). A brief that
		// instead tells (or lets) the lane invent its own feature branch
		// produces a PR the gate cannot join to anything, refused with no
		// override. Author turns only: a role=reviewer turn never opens or
		// continues a PR's branch, so it has nothing to state here.
		// Reviewer turns instead get the second-source verdict contract
		// (agent-estate#949), with this dispatch's own id and PR number
		// already filled in so it cannot be forgotten, reworded, or
		// invented by whatever brief the Director happened to write.
		// roleGrounding is the one place all three blocks are produced, so
		// a test can exercise the role switch directly without running the
		// rest of dispatch.
		grounded += roleGrounding(role, id, reviewPR, wt.Branch, fixPass)

		// The Dispatched record is appended once the pid is known (below,
		// right after cmd.Start()), not here -- agent-estate#948 wires up
		// ledger.Record.PID, and a dead turn can only be reclaimed by a pid
		// that was actually recorded. Appending here, before the process
		// exists, would leave the earlier PID-less shape and give
		// internal/reclaim nothing to check.

		// Make the turn watchable before it starts. 208 dispatches have run
		// and the operator could not watch one of them
		// (agent-estate#1001); his parameters
		// `terminals=tmux_persistent_required` and
		// `observability=jon_can_watch_via_tmux` say that is the wrong shape.
		//
		// This is a MIRROR, not a transport. The turn below is still the
		// subprocess whose stdout this function reads; internal/mirror only
		// tees that stream into a transcript and opens a tmux window running
		// `tail -f` on it. Nothing types into that pane -- internal/mirror has
		// a test that fails if send-keys ever appears in it.
		//
		// The bound is the in-flight cap, read from the same pressure.Default()
		// that bounds concurrent turns rather than written down twice, so the
		// two cannot drift. A mirror that cannot be opened -- no tmux, or every
		// window in the bound belongs to a live turn -- is reported and then
		// ignored: visibility never gates work.
		mcfg := mirror.Default(pressure.Default().MaxInFlight)
		if os.Getenv("ESTATE_MIRROR") == "0" {
			mcfg.Enabled = false
		}
		if s := os.Getenv("ESTATE_MIRROR_SESSION"); s != "" {
			mcfg.Session = s
		}
		if d := os.Getenv("ESTATE_MIRROR_DIR"); d != "" {
			mcfg.Dir = d
		}
		// Scopes every tmux call this dispatch makes to a private socket
		// directory with $TMUX unset. Production leaves it empty and uses the
		// operator's own server; it exists so a demonstration or an
		// end-to-end check can drive a REAL dispatch without addressing the
		// default socket, which is invariant 4's whole point.
		if d := os.Getenv("ESTATE_MIRROR_TMUX_TMPDIR"); d != "" {
			mcfg.TmuxTmpdir = d
		}
		mir, merr := mirror.Open(mcfg, mirror.Meta{
			ID: id, Issue: issue, Role: string(role), Harness: harnessName, Worktree: wt.Path,
		})
		if merr != nil {
			fmt.Fprintln(os.Stderr, "estate: dispatching unwatched --", merr)
		} else {
			fmt.Printf("watch: tmux attach -t %s  (window %s)  |  tail -f %s\n",
				mcfg.Session, mir.WindowID(), mir.Path())
		}
		// A terminal state is stamped into the transcript however this turn
		// ends, so a pane a human opens after the fact says what happened
		// instead of trailing off. Close is idempotent, so the real state
		// recorded below wins and this only fires on a path that never got
		// there. os.Exit skips defers, so EVERY exiting path between here and
		// the ledger append closes explicitly -- there are three, and review
		// of #1003 found the h.Start one missing while this comment claimed
		// there were two. TestEveryExitInsideTheMirrorRegionStampsTheTranscript
		// now enumerates them from the parsed source rather than asserting
		// that some known strings are present, which is what let a third path
		// hide.
		defer mir.Close("unknown", "dispatch exited before recording a terminal state")

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		// h.Start builds the command, wires the prompt to its stdin, and
		// hands back how to read the agent's final message out of whatever
		// shape this harness produces -- see internal/harness's doc comment
		// for why that split exists. The ledger, the gate and the worktree
		// stay owned here regardless of which harness ran the turn.
		turn, err := h.Start(ctx, wt.Path, grounded)
		if err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			mir.Close("failed", "the harness could not build the turn: "+err.Error())
			os.Exit(2)
		}
		defer turn.Cleanup()
		cmd := turn.Cmd
		var stdout bytes.Buffer
		// The buffer stays the transport's own capture -- Result, Spend and
		// SessionID are all read from it, unchanged. The mirror is a second
		// destination for the same bytes, and one that cannot fail back onto
		// this path: mirror's sink never returns an error, so a broken
		// transcript can never abort os/exec's copier and take the turn with
		// it.
		cmd.Stdout = io.MultiWriter(&stdout, mir.Stdout())
		// exec.Cmd.Output() only captures the child's stderr into the
		// returned *exec.ExitError when cmd.Stderr is nil -- and this path
		// does not use Output() at all (cmd.Start()/cmd.Wait() below, so the
		// pid is known before the ledger records Dispatched). Wiring our own
		// buffer here is what makes that stderr readable at all instead of
		// being lost with no capture path (agent-estate#950). The buffer
		// fills as the child runs, so it holds whatever the child wrote even
		// if the run times out below, not only on a clean exit.
		// Mirrored too, and line-tagged there. The three infrastructure deaths
		// this estate actually sees -- API error, quota window, signal kill --
		// announce themselves on stderr, and those are exactly the moments a
		// human wants a screen to look at.
		var stderrBuf bytes.Buffer
		cmd.Stderr = io.MultiWriter(&stderrBuf, mir.Stderr())

		// Started, not merely queued, before the ledger records it: only a
		// process that actually exists has a pid to record. A dispatch that
		// fails to start leaves nothing running and is recorded Failed
		// directly, never Dispatched with no pid to ever positively resolve.
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "estate: cannot start turn:", err)
			mir.Close("failed", "the turn never started: "+err.Error())
			if aerr := l.Append(ledger.Record{ID: id, Issue: issue, Lane: id, Role: role, PR: reviewPR, State: ledger.Failed, Harness: harnessName, Note: "failed to start: " + err.Error()}); aerr != nil {
				fmt.Fprintln(os.Stderr, "estate: cannot record dispatch failure:", aerr)
			}
			os.Exit(1)
		}
		pid := cmd.Process.Pid
		// The pid is recorded the moment it is known -- before the turn does
		// anything else -- because it is the one fact that later lets a dead
		// turn's slot be reclaimed without guessing. See internal/reclaim.
		mir.Logf("turn started, pid %d -- this window mirrors its output and cannot affect it", pid)
		if err := l.Append(ledger.Record{ID: id, Issue: issue, Lane: id, Role: role, PR: reviewPR, State: ledger.Dispatched, PID: pid, Harness: harnessName, Note: "worktree " + wt.Path}); err != nil {
			fmt.Fprintln(os.Stderr, "estate: cannot record dispatch:", err)
			mir.Close("unknown", "the turn is running but the ledger could not record it: "+err.Error())
			os.Exit(2)
		}
		fmt.Printf("dispatched %s (role=%s, harness=%s, pid=%d) in %s (grounded in %d operator parameters)\n", id, role, harnessName, pid, wt.Path, len(params))

		runErr := cmd.Wait()
		out := stdout.Bytes()

		rec := ledger.Record{ID: id, Issue: issue, Lane: id, Role: role, PR: reviewPR, PID: pid, Harness: harnessName}
		// Spend is read the same way Result is -- straight out of this exact
		// subprocess's own stdout, whatever state the turn ends up recorded
		// in below. A turn that timed out or exited non-zero may still have
		// written a spend-bearing diagnostic (claude does this on some
		// failures), so this is attempted unconditionally rather than only
		// on the Complete path; a harness that reported nothing usable
		// leaves every Spend* field nil, never a guess.
		if sp, sperr := turn.Spend(out); sperr == nil {
			rec.SpendCostUSD = sp.CostUSD
			rec.SpendInputTokens = sp.InputTokens
			rec.SpendOutputTokens = sp.OutputTokens
			rec.SpendCacheReadTokens = sp.CacheReadTokens
			rec.SpendCacheCreationTokens = sp.CacheCreationTokens
			if sp.ByModel != nil {
				rec.SpendByModel = make(map[string]ledger.ModelSpend, len(sp.ByModel))
				for model, ms := range sp.ByModel {
					rec.SpendByModel[model] = ledger.ModelSpend{
						CostUSD:             ms.CostUSD,
						InputTokens:         ms.InputTokens,
						OutputTokens:        ms.OutputTokens,
						CacheReadTokens:     ms.CacheReadTokens,
						CacheCreationTokens: ms.CacheCreationTokens,
					}
				}
			}
		}
		// SessionID is read the same unconditional way Spend is -- straight
		// out of this exact subprocess's own stdout, regardless of the state
		// this turn ends up recorded in below. A harness that reported no
		// usable handle for this turn leaves SessionID nil, never "".
		if sid, siderr := turn.SessionID(out); siderr == nil {
			rec.SessionID = &sid
		}
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
			// Reading the result is the harness's job -- each one emits its
			// own shape (claude's single JSON envelope on stdout, codex's
			// own result file). Exit 0 with output the harness cannot read
			// is NOT a clean completion, and is recorded Unknown rather than
			// Failed: the turn may well have done its work.
			res, resErr := turn.Result(out)
			if resErr != nil {
				rec.State, rec.Note = ledger.Unknown, resErr.Error()
			} else {
				rec.State, rec.Result = ledger.Complete, res
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
			// Base is what this worktree started FROM -- isolate.Create's
			// caller-checkout tip for a fresh dispatch, or
			// isolate.CreateOnBranch's freshly-fetched PR tip for a fix
			// pass. Recorded for every role=author turn now that the gate
			// can chain a fix pass's Base back to an earlier turn's own
			// HeadSHA (see internal/gate's package doc).
			rec.Base = wt.Base
		}

		// Tear the worktree down only when it is empty. A turn that left work
		// behind has output nobody has collected, and an isolated worktree
		// that still exists is a report; a deleted one is unrecoverable.
		if err := wt.Remove(); err != nil {
			rec.Note = strings.TrimSpace(rec.Note + "; " + wt.Path + " kept: " + err.Error())
			fmt.Fprintln(os.Stderr, "estate: "+err.Error())
		}

		// The transcript's footer names the state the ledger is about to
		// record, so the pane and the record agree. Close does not kill the
		// window: a turn dying is exactly when a human wants the screen, and
		// the pane stays readable until a later dispatch retires it under the
		// bound.
		mir.Close(string(rec.State), rec.Note)

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
