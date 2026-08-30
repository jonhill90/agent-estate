// Command prverdict is the merge-time gate for a PR this lane did not
// author: it prints the internal/prverdict decision as JSON and exits by
// DECISION, not by whether the read itself succeeded, mirroring
// jonhill90/skills#255's pr_verdict.py CLI contract exactly (0 approved,
// 1 rejected, 2 none, 3 unknown) -- a caller distinguishes "go ahead" from
// every other outcome by exit code alone, without parsing the JSON.
//
// Not wired into CI, deliberately, same reasoning as skills#255's own
// AGENTS.md note: this repo's CI (.github/workflows/ci.yml) builds, vets
// and tests every push and PR, but it never merges one -- merging is
// always a separate `gh pr merge` an operator or an agent lane runs
// directly, outside any workflow. There is no merge-time CI job to attach
// this gate to without inventing one that does not otherwise exist; this
// binary is the check that `gh pr merge` invocation must run first, by
// convention stated here (and in AGENTS.md's "Merging PRs you did not
// author" section), the same way skills' own pr_verdict.py is wired into
// its callers by convention rather than a workflow step.
//
//	go run ./cmd/prverdict -repo jonhill90/agent-tui -number 123
//	echo $?   # 0 only if it is genuinely safe to merge
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/jonhill90/agent-estate/tui/internal/prverdict"
)

func main() {
	repo := flag.String("repo", "", "owner/name of the PR's repo (required)")
	number := flag.Int("number", 0, "PR number (required)")
	ghBin := flag.String("gh-bin", envOr("AGENT_GH_BIN", "gh"), "gh binary to shell out to")
	flag.Parse()

	if *repo == "" || *number == 0 {
		fmt.Fprintln(os.Stderr, "prverdict: -repo and -number are both required")
		os.Exit(prverdict.Unknown.ExitCode())
	}

	run := prverdict.ExecRunner(*ghBin)
	payload, err := prverdict.Fetch(run, *repo, *number)
	if err != nil {
		result := prverdict.Result{Decision: prverdict.Unknown, Detail: err.Error()}
		emit(result)
		os.Exit(result.Decision.ExitCode())
	}

	result := prverdict.Resolve(payload)
	emit(result)
	os.Exit(result.Decision.ExitCode())
}

func emit(result prverdict.Result) {
	out, err := json.Marshal(map[string]string{
		"decision": string(result.Decision),
		"detail":   result.Detail,
	})
	if err != nil {
		// json.Marshal on a map[string]string cannot fail; kept as a
		// checked error only because every other error return in this
		// package is checked -- see internal/prverdict's own doc comment
		// on fail-closed posture.
		fmt.Fprintln(os.Stderr, "prverdict: internal error encoding result:", err)
		return
	}
	fmt.Println(string(out))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
