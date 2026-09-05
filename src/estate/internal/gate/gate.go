// Package gate decides whether a pull request may merge.
//
// Four independent conditions, all of which must pass, all failing closed:
//
//  1. The pull request is open, and every required check is green AT THE
//     HEAD SHA. Not "green somewhere" -- a check that passed on an earlier
//     push says nothing about what is being merged now.
//
//  2. A dispatched turn authored the work this PR is built from, joined on
//     TWO facts together, neither sufficient alone:
//
//     (a) The PR's own head ref names a dispatch id: internal/isolate.Create
//     writes "dispatch/<id>" as the branch for a dispatched turn's own
//     worktree. This is agent-estate#940's original fix -- the join used to
//     run through closingIssuesReferences (a PR body's own "Closes #N",
//     GitHub-parsed) joined to a ledger record BY ISSUE NUMBER, which failed
//     whenever the closing issue was filed after the dispatch it closes
//     (#944), or simply never named in GitHub's parsed form (#937, #939).
//
//     (b) The PR's own head commit (headRefOid) equals a HeadSHA the
//     estate itself recorded for that exact dispatch id, read directly from
//     the worktree by main.go's dispatch case the moment that turn's own
//     subprocess exited -- never anything the subprocess said about itself.
//     This is #940's OWN follow-up fix: a branch ref is a NAME, and a name
//     is written once, at Create time, but nothing stops a later push from
//     renaming a different branch to the same "dispatch/<id>" and opening a
//     PR from it -- every lane in this repo shares one GitHub login (see
//     AGENTS.md), so pushing to an arbitrary branch name costs no more than
//     writing PR body text. (a) alone binds a STRING to a ledger id; (b)
//     binds the PR's actual CODE to it, because reaching a recorded SHA
//     under a different id requires reproducing that exact commit object
//     (same tree, parents, authorship, message) -- not just a lookalike
//     name.
//
//     What this still does NOT establish: that the estate performed the
//     push (it did not -- the agent still pushes; the estate only observed
//     what its own worktree held afterward), that the recorded worktree
//     content is itself safe or reviewed, or anything about a PR whose head
//     was later force-pushed to new content sharing the same branch name --
//     that case simply fails the SHA match and refuses, which is the
//     correct direction, but it is a refusal produced by (b), not a
//     guarantee that no such push was attempted. A PR whose head ref is not
//     a dispatch branch is refused outright -- there is no fallback to the
//     old, forgeable issue-keyed path, and no partial credit for the branch
//     name matching alone.
//
//     A head commit that does NOT match the root dispatch's own HeadSHA is
//     not refused outright any more -- see condition 2c. It is refused only
//     if 2c also fails to account for it.
//
//  2c. A FIX PASS -- a second (or third, ...) dispatch that pushes new
//     commits onto a branch a first dispatch created -- moves the PR's head
//     past the root dispatch's own recorded HeadSHA while the branch NAME
//     stays the same (agent-estate#940's own follow-up finding: "the join
//     works for a fresh dispatch, and does not survive a fix pass"). When
//     2b's direct match fails, the gate tries to WALK a chain of completed,
//     PR-scoped role=author ledger records from the root dispatch's own
//     HeadSHA to the PR's current head: `estate dispatch fix <pr> <issue>
//     <brief>` records PR (the number it was scoped to) and Base (the
//     commit internal/isolate.CreateOnBranch fetched and checked out
//     BEFORE that turn's own commits, read directly by the estate, same
//     discipline as HeadSHA) on every such turn. The gate accepts the PR's
//     head when there is an unbroken sequence of these records, each one's
//     Base equal to the PREVIOUS link's HeadSHA (the root dispatch's HeadSHA
//     for the first link), ending at a HeadSHA equal to the PR's current
//     head commit. Every link is independently estate-observed; none is
//     anything a lane's own branch name, PR body, or comment asserted about
//     itself.
//
//     What this establishes: every commit between the root dispatch's own
//     output and the PR's current head was produced inside a worktree this
//     specific chain of dispatches controlled, in order, with no gap the
//     estate did not itself observe.
//
//     What this does NOT establish, stated plainly because a prior review
//     round on this exact issue blocked a change for overclaiming: it does
//     NOT prove the estate performed the push (the agent still runs `git
//     push`; the estate only re-reads its own worktree afterward, same as
//     2b), and it does NOT prove any of the intermediate commits were
//     reviewed (only the FINAL head must clear an independent APPROVE --
//     conditions 3 and 4 below). A record whose own Base equals its own
//     HeadSHA (a fix pass that committed nothing) is never usable as a
//     chain hop -- it explains no code the PR's actual head does not
//     already trace to through an earlier link, and admitting it would let
//     an unrelated no-op record with a coincidentally matching Base sit in
//     the chain without accounting for anything.
//
//  3. That same reviewer lane has a COMPLETED review turn on record for
//     THIS pull request. A dispatched-but-unfinished review is not
//     independence: nobody has actually looked yet.
//
//  4. The reviewer's own verdict comment on the PR resolves to APPROVE,
//     parsed from a Verdict: line -- never a substring match anywhere in
//     the body -- and is not stale against the checks that ran at head.
//
// Anything unresolved -- an unknown author, a pending check, an unreadable
// ledger, a head ref that is not a dispatch branch, or a head commit that
// does not match the HeadSHA the estate itself recorded for it -- is a
// REFUSAL. "Cannot tell" is never "allowed".
package gate

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	StartedAt  string `json:"startedAt"`
}

