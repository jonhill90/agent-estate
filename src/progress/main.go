// Command progress answers the only question that matters: did the app move?
//
// For a month the estate produced 236 commits against the shell supervisor and
// 13 against the app, and nobody noticed because nothing measured it. Merged
// PRs, closed issues and swept worktrees all rose steadily while the product
// stood still. This makes that ratio impossible to miss.
//
// Exit 1 when app work is a minority of the window's commits, so it can gate a
// weekly report rather than sit in a dashboard nobody opens.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Paths that constitute the product. Everything else is scaffolding: useful,
// sometimes necessary, never progress on its own.
var appPaths = []string{"src/tui", "src/estate"}

// Scaffolding worth counting separately so the ratio is visible.
var supportPaths = []string{"reference", ".github", "docs", "src/langguard", "src/notify", "src/issuemine", "src/progress"}

func count(since string, paths []string) (int, error) {
	args := append([]string{"log", "--since=" + since, "--oneline", "--"}, paths...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return 0, fmt.Errorf("git log %v: %w", paths, err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, nil
	}
	return len(strings.Split(s, "\n")), nil
}

func main() {
	since := "2 weeks ago"
	if len(os.Args) > 1 {
		since = strings.Join(os.Args[1:], " ")
	}

	app, err := count(since, appPaths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "progress: could not measure app commits:", err)
		os.Exit(2) // could not measure is not the same as progress
	}
	sup, err := count(since, supportPaths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "progress: could not measure support commits:", err)
		os.Exit(2)
	}

	total := app + sup
	fmt.Printf("since %s\n", since)
	fmt.Printf("  app        %3d  (%s)\n", app, strings.Join(appPaths, ", "))
	fmt.Printf("  scaffolding%3d\n", sup)
	if total == 0 {
		fmt.Println("\nno commits in this window -- nothing shipped, and that is the finding")
		os.Exit(1)
	}
	pct := float64(app) / float64(total) * 100
	fmt.Printf("  app share  %.0f%%\n", pct)

	if app == 0 {
		fmt.Println("\nZERO app commits. Everything in this window was scaffolding.")
		os.Exit(1)
	}
	if pct < 50 {
		ratio := strconv.FormatFloat(float64(sup)/float64(app), 'f', 1, 64)
		fmt.Printf("\n%s scaffolding commits per app commit. The product is not the thing being worked on.\n", ratio)
		os.Exit(1)
	}
	fmt.Println("\napp work is the majority of this window")
}
