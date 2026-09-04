package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/status"
)

// The outside-world half of `estate status`. It lives here, next to
// newResolver and the tick-record `produced` closure, for the same reason
// those do: internal/status must stay a pure deriver that tests with no git
// checkout, no gh and no network, so everything needing os/exec is supplied
// to it as a function.

// mergedPRRE matches the pull request number a squash-merge commit subject
// carries: "feat(estate): … (#1003)". This is how the repo's own history
// records which pull request a commit came from, and it is the only join
// available -- a commit does not otherwise name its PR.
var mergedPRRE = regexp.MustCompile(`\(#(\d+)\)`)

// mainRef picks which ref to read main's history from, preferring the remote
// -- a local `main` can sit behind origin for days, and answering "is #914 on
// main" from a stale local ref is exactly the drift this command exists to
// remove. Which ref was actually used is returned so the report can say so
// rather than leaving the reader to assume.
func mainRef() (string, error) {
	for _, ref := range []string{"origin/main", "main"} {
		if exec.Command("git", "rev-parse", "--verify", "--quiet", ref).Run() == nil {
			return ref, nil
		}
	}
	return "", fmt.Errorf("neither origin/main nor main resolves in this checkout")
}

// mergedPRs returns every pull request number main's own history carries.
//
// An error here must make every phase UNKNOWN, never NOT ON MAIN: "I could
// not read main" and "these pull requests are not on main" are different
// facts and only one of them is about the work.
func mergedPRs() (map[int]bool, string, error) {
	ref, err := mainRef()
	if err != nil {
		return nil, "", err
	}
	out, err := exec.Command("git", "log", "--format=%s", ref).Output()
	if err != nil {
		return nil, ref, fmt.Errorf("git log %s: %w", ref, err)
	}
	prs := map[int]bool{}
	for _, m := range mergedPRRE.FindAllStringSubmatch(string(out), -1) {
		if n, cerr := strconv.Atoi(m[1]); cerr == nil {
			prs[n] = true
		}
	}
	return prs, ref, nil
}

// forgeItem is the shape both `gh pr list` and `gh issue list` are asked for.
type forgeItem struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	CreatedAt string `json:"createdAt"`
	IsDraft   bool   `json:"isDraft"`
}

// ghList runs one `gh <kind> list` and decodes it. A failure is returned as
// an error and never as an empty slice: reporting "no open pull requests"
// because gh was unreachable is the fail-open direction this repo refuses
// everywhere else.
func ghList(kind string, fields []string) ([]status.Item, error) {
	args := []string{kind, "list", "--state", "open", "--limit", "200", "--json", strings.Join(fields, ",")}
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("gh %s list: %w", kind, err)
	}
	var raw []forgeItem
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("gh %s list returned output that could not be decoded: %w", kind, err)
	}
	items := make([]status.Item, 0, len(raw))
	for _, r := range raw {
		it := status.Item{Number: r.Number, Title: r.Title, Draft: r.IsDraft}
		// A createdAt that will not parse leaves the zero time, which Render
		// prints as "age unknown" -- never as "created just now".
		if t, perr := time.Parse(time.RFC3339, r.CreatedAt); perr == nil {
			it.CreatedAt = t
		}
		items = append(items, it)
	}
	return items, nil
}

func openPRs() ([]status.Item, error) {
	return ghList("pr", []string{"number", "title", "createdAt", "isDraft"})
}

func openIssues() ([]status.Item, error) {
	return ghList("issue", []string{"number", "title", "createdAt"})
}