type Comment struct {
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

type closingIssue struct {
	Number int `json:"number"`
}

type PR struct {
	Number        int            `json:"number"`
	HeadOID       string         `json:"headRefOid"`
	HeadRefName   string         `json:"headRefName"`
	State         string         `json:"state"`
	Body          string         `json:"body"`
	Checks        []Check        `json:"statusCheckRollup"`
	ClosingIssues []closingIssue `json:"closingIssuesReferences"`
	Comments      []Comment      `json:"comments"`
}

type Decision struct {
	Allow   bool
	Reasons []string
	HeadOID string
}

// fetch reads the pull request's own state from GitHub. Everything Evaluate
// needs to establish identity (headRefName, and the body only to catch a
// contradicting Author-Lane: trailer -- never to establish identity itself),
// freshness (checks plus their own startedAt) and the reviewer's public
// verdict (comments) comes from here -- never from a caller argument, per
// constraint 6 of agent-estate#926: an author who could name any issue they
// liked used to be able to merge anything, using only record shapes `estate
// dispatch` writes itself.
func fetch(repo string, pr int) (*PR, error) {
	out, err := exec.Command("gh", "pr", "view", fmt.Sprint(pr), "-R", repo,
		"--json", "number,headRefOid,headRefName,state,body,statusCheckRollup,closingIssuesReferences,comments").Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr view %s#%d: %w", repo, pr, err)
	}
	var p PR
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("decode pr: %w", err)
	}
	return &p, nil
}

// checksGreen requires every check to be COMPLETED and SUCCESS. A pending
// check is not a pass: merging under one is how a green-looking PR lands
// untested.
func checksGreen(p *PR) []string {
	if len(p.Checks) == 0 {
		return []string{"no checks reported at head " + short(p.HeadOID) + " -- refusing rather than assuming none are required"}
	}
	var bad []string
	for _, c := range p.Checks {
		switch {
		case !strings.EqualFold(c.Status, "COMPLETED"):
			bad = append(bad, fmt.Sprintf("%s is %s, not completed", c.Name, strings.ToLower(c.Status)))
		case !strings.EqualFold(c.Conclusion, "SUCCESS"):
			bad = append(bad, fmt.Sprintf("%s concluded %s", c.Name, strings.ToLower(c.Conclusion)))
		}
	}
	return bad
}

// earliestCheckStart is the earliest startedAt reported across the PR's
// checks -- the earliest moment GitHub itself observed work happening
// against the current head. Used as the staleness anchor for constraint 5:
// "measured against when the checks actually ran, not a committer date,
// which the author controls." A check run's startedAt is written by
// GitHub's own runner when it picks the job up; nothing a PR author does to
// a commit's authored/committer date touches it.
func earliestCheckStart(p *PR) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, c := range p.Checks {
		if c.StartedAt == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, c.StartedAt)
		if err != nil {
			continue
		}
		if !found || t.Before(earliest) {
			earliest = t
			found = true
		}
	}
	return earliest, found
}

