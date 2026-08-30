package prverdict

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// Runner executes `gh` with the given args and returns its stdout -- the
// same seam shape as internal/board.GitHubRunner, kept as its own type
// rather than reused directly so this package never imports internal/board
// for an unrelated feature (agent-tui#28's own board/gallery split makes
// the same call: adjacent seams, not a shared one, when the two features
// are otherwise unconnected).
type Runner func(args []string) ([]byte, error)

// execTimeout bounds every ExecRunner invocation -- mirrors
// internal/board's own execTimeout and its doc comment's reasoning: an
// unbounded `gh` call (a network stall, an SSO prompt with no stdin to
// answer it) must not hang whatever invoked this package forever. A
// package var, not a literal, so a test can shrink it.
var execTimeout = 15 * time.Second

// ExecRunner shells `gh` out via os/exec -- the real implementation.
// prverdict is not under internal/board or internal/session, so this is
// not the tmux-avoidance rule this repo's AGENTS.md states (that rule is
// specifically "never os/exec for tmux from internal/*"); it is the same
// gh-calling pattern internal/board.ExecRunner already uses for exactly
// this reason -- gh's own CLI has no library form, so every seam that
// needs it shells out once, behind a Runner, the same way board's does.
func ExecRunner(name string) Runner {
	return func(args []string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, name, args...).Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return nil, fmt.Errorf("%s %v: %w: %s", name, args, err, string(exitErr.Stderr))
			}
			return nil, fmt.Errorf("%s %v: %w", name, args, err)
		}
		return out, nil
	}
}

// prView is the subset of `gh pr view --json body,headRefOid,comments`'s
// own output shape this package reads.
type prView struct {
	Body       string `json:"body"`
	HeadRefOid string `json:"headRefOid"`
	Comments   []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		Body string `json:"body"`
	} `json:"comments"`
}

// Fetch builds a Payload for repo/number by running `gh pr view` through
// run -- the one function in this package that knows the gh CLI's JSON
// shape; Resolve (verdict.go) never sees it.
func Fetch(run Runner, repo string, number int) (Payload, error) {
	out, err := run([]string{
		"pr", "view", fmt.Sprintf("%d", number),
		"--repo", repo,
		"--json", "body,headRefOid,comments",
	})
	if err != nil {
		return Payload{}, fmt.Errorf("prverdict: gh pr view %s#%d: %w", repo, number, err)
	}

	var view prView
	if err := json.Unmarshal(out, &view); err != nil {
		return Payload{}, fmt.Errorf("prverdict: decode gh pr view %s#%d: %w", repo, number, err)
	}

	payload := Payload{Body: view.Body, HeadSHA: view.HeadRefOid}
	for _, c := range view.Comments {
		payload.Comments = append(payload.Comments, Comment{Author: c.Author.Login, Body: c.Body})
	}
	return payload, nil
}
