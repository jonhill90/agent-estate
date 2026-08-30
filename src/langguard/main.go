// Command langguard enforces the implementation-language rule.
//
// The app is Go. Shell and Python exist in this repo only as reference
// material under reference/ -- code an agent may read to recover a rule the
// old supervisor encoded. They are never the implementation path.
//
// This exists because writing the rule down did not hold it. The directive
// "the supervisor is Go" was recorded on 2026-08-22, its named target was
// later archived, and the rule silently stopped binding while the shell layer
// grew for another week. A rule nothing checks is a preference.
//
// Exit 1 on violation, with the offending paths named.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Paths where shell and Python are permitted, and why.
var allowed = map[string]string{
	"reference/": "read-only reference material recovered from the deleted supervisor",
	".github/":   "CI workflow glue, which GitHub Actions requires",
	".claude/":   "Claude Code hook glue, which the harness invokes directly",
}

func tracked() ([]string, error) {
	out, err := exec.Command("git", "ls-files").Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n"), nil
}

func permitted(p string) bool {
	for prefix := range allowed {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func main() {
	files, err := tracked()
	if err != nil {
		fmt.Fprintln(os.Stderr, "langguard: could not list tracked files:", err)
		os.Exit(2) // could not measure is not the same as clean
	}
	var bad []string
	for _, f := range files {
		switch filepath.Ext(f) {
		case ".sh", ".py":
			if !permitted(f) {
				bad = append(bad, f)
			}
		}
	}
	if len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "langguard: %d shell/Python file(s) outside the permitted paths.\n", len(bad))
		fmt.Fprintln(os.Stderr, "The app is Go. These are implementation, not reference:")
		for _, f := range bad {
			fmt.Fprintln(os.Stderr, "  "+f)
		}
		fmt.Fprintln(os.Stderr, "\nPermitted paths:")
		for p, why := range allowed {
			fmt.Fprintf(os.Stderr, "  %-12s %s\n", p, why)
		}
		os.Exit(1)
	}
	fmt.Printf("langguard: ok -- %d tracked files, no shell or Python outside reference/\n", len(files))
}
