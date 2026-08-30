// Command mergepr is the ONLY path a lane or an operator should use to
// merge a PR in jonhill90/agent-tui -- so the gates in internal/mergepr
// cannot be skipped by habit, the same role agent-supervisor's own
// scripts/supervisor/merge-pr.sh plays for that repo (see
// internal/mergepr's own doc comment for the two gates it chains).
//
// This exists because agent-tui#109 recorded two confirmed instances of
// the same anti-pattern -- a comment-verdict gate merged by its own
// author, unreviewed, within minutes (skills#255, agent-tui#107) -- and a
// tool nobody is told to use is exactly how agent-tui#107 happened. AGENTS.md's own
// "Merging PRs you did not author" section says to run cmd/prverdict
// before merging; this command is what actually enforces that, plus the
// CI gate agent-supervisor's own history (PR agent-supervisor#56, agent-supervisor#49) shows is needed
// alongside it.
//
//	go run ./cmd/mergepr -repo jonhill90/agent-tui -number 123
//	go run ./cmd/mergepr -repo jonhill90/agent-tui -number 123 -- --squash --delete-branch
//
// Exit 0   both gates passed and `gh pr merge` ran (its own exit code,
//
//	which on success is 0).
//
// Exit 1   a gate refused, or `gh pr merge` itself failed. Nothing was
//
//	merged if a gate refused; the refusing gate's reason is printed
//	to stderr either way.
//
// Exit 2   usage error.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jonhill90/agent-estate/tui/internal/mergepr"
	"github.com/jonhill90/agent-estate/tui/internal/prverdict"
)

func main() {
	repo := flag.String("repo", "", "owner/name of the PR's repo (required)")
	number := flag.Int("number", 0, "PR number (required)")
	ghBin := flag.String("gh-bin", envOr("AGENT_GH_BIN", "gh"), "gh binary to shell out to")
	flag.Parse()

	if *repo == "" || *number == 0 {
		fmt.Fprintln(os.Stderr, "mergepr: -repo and -number are both required")
		os.Exit(2)
	}

	// Anything after the flags (conventionally following a `--`) is
	// passed through to `gh pr merge` verbatim -- e.g. --squash,
	// --delete-branch -- so this command never picks a merge strategy on
	// the caller's behalf, same as merge-pr.sh's own extra-args handling.
	extraArgs := flag.Args()

	run := prverdict.ExecRunner(*ghBin)
	result, out, err := mergepr.Merge(run, mergepr.ExecGHMerger, *ghBin, *repo, *number, extraArgs)

	fmt.Fprintf(os.Stderr, "mergepr: CI gate -- %s\n", result.CI.Reason)
	if result.Verdict.Detail != "" {
		fmt.Fprintf(os.Stderr, "mergepr: verdict gate -- %s\n", result.Verdict.Detail)
	}

	if result.Decision != mergepr.MergeAllow {
		fmt.Fprintf(os.Stderr, "mergepr: refused -- %s\n", result.Reason)
		os.Exit(1)
	}

	if out != "" {
		fmt.Println(out)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "mergepr: gh pr merge failed -- %s\n", err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr, "mergepr: merged")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
