// Package gate decides whether a pull request may merge.
//
// Four independent conditions, all of which must pass, all failing closed:
//
//  1. The pull request is open, and every required check is green AT THE
//     HEAD SHA. Not "green somewhere" -- a check that passed on an earlier
//     push says nothing about what is being merged now.
//  2. A dispatched turn authored the work this PR closes, and the reviewer
//     lane is not among those authoring lanes. Authorship is read from
//     ledger records this estate itself wrote at dispatch time -- never
//     from a caller-supplied issue number, and never from anything a PR's
//     own body or comments assert about themselves. See agent-estate#926.
//  3. That same reviewer lane has a COMPLETED review turn on record for
//     THIS pull request. A dispatched-but-unfinished review is not
//     independence: nobody has actually looked yet.
//  4. The reviewer's own verdict comment on the PR resolves to APPROVE,
//     parsed from a Verdict: line -- never a substring match anywhere in
//     the body -- and is not stale against the checks that ran at head.
//
// Anything unresolved -- an unknown author, a pending check, an unreadable
// ledger, a PR that closes no issue GitHub can confirm -- is a REFUSAL.
// "Cannot tell" is never "allowed".
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
	State         string         `json:"state"`
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
// needs to establish identity (closingIssuesReferences), freshness (checks
// plus their own startedAt) and the reviewer's public verdict (comments)
// comes from here -- never from a caller argument, per constraint 6 of
// agent-estate#926: an author who could name any issue they liked used to
// be able to merge anything, using only record shapes `estate dispatch`
// writes itself.
func fetch(repo string, pr int) (*PR, error) {
	out, err := exec.Command("gh", "pr", "view", fmt.Sprint(pr), "-R", repo,
		"--json", "number,headRefOid,state,statusCheckRollup,closingIssuesReferences,comments").Output()
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

	// Identity comes from the PR itself, never a caller argument
	// (constraint 6). A PR GitHub reports as closing nothing is refused
	// rather than treated as authorless-and-fine: an unlinked PR gives the
	// gate no issue to check authorship against at all.
	if len(p.ClosingIssues) == 0 {
		return refuse(d, fmt.Sprintf("PR #%d closes no issue GitHub can confirm -- authorship cannot be established, refusing", p.Number))
	}
	issues := map[string]bool{}
	for _, ci := range p.ClosingIssues {
		issues[strconv.Itoa(ci.Number)] = true
	}

	authors, err := authorLanes(l, issues)
	if err != nil {
		return refuse(d, "cannot read ledger for authorship: "+err.Error())
	}
	if len(authors) == 0 {
		var names []string
		for n := range issues {
			names = append(names, "#"+n)
		}
		return refuse(d, "no authoring (role=author) lane on record for "+strings.Join(names, ", ")+" -- authorship unknown, refusing")
	}
	if authors[reviewerLane] {
		return refuse(d, "reviewer lane "+reviewerLane+" also authored work on an issue this PR closes -- self-review")
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
		return refuse(d, "reviewer "+reviewerLane+"'s ledger record carries no parsable Verdict: line in its own Result -- cannot cross-check the PR comment against it, refusing")
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
	if lv.hasSHA && lv.reviewedSHA != p.HeadOID {
		return refuse(d, "reviewer "+reviewerLane+"'s Reviewed-SHA "+lv.reviewedSHA+" does not match current head "+p.HeadOID+" -- stale, does not count")
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
