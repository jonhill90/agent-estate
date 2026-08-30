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
func independent(l *ledger.Ledger, issue, reviewerLane string) []string {
	if strings.TrimSpace(reviewerLane) == "" {
		return []string{"reviewer lane not supplied -- cannot establish independence"}
	}
	cur, err := l.Current()
	if err != nil {
		return []string{"cannot read ledger: " + err.Error()}
	}
	var author string
	for _, r := range cur {
		if r.Issue == issue && r.Lane != "" && r.Lane != reviewerLane {
			author = r.Lane
		}
	}
	if author == "" {
		return []string{"no authoring lane on record for issue " + issue + " -- authorship unknown, refusing"}
	}
	if author == reviewerLane {
		return []string{"reviewer lane " + reviewerLane + " is also the author -- self-review"}
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
	if bad := independent(l, issue, reviewerLane); len(bad) > 0 {
		d.Allow = false
		d.Reasons = append(d.Reasons, bad...)
	}
	return d
}