// authorLanes returns every lane the ledger records as RoleAuthor on any of
// the given issues. Reviewer turns are deliberately excluded here even
// though they may share the same Issue field -- a review turn is dispatched
// against the same issue as the work it reviews, which is exactly the
// ambiguity agent-estate#926 reports: "That lane authored nothing. It was a
// review seat... The gate derives authorship from the issue prefix in a
// dispatch id, and a review turn carries the same issue as the work it
// reviews, so the two are indistinguishable." Role, recorded at dispatch,
// is what removes the ambiguity.
//
// STATUS (agent-estate#940): evaluate no longer calls this to establish
// authorship -- the issue-keyed join it implements is the join #940 found
// broken (it needs a PR body to declare "Closes #N" in GitHub's own parsed
// form, AND it needs that issue to already exist at dispatch time, which is
// false whenever the Director files the closing issue after dispatching the
// work that closes it -- agent-estate#944's exact case). It is kept, tested,
// and correct for what it does; a future caller wanting issue-keyed
// authorship as ADDITIONAL, non-blocking evidence alongside the head-ref
// join below can still reach for it.
func authorLanes(l *ledger.Ledger, issues map[string]bool) (map[string]bool, error) {
	cur, err := l.Current()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range cur {
		if r.Lane == "" || !issues[r.Issue] {
			continue
		}
		if r.EffectiveRole() != ledger.RoleAuthor {
			continue
		}
		out[r.Lane] = true
	}
	return out, nil
}

// authorFromHeadRef reports the dispatch id a PR's head ref names, if any.
// internal/isolate.Create writes exactly "dispatch/<id>" as the branch for a
// dispatched turn's own worktree (isolate.go's Create); this is the inverse
// read of that one fixed, estate-written string. Only a SINGLE leading
// occurrence is stripped -- "dispatch/dispatch/<id>" is refused rather than
// unwrapped, the same discipline verdict.go's normaliseLaneID already
// applies to the Review-Lane: trailer, and for the same reason: a doubled
// prefix is not a shape the estate ever writes, so accepting it would be
// widening the match past what "the estate wrote this" can actually prove.
func authorFromHeadRef(headRef string) (id string, ok bool) {
	if !strings.HasPrefix(headRef, DispatchBranchPrefix) {
		return "", false
	}
	id = strings.TrimPrefix(headRef, DispatchBranchPrefix)
	if id == "" || strings.HasPrefix(id, DispatchBranchPrefix) {
		return "", false
	}
	return id, true
}

// authorRecordForDispatchID returns the ledger's own role=author record for
// EXACTLY this dispatch id -- the id a PR's own head ref names, per
// authorFromHeadRef above. This is HALF of the structural join
// agent-estate#940 (and its own follow-up review) asks for: main.go's
// dispatch case writes Lane equal to id for every role=author record
// (l.Append(ledger.Record{ID: id, Lane: id, ...})), so matching on ID here
// is matching on the exact dispatched turn that created this branch --
// never an issue number a PR body asserts about itself, and never any
// OTHER authoring turn that happens to share an issue. The OTHER half is
// the caller's job: the record's own HeadSHA field, read here but compared
// against the PR's headRefOid by evaluate(), is what turns "a branch named
// this" into "a commit this estate actually observed" -- see the package
// doc's condition 2(b).
//
// State must be Complete, the same requirement reviewerRecord already
// applies to a review turn: an in-flight or abandoned dispatch never
// finished producing whatever the branch currently holds, so its own
// worktree observation (if any was even taken) cannot be trusted as the
// turn's final output.
func authorRecordForDispatchID(l *ledger.Ledger, id string) (ledger.Record, bool, error) {
	cur, err := l.Current()
	if err != nil {
		return ledger.Record{}, false, err
	}
	for _, r := range cur {
		if r.ID == id && r.EffectiveRole() == ledger.RoleAuthor && r.State == ledger.Complete {
			return r, true, nil
		}
	}
	return ledger.Record{}, false, nil
}

