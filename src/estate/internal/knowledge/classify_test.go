package knowledge

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// classifiedSourceNames parses classify.go's own AST and returns every
// string literal named in a `case` clause of the switch inside func
// classify, in source order, excluding the default clause. This is how
// TestClassifyPublishableSetIsExactlyGithubStarsAndRepoDocs derives the
// source set instead of hand-listing it -- see that test's own comment for
// why a hand-listed set cannot catch a genuinely new case name.
func classifiedSourceNames(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "classify.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing classify.go: %v", err)
	}

	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "classify" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			for _, stmt := range sw.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok || clause.List == nil { // nil List is the default clause
					continue
				}
				for _, expr := range clause.List {
					lit, ok := expr.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					unquoted, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("unquoting case literal %s: %v", lit.Value, err)
					}
					names = append(names, unquoted)
				}
			}
			return false
		})
		return false
	})

	if len(names) == 0 {
		t.Fatal("classifiedSourceNames found no case clauses in classify() -- parser is broken or classify.go's shape changed under it")
	}
	return names
}

// TestClassifyDefaultsUnknownSourcesPrivate is classify's own contract in
// isolation: any source name it has not positively allow-listed comes back
// Publishable=false with a non-empty basis -- never true, never a blank
// reason a reader has to guess at. This includes a source name that does
// not exist yet, on purpose: a new source added tomorrow with no
// classify.go change is private by construction, not by someone
// remembering to add a case here. loops-research and vault-fact are
// deliberately NOT in this list as of agent-estate#1059 -- they have their
// own explicit table entries now (see
// TestClassifyLoopsResearchAndVaultFactAreExplicitNotDefault below) and
// this test must exercise only sources still relying on the fallthrough,
// or a future edit to the default branch could satisfy this test while
// silently changing their basis string.
func TestClassifyDefaultsUnknownSourcesPrivate(t *testing.T) {
	for _, source := range []string{"corpus-parameter", "some-future-source"} {
		publishable, basis := classify(source)
		if publishable {
			t.Errorf("classify(%q) = publishable=true, want false (unclassified means private)", source)
		}
		if basis == "" {
			t.Errorf("classify(%q) gave no basis for its verdict", source)
		}
	}
}

// TestClassifyLoopsResearchAndVaultFactAreExplicitNotDefault is the test
// agent-estate#1059 exists for. Before this change, both sources were
// private only because they fell through to classify's default branch --
// an accident of an unrelated rule, not a stated decision about either
// source. This test pins each one to its OWN table entry, distinguishable
// from the default branch's basis string, so that widening the default
// branch to publishable (a plausible future edit -- see the default test
// above) cannot silently flip these two along with it.
//
// To confirm this test is load-bearing rather than vacuous: temporarily
// change classify's default case to `return true, "default now public"`
// and run this test -- TestClassifyDefaultsUnknownSourcesPrivate fails (as
// expected, that test covers the default branch), and this test PASSES
// unchanged, because loops-research and vault-fact never reach the
// default branch. That is the property being asserted: their privacy does
// not derive from the default. See the PR body for that run's output.
func TestClassifyLoopsResearchAndVaultFactAreExplicitNotDefault(t *testing.T) {
	const defaultBasisSuffix = "source defaults to private -- no per-item publishability marker exists yet (agent-estate#1028)"

	for _, source := range []string{"loops-research", "vault-fact"} {
		publishable, basis := classify(source)
		if publishable {
			t.Errorf("classify(%q) = publishable=true, want false", source)
		}
		if basis == "" {
			t.Errorf("classify(%q) gave no basis for its verdict", source)
			continue
		}
		if strings.Contains(basis, defaultBasisSuffix) {
			t.Errorf("classify(%q) basis %q still reads as the default fallthrough -- want its own explicit table entry (agent-estate#1059)", source, basis)
		}
		if !strings.Contains(basis, "agent-estate#1059") {
			t.Errorf("classify(%q) basis %q does not cite agent-estate#1059 -- want evidence this is a stated decision, not a default", source, basis)
		}
	}
}

