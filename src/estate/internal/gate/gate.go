// Package gate decides whether a pull request may merge.
//
// Two independent conditions, both of which must pass, both failing closed:
//
//  1. Every required check is green AT THE HEAD SHA. Not "green somewhere" --
//     a check that passed on an earlier push says nothing about what is being
//     merged now.
//  2. The reviewer is not the author. The old supervisor lost a task row on
//     cancel, could no longer say who wrote a PR, and approved a lane to
//     review its own work.
//
// Anything unresolved -- an unknown author, a pending check, an unreadable
// ledger -- is a REFUSAL. "Cannot tell" is never "allowed".
package gate

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type PR struct {
	Number  int     `json:"number"`
	HeadOID string  `json:"headRefOid"`
	State   string  `json:"state"`
	Checks  []Check `json:"statusCheckRollup"`
}

type Decision struct {
	Allow   bool
	Reasons []string
	HeadOID string
}

func fetch(repo string, pr int) (*PR, error) {
	out, err := exec.Command("gh", "pr", "view", fmt.Sprint(pr), "-R", repo,
		"--json", "number,headRefOid,state,statusCheckRollup").Output()
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

// independent reports whether the reviewing lane differs from the authoring
// lane. Either being unknown is a refusal, not a pass.
// independent reports whether the reviewing lane differs from every lane that
// authored work on the issue.
//
// The first version filtered the reviewer OUT of the author candidates
// (`r.Lane != reviewerLane`) before asking whether the author WAS the
// reviewer, so the self-review branch was unreachable dead code and a lane
// that had worked an issue alongside another lane could approve its own PR --
// the exact failure this package exists to prevent. The test passed because it
// only ever hit the unknown-author branch and asserted that SOME refusal came
// back, not which one.
// approved reports whether a review turn's own report says APPROVE.
//
// A council seat found the gate never read what the review SAID: a turn that
// returned REQUEST CHANGES satisfied it exactly like an approval, because
// `reviewed` was set from the record's existence and not its content. The
// negative readings are checked first so "I would APPROVE but VERDICT:
// REQUEST CHANGES" is read correctly, and anything unparseable is NOT an
// approval -- a review nobody can read has not approved anything.
func approved(report string) bool {
	// The verdict must be a LINE of its own, not a substring anywhere in the
	// prose. A council seat found the substring form read a refusal as an
	// approval whenever the refusal quoted a previous round -- and every
	// council comment in this repo quotes verdicts, in its seat table. The
	// first verdict line wins, so a report that opens by refusing cannot be
	// rescued by a later quotation.
	for _, raw := range strings.Split(report, "\n") {
		line := strings.ToUpper(strings.TrimSpace(raw))
		if !strings.HasPrefix(line, "VERDICT:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "VERDICT:"))
		switch {
		case strings.HasPrefix(v, "REQUEST CHANGES"), strings.HasPrefix(v, "COULD NOT DETERMINE"):
			return false
		case strings.HasPrefix(v, "APPROVE"):
			return true
		}
	}
	return false
}

// independent is independentAt with no head time, used where the head's age
// is unknown. Prefer independentAt.
func independent(l *ledger.Ledger, issue, reviewerLane string) []string {
	return independentAt(l, issue, reviewerLane, time.Time{}, true)
}

// independentAt adds headAt: the moment the commit being merged was created.
// A review recorded BEFORE that reviewed something else.
func independentAt(l *ledger.Ledger, issue, reviewerLane string, headAt time.Time, headKnown bool) []string {
	if strings.TrimSpace(reviewerLane) == "" {
		return []string{"reviewer lane not supplied -- cannot establish independence"}
	}
	cur, err := l.Current()
	if err != nil {
		return []string{"cannot read ledger: " + err.Error()}
	}
	authors := map[string]bool{}
	// derived records whether ANY authorship came from a dispatched turn.
	//
	// `estate authored` lets a caller write any lane as the author of any
	// issue. A council found the consequence: name a decoy as author, pass
	// your own lane as reviewer, and the gate says "may merge" -- laundering
	// a self-merge as an independent one.
	//
	// So the two kinds of record are not equal. Authorship from a dispatched
	// turn is EVIDENCE: the estate ran that turn and recorded it. Authorship
	// written by hand is an ASSERTION, and an assertion by the party who
	// benefits is not a basis for permitting anything. It still NARROWS who
	// may review -- catching self-review -- it just cannot WIDEN who may
	// merge.
	derived := false
	// reviewed records whether the lane offered as reviewer actually has a
	// COMPLETED review turn on this issue.
	//
	// Round two of the council: the reviewer lane was an unverified string
	// supplied by the caller. The gate only checked it differed from the
	// authors, so an author could invent any name -- "some-other-lane" --
	// and get "may merge". RoleReview was being written and never required,
	// which made the independence check theatre.
	reviewed := false
	reviewNotApproved := false
	reviewStale := false
	for _, r := range cur {
		// A lane dispatched to REVIEW an issue is not one of its authors.
		// Without this the reviewer is counted as having worked the issue and
		// every review is refused as self-review -- which is what happened the
		// first time a review was dispatched against the issue it reviewed.
		if r.Role == ledger.RoleReview {
			// A review that failed, timed out, or is still running has not
			// reviewed anything; counting it would let a crashed seat satisfy
			// the gate.
			if r.Issue == issue && r.Lane == reviewerLane && r.State == ledger.Complete {
				switch {
				case !approved(r.Result):
					reviewNotApproved = true
				case headKnown && r.At.Before(headAt):
					// The review ran before this head existed.
					reviewStale = true
				default:
					reviewed = true
				}
			}
			continue
		}
		if r.Issue == issue && r.Lane != "" {
			authors[r.Lane] = true
			// Only a turn that actually finished is authorship evidence. A
			// failed or unobserved turn proves nothing about who wrote the
			// work.
			if r.State == ledger.Complete {
				derived = true
			}
		}
	}
	if len(authors) == 0 {
		return []string{"no authoring lane on record for issue " + issue + " -- authorship unknown, refusing"}
	}
	if authors[reviewerLane] {
		return []string{"reviewer lane " + reviewerLane + " also authored work on issue " + issue + " -- self-review"}
	}
	if !headKnown {
		return []string{"could not read when the head commit of issue " + issue +
			" was created, so a review cannot be shown to have seen it -- refusing rather than assuming"}
	}
	if !reviewed {
		switch {
		case reviewStale:
			return []string{"reviewer lane " + reviewerLane + " reviewed issue " + issue +
				" before the current head existed -- that review saw different code"}
		case reviewNotApproved:
			return []string{"reviewer lane " + reviewerLane + "'s review of issue " + issue +
				" does not say APPROVE -- a review that refused, could not determine, or cannot be read is not an approval"}
		default:
			return []string{"reviewer lane " + reviewerLane + " has no completed review on record for issue " + issue +
				" -- an unverified reviewer name cannot establish independence"}
		}
	}
	if !derived {
		return []string{"authorship for issue " + issue + " is asserted by `estate authored`, not derived from a dispatched turn -- " +
			"an assertion by the party it benefits cannot establish independence. This is for a human to merge"}
	}
	return nil
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func Evaluate(repo string, pr int, reviewerLane string, issue string, l *ledger.Ledger) Decision {
	d := Decision{Allow: true}
	p, err := fetch(repo, pr)
	if err != nil {
		return Decision{Allow: false, Reasons: []string{"could not read the PR: " + err.Error()}}
	}
	d.HeadOID = p.HeadOID
	if !strings.EqualFold(p.State, "OPEN") {
		d.Allow = false
		d.Reasons = append(d.Reasons, "pull request is "+strings.ToLower(p.State))
	}
	if bad := checksGreen(p); len(bad) > 0 {
		d.Allow = false
		d.Reasons = append(d.Reasons, bad...)
	}
	headAt, headKnown := headSeenAt(repo, p.HeadOID)
	if bad := independentAt(l, issue, reviewerLane, headAt, headKnown); len(bad) > 0 {
		d.Allow = false
		d.Reasons = append(d.Reasons, bad...)
	}
	return d
}

// headSeenAt returns when GitHub first SAW the head commit, and whether that
// could be read at all.
//
// It deliberately does not use the commit's committer date: a council seat
// pointed out that GIT_COMMITTER_DATE is set by whoever makes the commit, so
// a head can claim to predate a review that never saw it. A check run's
// startedAt is stamped by GitHub when the commit arrives, and the gate
// already requires checks to exist and be green, so it is available for free
// and is not the author's to choose.
//
// A time that cannot be read returns false. The caller refuses on that:
// every other limit in this package fails closed, and this one used to be
// the exception.
func headSeenAt(repo, oid string) (time.Time, bool) {
	if oid == "" {
		return time.Time{}, false
	}
	out, err := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/commits/%s/check-runs", repo, oid),
		"-q", "[.check_runs[].started_at] | sort | .[0]").Output()
	if err != nil {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}