// authorRecordForFixPassChain is condition 2c of the package doc: it walks
// a chain of completed, PR-scoped role=author ledger records forward from a
// known-good starting commit (rootHeadSHA -- already established by the
// caller via authorRecordForDispatchID's mandatory branch-name join, never
// by this function) toward the PR's current head commit (headOID).
//
// Each hop must be a Complete role=author record recorded for EXACTLY this
// PR number (`estate dispatch fix <pr> ...` sets PR; a plain `estate
// dispatch <issue> ...` never does) whose own Base equals the SHA the
// PREVIOUS hop ended at. That is what makes this a CHAIN rather than a
// search: a record is only usable once the gate has already established
// what came immediately before it, all the way back to the root dispatch
// the PR's own head ref names. A record whose HeadSHA equals its own Base
// (a fix pass that committed nothing) is skipped -- see the package doc's
// condition 2c for why an inert record must never count as a hop.
//
// Returns the LAST hop's record (the one whose HeadSHA equals headOID) plus
// every hop's own record, root-to-tip, when the chain resolves; ok=false
// when it does not, including when no chain exists at all. The full hop
// list is what lets a caller ask "which lanes actually produced code
// between the root and the current head", not merely "which lane produced
// the head" -- see evaluate's Author-Lane: trailer check, which must accept
// a trailer naming ANY hop, not only the last one (agent-estate#940's
// "over-refuses on the chain" finding: the root dispatch authored the PR
// and wrote the trailer at open time; a later fix pass becomes the new
// chain-terminal author, and both are simultaneously true).
//
// visited guards against a cycle -- a malformed or adversarial record set
// where hops point back at an already-used SHA -- by refusing to reuse a
// starting point, rather than looping forever.
func authorRecordForFixPassChain(l *ledger.Ledger, pr int, rootHeadSHA, headOID string) (ledger.Record, []ledger.Record, bool, error) {
	cur, err := l.Current()
	if err != nil {
		return ledger.Record{}, nil, false, err
	}
	from := rootHeadSHA
	visited := map[string]bool{from: true}
	var hops []ledger.Record
	for {
		var next ledger.Record
		found := false
		for _, r := range cur {
			if r.PR != pr || r.EffectiveRole() != ledger.RoleAuthor || r.State != ledger.Complete {
				continue
			}
			if r.HeadSHA == "" || r.HeadSHA == r.Base || r.Base != from {
				continue
			}
			next, found = r, true
			break
		}
		if !found {
			return ledger.Record{}, nil, false, nil
		}
		hops = append(hops, next)
		if next.HeadSHA == headOID {
			return next, hops, true, nil
		}
		if visited[next.HeadSHA] {
			return ledger.Record{}, nil, false, nil
		}
		visited[next.HeadSHA] = true
		from = next.HeadSHA
	}
}

// reviewerRecord returns the latest COMPLETE RoleReviewer ledger record on
// file for reviewerLane against this exact PR number. This is the SECOND,
// independent source constraint 4 cross-checks the PR comment against
// (agent-estate#934): the record's Result field is written locally by the
// dispatch process itself, from the reviewer subprocess's own output, when
// that turn completes. A party who can post a GitHub PR comment under this
// estate's shared login cannot write here -- only a process this estate
// itself dispatched can. A dispatched-but-not-yet-Complete review turn does
// not satisfy this: "a dispatched but unfinished review turn is not
// independence" (constraint 3).
func reviewerRecord(l *ledger.Ledger, pr int, reviewerLane string) (ledger.Record, bool, error) {
	cur, err := l.Current()
	if err != nil {
		return ledger.Record{}, false, err
	}
	var latest ledger.Record
	found := false
	for _, r := range cur {
		if r.Lane != reviewerLane || r.PR != pr {
			continue
		}
		if r.EffectiveRole() != ledger.RoleReviewer {
			continue
		}
		if r.State != ledger.Complete {
			continue
		}
		if !found || r.At.After(latest.At) {
			latest = r
			found = true
		}
	}
	return latest, found, nil
}

