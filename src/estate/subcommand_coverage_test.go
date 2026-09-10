package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// WHY THIS EXISTS, AND WHY IT IS SHAPED LIKE THIS. agent-estate#1359:
// TestAgentsMDNamesOnlyRealSubcommands (this file's neighbour) catches
// confabulation -- a doc naming a command that does not exist -- but nothing
// catches the opposite drift: a real command that no doc names at all.
// docs/product/SPEC.md understated the system in three separate, verified
// ways (a missing `--harness=` flag on the Dispatch heading, two entirely
// unmentioned subcommands, a missing internal package) because nothing
// bound it to the tree the way AGENTS.md is bound.
//
// THE SHAPE I DID NOT PICK, AND WHY. #1359's own proposal was "every real
// subcommand must appear in AGENTS.md or SPEC.md, or sit on an explicit
// exemption list" -- and its own filing said not to assume that is right.
// Measured before building anything: of the 15 top-level subcommands in
// main.go's switch, 6 (features, inflight, reclaim, tasks, toolusage,
// verify-branch) name no doc at all, even after this same PR's SPEC.md
// corrections closed two of the previously-missing ones (corpus-audit,
// candidates). A bare exemption list seeded at 6-of-15 (40%) on day one,
// with nothing stopping a future author from adding a 7th with zero
// justification, is exactly the shape docs/historical/ci-rules-retired.md
// describes dying: a check that trains a reflexive workaround "measures
// nothing" the moment adding to the list costs less than fixing the doc.
//
// So this test does not accept a bare name. Every exemption is a
// (subcommand -> reason) pair, and the reason must be non-empty --
// modelled directly on this repo's own retired `gh-comment-gate.sh`
// (docs/historical/ci-rules-retired.md): "pre-existing exceptions
// grandfathered by exact line, not by file, so editing that line has to
// re-earn the exemption." Adding subcommand #7 to undocumentedSubcommands
// below costs a sentence, reviewed in the diff, not a free pass.
//
// It also checks the list in BOTH directions: an exemption for a
// subcommand that is now documented (or no longer exists) is dead weight,
// caught here rather than accumulating forever -- the same "does this
// exemption still earn its place" question #1359's own exemption-list
// framing raises, made concrete.
var undocumentedSubcommands = map[string]string{
	"features":      "meta-tooling reporting on the feature-completion ledger itself, not an operator-facing contract",
	"inflight":      "thin ledger query (tasks still occupying a slot), paired with `tasks` under one case label",
	"reclaim":       "internal safety mechanism (agent-estate#1194): reports and frees stranded in-flight slots, not yet given a contract section",
	"tasks":         "thin ledger query (latest state of every task), paired with `inflight` under one case label",
	"toolusage":     "diagnostic/reporting command over a completed turn's tool calls, not an operator-facing contract",
	"verify-branch": "CI-adjacent build/test helper for a branch in its own tree, not an operator-facing contract",
}

// docNamesSubcommand reports whether doc mentions `estate <name>` as a whole
// token -- word-boundaried so "dispatch" does not match inside "dispatched"
// and a hyphenated name like "corpus-audit" is matched exactly, not as a
// prefix of some other command. `\s+` rather than a literal space between
// "estate" and the name: prose in these docs wraps at ~80 columns, and
// whether "estate" and its subcommand happen to land on the same line is a
// typesetting accident, not a claim about whether the command is
// documented.
func docNamesSubcommand(doc, name string) bool {
	pat := regexp.MustCompile(`\bestate\s+` + regexp.QuoteMeta(name) + `\b`)
	return pat.MatchString(doc)
}

// TestEveryRealSubcommandIsDocumentedOrExempted is the missing direction:
// AGENTS.md's own test (TestAgentsMDNamesOnlyRealSubcommands) refuses a doc
// naming something unreal; this refuses the tree containing something
// undocumented AND unexplained. Together they bind the doc to the tree in
// both directions, closing the gap AGENTS.md's own header already names in
// its two binding tests: "they would pass prose that described every
// command doing the wrong thing" -- this does not fix that (still no prose
// checking), but it does fix "a command nobody wrote a word about."
func TestEveryRealSubcommandIsDocumentedOrExempted(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("cannot read main.go, so nothing was verified: %v", err)
	}
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("cannot read AGENTS.md, so nothing was verified: %v", err)
	}
	spec, err := os.ReadFile(filepath.Join(root, "docs", "product", "SPEC.md"))
	if err != nil {
		t.Fatalf("cannot read docs/product/SPEC.md, so nothing was verified: %v", err)
	}

	real := map[string]bool{}
	for _, m := range caseRE.FindAllStringSubmatch(string(src), -1) {
		for _, lbl := range strings.Split(m[1], ", ") {
			real[strings.Trim(lbl, `"`)] = true
		}
	}
	if len(real) == 0 {
		t.Fatal("found no subcommands in main.go; the instrument is broken, not the docs")
	}

	// Every reason must be real -- a bare name with an empty string is the
	// exact workaround this test exists to refuse.
	for name, reason := range undocumentedSubcommands {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("undocumentedSubcommands[%q] has an empty reason -- an exemption with no justification is a bare name wearing a map", name)
		}
	}

	var missing []string
	for name := range real {
		documented := docNamesSubcommand(string(agents), name) || docNamesSubcommand(string(spec), name)
		_, exempted := undocumentedSubcommands[name]
		if !documented && !exempted {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("estate subcommand(s) named in neither AGENTS.md nor docs/product/SPEC.md, and not in undocumentedSubcommands: %s\n"+
			"either document one, or add it to undocumentedSubcommands with a real reason",
			strings.Join(missing, ", "))
	}

	// The other direction: an exemption that no longer earns its place.
	var stale []string
	for name := range undocumentedSubcommands {
		if !real[name] {
			stale = append(stale, name+" (no longer a real subcommand)")
			continue
		}
		if docNamesSubcommand(string(agents), name) || docNamesSubcommand(string(spec), name) {
			stale = append(stale, name+" (now documented -- remove the exemption)")
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("undocumentedSubcommands carries stale entries: %s", strings.Join(stale, ", "))
	}
}