// TestClassifyPublishableSetIsExactlyGithubStarsAndRepoDocs replaces
// TestClassifyGithubStarsIsTheOnlyPublicSource (agent-estate#1180). That
// test's name claimed github-stars was the ONLY public source, but its
// body only ever called classify("github-stars") and asserted it came
// back true -- it never enumerated the rest of the switch, so the name
// was an assertion the test never made. It was true when written; #1034
// made repo-docs publishable too and nobody updated the name or the body,
// so a third `return true, ...` case added to classify's switch tomorrow
// would pass this test just as silently as repo-docs did.
//
// agent-estate#1180 offered two fixes: rename the test to describe only
// what it checks, or make the name true by asserting the complete
// publishable set. This is (2) -- the stronger form, chosen deliberately
// rather than defaulted into, for a reason worth stating here rather than
// leaving implicit: knowledgeGrounding() (src/estate/main.go,
// agent-estate#1177) ships a claim to every dispatched lane that
// public-mode source: scoping is structurally inert for repo-oriented
// questions, because exactly two sources are publishable, so the filter
// can only ever remove github-stars items -- github-stars has never
// outranked repo-docs on a repo question (0 of 21 golden cases differ
// between public unscoped and public scoped, agent-estate#1166). That
// claim depends on the publishable set staying at exactly {github-stars,
// repo-docs}. A third public source could change the outranking and make
// the shipped claim false without any code here changing -- this test is
// the thing that has to fail the moment classify's switch grows a third
// publishable case, per the "debt travels with the check" pattern
// (agent-estate#1066), rather than leaving that claim unguarded the way
// choice (1) would have.
//
// This does not duplicate TestClassifyDefaultsUnknownSourcesPrivate: that
// test is classify's contract for the *unclassified* branch in isolation.
// This test's job is the explicitly classified set as a whole -- it would
// catch a third *named* case added to the switch, which the default test
// cannot, since a named case never reaches the default branch.
//
// agent-estate#1180 RESIDUAL, found by a follow-up audit: the fix above
// still iterated a hand-written allKnownSources list. That caught a
// LISTED source flipping publishable (e.g. changing vault-fact's case to
// return true), but a genuinely NEW case name -- someone adds
// `case "slack-archive": return true, ...` to classify.go tomorrow --
// was never in the list, so it was never called, and the test passed
// unchanged. The doc comment above claims this test "would catch a third
// named case added to the switch" -- true only for a name already in the
// list, which is exactly the gap a new case exploits.
//
// The fix here is choice (1) from that audit, not (2): derive the source
// set from classify.go's own AST (classifiedSourceNames, above) instead
// of hand-listing it, so the enumeration cannot go stale independently of
// the switch it describes. Every case name the switch actually contains
// is looked up in wantPublic below; a name with no entry fails loudly
// (via the `ok` check) naming itself, rather than silently never being
// asked. Choice (2) -- narrowing the doc comment to admit the gap -- was
// rejected: it leaves the blind spot open, and knowledgeGrounding()
// (src/estate/main.go, agent-estate#1177) ships a claim to every
// dispatched lane that depends on the publishable set staying exactly
// {github-stars, repo-docs} (see the long comment above). A guard that
// cannot see a new source at all is worse than one with a slightly wrong
// name; this repo has already recorded that a check nothing can fail is
// not a check (see the "count-agents.sh" lesson in this repo's own
// CLAUDE.md) -- the same reasoning applies to a check that CAN fail but
// structurally never sees the input that would make it.
//
// This still cannot see everything: classifiedSourceNames only reads
// string-literal case labels from classify()'s switch. A rewrite of
// classify() to something other than a string switch (a map lookup, a
// helper function, a build tag) would make classifiedSourceNames find
// zero cases and fail loudly at the `len(names) == 0` check above, rather
// than silently passing vacuously -- that failure is the intended
// fallback, not a gap, since it forces whoever changes classify()'s shape
// to update this test's derivation too.
//
// To confirm this guard actually fires rather than passing vacuously, two
// mutations, both demonstrated in the PR body for this change:
//  1. temporarily add a third publishable case under a NEW name, e.g.
//     `case "slack-archive-TEMP": return true, "..."` -- this test fails,
//     naming "slack-archive-TEMP" as classified true with no wantPublic
//     entry. This is the exact case the pre-#1180-residual version of
//     this test missed.
//  2. temporarily flip an existing listed case's boolean, e.g. change
//     vault-fact's case to `return true, "..."` -- this test fails,
//     naming vault-fact's mismatch (the case the original 1180 fix
//     already covered).
//
// Revert both and the test passes again.
func TestClassifyPublishableSetIsExactlyGithubStarsAndRepoDocs(t *testing.T) {
	wantPublic := map[string]bool{
		"github-stars":   true,
		"repo-docs":      true,
		"loops-research": false,
		"vault-fact":     false,
	}

	for _, source := range classifiedSourceNames(t) {
		want, ok := wantPublic[source]
		if !ok {
			t.Errorf("classify.go's switch has a case %q with no expectation in this test's wantPublic map -- a new source was added to classify() without updating this guard (agent-estate#1180)", source)
			continue
		}
		publishable, basis := classify(source)
		if publishable != want {
			t.Errorf("classify(%q) = publishable=%v, want %v -- the publishable set must stay exactly {github-stars, repo-docs} (agent-estate#1180)", source, publishable, want)
		}
		if basis == "" {
			t.Errorf("classify(%q) gave no basis for its verdict", source)
		}
	}

	// Also assert the reverse direction: every source this test expects
	// to exist was actually found in the switch. Without this, deleting a
	// case from classify.go (e.g. removing vault-fact's explicit entry)
	// would silently shrink classifiedSourceNames' output and this test
	// would just check less, not fail.
	found := map[string]bool{}
	for _, source := range classifiedSourceNames(t) {
		found[source] = true
	}
	for source := range wantPublic {
		if !found[source] {
			t.Errorf("wantPublic expects a case %q but classify.go's switch no longer has it", source)
		}
	}
}