// reviewerCompleted reports whether reviewerLane has a COMPLETE RoleReviewer
// record on file for this exact PR number, and its completion time -- the
// ledger-owned timestamp used as the staleness anchor's other side (see
// earliestCheckStart). A thin wrapper over reviewerRecord kept for its own
// existing callers and tests; evaluate itself calls reviewerRecord directly
// so it can also reach the record's Result field.
func reviewerCompleted(l *ledger.Ledger, pr int, reviewerLane string) (time.Time, bool, error) {
	rec, ok, err := reviewerRecord(l, pr, reviewerLane)
	return rec.At, ok, err
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// minReviewedSHAPrefix is the shortest a Reviewed-SHA trailer may be and
// still be trusted as identifying one commit. This gate has no local git
// object database to run a real `git rev-parse --disambiguate` against (it
// sees only the PR's own head OID from `gh pr view` -- see Evaluate's own
// doc comment); a length floor is the fallback uniqueness check the issue
// asks for. Git's own porcelain refuses anything under 4, and GitHub's UI
// never prints fewer than 7 -- 7 is the floor here too, so a trailer this
// gate accepts is one any human reading the PR would also recognise as
// unambiguous.
const minReviewedSHAPrefix = 7

// reviewedSHAStatus classifies a reviewer's Reviewed-SHA trailer against the
// PR's actual head OID. It is deliberately three-valued, not a bool: a
// trailer that is a real prefix of a DIFFERENT, older commit is "stale" (the
// reviewer genuinely reviewed something else); a trailer too short to name
// one commit is "ambiguous" (this gate cannot tell what it names at all);
// only an exact match or an unambiguous prefix of the CURRENT head is a
// match. Conflating the last two is agent-estate#1107 -- a correct
// abbreviated review reported as if it were a stale one.
type reviewedSHAStatus int

const (
	shaMatches reviewedSHAStatus = iota
	shaStale
	shaAmbiguous
)

func classifyReviewedSHA(reviewed, head string) reviewedSHAStatus {
	reviewed = strings.ToLower(strings.TrimSpace(reviewed))
	head = strings.ToLower(head)
	if reviewed == head {
		return shaMatches
	}
	if len(reviewed) < minReviewedSHAPrefix {
		return shaAmbiguous
	}
	if len(reviewed) < len(head) && strings.HasPrefix(head, reviewed) {
		return shaMatches
	}
	return shaStale
}

// Evaluate is the whole gate. repo and pr identify the pull request; every
// other fact it needs -- who authored the work it closes, whether the
// reviewer actually reviewed, and what they said -- is derived from GitHub
// and the ledger, never from a caller-supplied argument.
func Evaluate(repo string, pr int, reviewerLane string, l *ledger.Ledger) Decision {
	p, err := fetch(repo, pr)
	if err != nil {
		return Decision{Allow: false, Reasons: []string{"could not read the PR: " + err.Error()}}
	}
	return evaluate(p, reviewerLane, l)
}

// evaluate is Evaluate's whole decision logic, taking an already-fetched PR
// so tests can drive it against a fixture without a gh/network dependency
// -- against the SAME function main.go and Evaluate call, not a
// reimplementation. A test exercising a copy of this logic instead of this
// function itself would not catch a real regression here; this seam is
// what lets gate_test.go's bypass mutations do that.
func evaluate(p *PR, reviewerLane string, l *ledger.Ledger) Decision {
	d := Decision{Allow: true, HeadOID: p.HeadOID}
	if !strings.EqualFold(p.State, "OPEN") {
		d.Allow = false
		d.Reasons = append(d.Reasons, "pull request is "+strings.ToLower(p.State))
	}
	if bad := checksGreen(p); len(bad) > 0 {
		d.Allow = false
		d.Reasons = append(d.Reasons, bad...)
	}

	if strings.TrimSpace(reviewerLane) == "" {
		return refuse(d, "reviewer lane not supplied -- cannot establish independence")
	}

	// Identity comes from the PR's own head ref, never a caller argument
	// (constraint 6) and never a closing issue a PR body asserts about
	// itself. See package doc's condition 2 and agent-estate#940: a head
	// ref is written by internal/isolate.Create, not by the agent and not
	// by whoever opened the PR, so a PR whose head is not a dispatch
	// branch carries no evidence the estate itself produced -- refused
	// outright, with no fallback to the old issue-keyed path.
	dispatchID, isDispatchBranch := authorFromHeadRef(p.HeadRefName)
	if !isDispatchBranch {
		return refuse(d, fmt.Sprintf("PR #%d head ref %q is not a dispatch branch -- authorship cannot be established structurally, refusing (agent-estate#940)", p.Number, p.HeadRefName))
	}
	authorRec, ok, err := authorRecordForDispatchID(l, dispatchID)
	if err != nil {
		return refuse(d, "cannot read ledger for authorship: "+err.Error())
	}
	if !ok {
		return refuse(d, "no completed role=author ledger record on file for dispatch id "+dispatchID+" (from head ref "+p.HeadRefName+") -- authorship unknown, refusing")
	}

	// The branch NAME matching a ledger id is not, by itself, evidence the
	// PR carries that dispatch's actual work -- every lane here shares one
	// GitHub login, so pushing different content to a branch named
	// "dispatch/<someone-else's-id>" costs no more than writing PR body
	// text (agent-estate#940's follow-up review, which found exactly this
	// hole in the branch-name-only join and blocked #952 over it). HeadSHA
	// is the estate's OWN observation -- read by main.go directly from the
	// worktree the moment that dispatch's subprocess exited, never
	// anything the subprocess claimed -- of which commit that specific
	// turn actually produced. Requiring an exact match binds the PR's
	// CODE, not merely a label, to the dispatch id. A record with no
	// HeadSHA (never observed, or the observation itself failed) refuses
	// rather than being treated as a pass: "could not measure" is not
	// "fine" anywhere else in this gate, and it is not fine here either.
	if authorRec.HeadSHA == "" {
		return refuse(d, "role=author record for dispatch id "+dispatchID+" carries no recorded HeadSHA -- cannot confirm this PR's head commit against what that dispatch's worktree actually produced, refusing")
	}

	authorLane := authorRec.Lane
	// chainLanes is every lane the gate has structurally verified produced
	// SOME commit between the root dispatch's own output and the PR's
	// current head -- the root itself, always, plus one entry per walked
	// fix-pass hop. This is the set an Author-Lane: trailer is checked
	// against below, not just the chain-terminal authorLane: a trailer
	// written when the PR was FIRST opened necessarily names the root, and
	// staying correct after a fix pass moves the terminal author elsewhere
	// does not make the root's own claim false.
	chainLanes := map[string]bool{normaliseLaneID(authorLane): true}
	if authorRec.HeadSHA != p.HeadOID {
		// The root dispatch's own commit is not the PR's current head. That
		// used to be an immediate refusal -- and still is, UNLESS a chain of
		// completed fix-pass dispatches (condition 2c) accounts for every
		// commit between the root's HeadSHA and the PR's actual head. See
		// the package doc for exactly what this does and does not prove.
		fp, hops, fok, ferr := authorRecordForFixPassChain(l, p.Number, authorRec.HeadSHA, p.HeadOID)
		if ferr != nil {
			return refuse(d, "cannot read ledger for fix-pass chain: "+ferr.Error())
		}
		if !fok {
			return refuse(d, "PR #"+strconv.Itoa(p.Number)+"'s head commit "+short(p.HeadOID)+" does not match the HeadSHA "+short(authorRec.HeadSHA)+
				" the estate recorded for dispatch id "+dispatchID+", and no completed fix-pass dispatch for PR #"+strconv.Itoa(p.Number)+
				" chains from that commit to the current head either -- the branch name matches but the code it points at does not, refusing")
		}
		authorLane = fp.Lane
		for _, hop := range hops {
			chainLanes[normaliseLaneID(hop.Lane)] = true
		}
	}

	// A PR body may carry a self-declared Author-Lane: trailer (repo
	// convention, AGENTS.md/CLAUDE.md). It is never trusted to ESTABLISH
	// identity -- the head ref above already did that structurally -- but
	// if one is present and names a lane OUTSIDE the verified chain, that is
	// exactly the shape of a forged trailer, the same class of hole
	// agent-estate#934 closed for Review-Lane:, and is refused rather than
	// silently ignored. A trailer naming ANY hop the gate has itself walked
	// -- the root dispatch that opened the PR, or a later fix pass that
	// moved the head -- is accepted: both are genuinely "the lane that
	// authored this PR", just at different points in its history
	// (agent-estate#940's "the gate's Author-Lane: check over-refuses every
	// fix-passed PR"). This does NOT loosen the chain walk itself -- a
	// trailer can only ever match a lane the Base/HeadSHA walk above already
	// verified produced code in this exact PR's history.
	//
	// A trailer may ALSO carry the "<session>:" qualifier AGENTS.md
	// Invariant 9 documents as the identity form ("<session>:<index>") --
	// stripping it before comparing is normalisation of a documented
	// SYNONYM for the same lane, never a widening of what is accepted:
	// stripSessionQualifier's own doc comment is where the exact-match, no-
	// substring guarantee lives (agent-estate#1067, following #1008's
	// "agent-supervisor:1006-...-28789-1" trailer for a chain root the gate
	// itself named "1006-...-28789-1"). A trailer that matches only after
	// stripping a qualifier is accepted exactly like an unqualified match --
	// there is nothing further to distinguish once the lane part checks out.
	if claimed, has := parseAuthorLaneTrailer(p.Body); has {
		unqualified := normaliseLaneID(claimed)
		qualified := normaliseLaneID(stripSessionQualifier(claimed))
		if !chainLanes[unqualified] && !chainLanes[qualified] {
			return refuse(d, "PR body's Author-Lane: trailer (\""+claimed+"\") names a lane outside the verified author chain (root "+authorRec.Lane+
				", current head authored by \""+authorLane+"\") -- refusing")
		}
	}

	if authorLane == reviewerLane {
		return refuse(d, "reviewer lane "+reviewerLane+" is also the dispatch that authored this PR's own head branch -- self-review")
	}

	rec, ok, err := reviewerRecord(l, p.Number, reviewerLane)
	if err != nil {
		return refuse(d, "cannot read ledger for reviewer completion: "+err.Error())
	}
	if !ok {
		return refuse(d, "reviewer lane "+reviewerLane+" has no completed role=reviewer turn on record for PR #"+strconv.Itoa(p.Number)+" -- a dispatched review that never finished is not independence")
	}
	reviewedAt := rec.At

	lv := resolveLaneVerdict(p.Comments, reviewerLane)
	if !lv.found {
		return refuse(d, lv.reason)
	}
	if !lv.ok {
		return refuse(d, "reviewer "+reviewerLane+"'s own verdict comment is unresolved -- "+lv.reason)
	}

	// Cross-check against the SECOND, independent source: the ledger's own
	// Result field for this exact reviewer turn, written locally by the
	// dispatch process from the reviewer subprocess's own output -- never
	// from anything a GitHub comment asserts about itself. A comment anyone
	// with the shared login can post is not, by itself, proof of what the
	// reviewer actually concluded (agent-estate#934); this record is the
	// one thing a forged comment cannot also forge. Unreadable, unparsable,
	// or disagreeing with the PR comment are each their own refusal --
	// "cannot tell" is never "allowed", same as every other check here.
	rv := resolveResultVerdict(rec.Result)
	if !rv.found {
		return refuse(d, "reviewer "+reviewerLane+"'s ledger record carries no parsable Verdict: line in its own Result -- cannot cross-check the PR comment against it; the reviewing turn's own final returned text must repeat the same Verdict:/Review-Lane: block it posted as a PR comment, refusing")
	}
	if !rv.ok {
		return refuse(d, "reviewer "+reviewerLane+"'s ledger Result verdict is unresolved -- "+rv.reason)
	}
	if rv.decision != lv.decision {
		return refuse(d, "reviewer "+reviewerLane+"'s PR comment says "+string(lv.decision)+" but the ledger's own record of that turn says "+string(rv.decision)+" -- the two independent sources disagree, refusing")
	}

	if lv.decision != verdictApproved {
		return refuse(d, "reviewer "+reviewerLane+"'s own verdict comment is "+string(lv.decision)+", not an approval")
	}
	if lv.hasSHA {
		switch classifyReviewedSHA(lv.reviewedSHA, p.HeadOID) {
		case shaStale:
			return refuse(d, "reviewer "+reviewerLane+"'s Reviewed-SHA "+lv.reviewedSHA+" does not match current head "+p.HeadOID+" -- stale, does not count")
		case shaAmbiguous:
			return refuse(d, "reviewer "+reviewerLane+"'s Reviewed-SHA "+lv.reviewedSHA+" is too short ("+strconv.Itoa(len(strings.TrimSpace(lv.reviewedSHA)))+" chars, need "+strconv.Itoa(minReviewedSHAPrefix)+") to identify a single commit against current head "+p.HeadOID+" -- refusing rather than guessing")
		}
	}

	if earliest, hasEarliest := earliestCheckStart(p); hasEarliest && reviewedAt.Before(earliest) {
		return refuse(d, "reviewer "+reviewerLane+" completed at "+reviewedAt.Format(time.RFC3339)+
			", before the current head's checks started at "+earliest.Format(time.RFC3339)+
			" -- reviewed against stale code")
	}

	return d
}

func refuse(d Decision, reason string) Decision {
	d.Allow = false
	d.Reasons = append(d.Reasons, reason)
	return d
}
