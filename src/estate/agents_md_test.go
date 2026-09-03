package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// WHY THIS TEST EXISTS. Three consecutive review rounds on one pull request
// each found the same defect: AGENTS.md naming something that was real in the
// author's working tree and absent from the branch. A file
// (frame_capture_test.go), then two subcommands (`estate tick`, `estate
// authored`). Each fix introduced the next instance, because the author kept
// checking the claim against the checkout in front of them rather than the
// tree the change actually lands in.
//
// A reviewer catching that three times is not a process. This is the check.
// It runs in CI, against the branch's own tree, where a working-copy-only
// command simply does not exist.
//
// Scope is deliberately narrow: it verifies that every `estate <subcommand>`
// named in AGENTS.md is a real case in this file's own switch. It does not
// try to validate prose. A narrow check that runs beats a broad one that
// cannot.

var (
	// Matches an `estate <subcommand>` mention inside backticks, which is how
	// AGENTS.md refers to commands throughout.
	claimRE = regexp.MustCompile("`estate ([a-z][a-z-]*)")
	// Matches the case labels of main's dispatch switch, including the
	// multi-label form `case "tasks", "inflight":`.
	caseRE = regexp.MustCompile(`(?m)^\tcase (\"[a-z-]+\"(?:, \"[a-z-]+\")*):`)
	// Words that follow "estate" in prose without naming a subcommand.
	notSubcommands = map[string]bool{
		"is": true, "the": true, "and": true, "as": true, "loop": true,
		"pressure-": true, "repo": true, "runs": true, "will": true,
	}
)

func TestAgentsMDNamesOnlyRealSubcommands(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(root, "AGENTS.md")
	doc, err := os.ReadFile(docPath)
	if err != nil {
		// Refuse rather than pass: a check that cannot find its subject has
		// not verified anything, and must not report success.
		t.Fatalf("cannot read %s, so nothing was verified: %v", docPath, err)
	}
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go, so nothing was verified: %v", err)
	}

	real := map[string]bool{}
	for _, m := range caseRE.FindAllStringSubmatch(string(src), -1) {
		for _, lbl := range strings.Split(m[1], ", ") {
			real[strings.Trim(lbl, `"`)] = true
		}
	}
	if len(real) == 0 {
		t.Fatal("found no subcommands in main.go; the instrument is broken, not the document")
	}

	bad := map[string]bool{}
	for _, m := range claimRE.FindAllStringSubmatch(string(doc), -1) {
		sub := m[1]
		if notSubcommands[sub] || real[sub] {
			continue
		}
		bad[sub] = true
	}
	if len(bad) > 0 {
		var names []string
		for s := range bad {
			names = append(names, s)
		}
		sort.Strings(names)
		var have []string
		for s := range real {
			have = append(have, s)
		}
		sort.Strings(have)
		t.Fatalf("AGENTS.md names estate subcommand(s) that do not exist in this tree: %s\n"+
			"main.go has: %s\n"+
			"If the command is real on another branch, it is not real here -- say so, or do not name it.",
			strings.Join(names, ", "), strings.Join(have, ", "))
	}
}
