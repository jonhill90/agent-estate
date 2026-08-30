// Command issuemine distils the closed-issue history into a compact digest.
//
// The shell and Python supervisor was deleted, but the issues recorded against
// it hold hard-won invariants: guards that must fail closed, instruments that
// lie, orderings that matter. Those are worth carrying into the Go supervisor;
// the scripts they described are not.
//
// It shells out to `gh` for transport only. Selection and ranking happen here.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

type issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
}

// Signals that an issue records a durable rule rather than a one-off chore.
// Weighted: a phrase naming a failure direction is worth more than a mention.
var signals = []struct {
	re *regexp.Regexp
	w  int
}{
	{regexp.MustCompile(`(?i)fail(s|ed)? (closed|open)`), 5},
	{regexp.MustCompile(`(?i)must (not )?(refuse|block|never|always)`), 4},
	{regexp.MustCompile(`(?i)invariant|guard`), 3},
	{regexp.MustCompile(`(?i)mutation[- ]check|positive control`), 4},
	{regexp.MustCompile(`(?i)silently|reads as clean|looks identical`), 4},
	{regexp.MustCompile(`(?i)race|ordering|before .* runs`), 2},
	{regexp.MustCompile(`(?i)could not measure|blindness|instrument`), 3},
	{regexp.MustCompile(`(?i)data loss|lost work|destroyed`), 5},
	{regexp.MustCompile(`(?i)verified|measured|reproduced`), 1},
}

func score(i issue) int {
	t := i.Title + "\n" + i.Body
	n := 0
	for _, s := range signals {
		n += s.w * len(s.re.FindAllString(t, 4))
	}
	return n
}

func fetch(repo string) ([]issue, error) {
	cmd := exec.Command("gh", "issue", "list", "-R", repo,
		"--state", "closed", "--limit", "1000",
		"--json", "number,title,body,state")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh issue list %s: %w", repo, err)
	}
	var is []issue
	if err := json.Unmarshal(out, &is); err != nil {
		return nil, fmt.Errorf("decode %s: %w", repo, err)
	}
	return is, nil
}

func main() {
	repos := os.Args[1:]
	if len(repos) == 0 {
		repos = []string{"jonhill90/agent-estate"}
	}
	var all []issue
	for _, r := range repos {
		is, err := fetch(r)
		if err != nil {
			fmt.Fprintln(os.Stderr, "issuemine:", err)
			os.Exit(1)
		}
		all = append(all, is...)
	}
	sort.SliceStable(all, func(a, b int) bool { return score(all[a]) > score(all[b]) })

	kept := 0
	for _, i := range all {
		s := score(i)
		if s < 8 {
			continue
		}
		kept++
		body := strings.Join(strings.Fields(i.Body), " ")
		if len(body) > 600 {
			body = body[:600]
		}
		fmt.Printf("## %d [%d] %s\n%s\n\n", i.Number, s, i.Title, body)
	}
	fmt.Fprintf(os.Stderr, "issuemine: %d issues scanned, %d carry durable rules\n", len(all), kept)
}