// TestGenerateNeverPublishesAnItemWithNoBasis is the end-to-end guard
// #1028 asks for, run over a real Generate() call across all four
// sources: every item in the Result must carry a non-empty PublishBasis,
// and Publishable=true must never appear without one. This is the test to
// break and watch fail -- flip classify's default branch in classify.go to
// `return true, ""` (unclassified source, no reason) and this test must
// fail; restore it and this test must pass again. See the PR body for
// both runs.
func TestGenerateNeverPublishesAnItemWithNoBasis(t *testing.T) {
	dir := t.TempDir()
	loopsDir := filepath.Join(dir, "loops")
	if err := os.MkdirAll(loopsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loopsDir, "a.md"), []byte("# A\n\npara\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeVaultFact(t, dir, "fact", fixtureVaultFact)

	cfg := Config{
		VaultDir:      dir,
		CorpusDBPath:  filepath.Join(dir, "absent.sqlite3"), // fails, exercises the SourceResult path only
		LoopsResearch: loopsDir,
		RunGH: func(args ...string) ([]byte, error) {
			return []byte(`{"full_name":"a/one","html_url":"https://github.com/a/one"}` + "\n"), nil
		},
	}
	res := Generate(cfg, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	if len(res.Items) == 0 {
		t.Fatal("test setup produced no items -- guard below would pass vacuously")
	}

	sawPublic, sawPrivate := false, false
	for _, it := range res.Items {
		if it.PublishBasis == "" {
			t.Fatalf("item %s (%s) has Publishable=%v with no PublishBasis", it.ID, it.Source, it.Publishable)
		}
		if it.Publishable {
			sawPublic = true
			if it.Source != "github-stars" {
				t.Errorf("item %s (%s) is Publishable=true, but only github-stars is classified public today", it.ID, it.Source)
			}
		} else {
			sawPrivate = true
		}
	}
	if !sawPublic || !sawPrivate {
		t.Fatalf("test setup did not exercise both classes: sawPublic=%v sawPrivate=%v", sawPublic, sawPrivate)
	}
}
