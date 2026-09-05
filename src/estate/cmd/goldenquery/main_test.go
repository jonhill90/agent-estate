package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge/goldenset"
)

const sampleMatchedStdout = `2 match(es) for "auth tokens" (showing 2, 0 not returned)

[20260904055010] vault-fact (score 2: auth, tokens)
  auth token rotation -- rotate every 90 days
  /vault/agent/facts/auth-token-rotation.md

[20260904055011] corpus-parameter (score 1: auth)
  auth must use short-lived tokens
  corpus:item:it-abc123

ranking: score = count of distinct question terms ...
ask ` + "`estate knowledge get <id>`" + ` for one item's full tier2/tier3
`

const sampleNoMatchStdout = `no item matches "quokka platypus giraffe zeppelin"
`

func TestParseMatchesRecoversIDSourceAndPermalink(t *testing.T) {
	got := parseMatches(sampleMatchedStdout)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].ID != "20260904055010" || got[0].Source != "vault-fact" || got[0].Score != 2 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[0].Permalink != "/vault/agent/facts/auth-token-rotation.md" {
		t.Errorf("got[0].Permalink = %q", got[0].Permalink)
	}
	if got[1].Permalink != "corpus:item:it-abc123" {
		t.Errorf("got[1].Permalink = %q", got[1].Permalink)
	}
}

func TestEvaluatePassesWhenExpectedIdentifierInTop3(t *testing.T) {
	c := goldenset.Case{
		ID: "t1", ExpectedSource: goldenset.SourceVaultFact,
		ExpectedIdentifier: "auth-token-rotation.md",
	}
	r := evaluate(c, sampleMatchedStdout, 0)
	if !r.pass {
		t.Fatalf("evaluate() pass = false, detail=%q", r.detail)
	}
}

func TestEvaluateFailsWhenExpectedIdentifierMissing(t *testing.T) {
	c := goldenset.Case{
		ID: "t2", ExpectedSource: goldenset.SourceVaultFact,
		ExpectedIdentifier: "never-appears.md",
	}
	r := evaluate(c, sampleMatchedStdout, 0)
	if r.pass {
		t.Fatal("evaluate() pass = true for an identifier that never appeared")
	}
}

func TestEvaluateFailsWhenExitCodeIsNotZeroForARealCase(t *testing.T) {
	c := goldenset.Case{
		ID: "t3", ExpectedSource: goldenset.SourceVaultFact,
		ExpectedIdentifier: "auth-token-rotation.md",
	}
	r := evaluate(c, "", 2)
	if r.pass {
		t.Fatal("evaluate() pass = true for exit code 2 (index_missing/unreadable)")
	}
}

func TestEvaluatePassesNoMatchCaseOnGenuineNoMatch(t *testing.T) {
	c := goldenset.Case{ID: "n1", ExpectedSource: goldenset.SourceNone}
	r := evaluate(c, sampleNoMatchStdout, 1)
	if !r.pass {
		t.Fatalf("evaluate() pass = false for a genuine no_match, detail=%q", r.detail)
	}
}

func TestEvaluateFailsNoMatchCaseWhenSomethingActuallyMatched(t *testing.T) {
	c := goldenset.Case{ID: "n2", ExpectedSource: goldenset.SourceNone}
	r := evaluate(c, sampleMatchedStdout, 0)
	if r.pass {
		t.Fatal("evaluate() pass = true for a no_match case that actually matched something")
	}
}

// agent-estate#1073: firstMatchRank is the natural-language stratum's own
// primitive -- it must find a match at any rank in the returned list, not
// only within a top-3 slice, so both the top-3 and top-10 numbers can be
// derived from one query per case.

func TestFirstMatchRankFindsRankBeyondThree(t *testing.T) {
	c := goldenset.Case{ID: "t4", ExpectedSource: goldenset.SourceCorpusParameter, ExpectedIdentifier: "it-abc123"}
	matches := parseMatches(sampleMatchedStdout)
	if got := firstMatchRank(c, matches); got != 2 {
		t.Fatalf("firstMatchRank() = %d, want 2", got)
	}
}

func TestFirstMatchRankZeroWhenNeverReturned(t *testing.T) {
	c := goldenset.Case{ID: "t5", ExpectedSource: goldenset.SourceVaultFact, ExpectedIdentifier: "never-appears.md"}
	matches := parseMatches(sampleMatchedStdout)
	if got := firstMatchRank(c, matches); got != 0 {
		t.Fatalf("firstMatchRank() = %d, want 0", got)
	}
}

// agent-estate#1077: scopedQuestion is the natural-language stratum's
// scoped-run primitive -- these two cases fail against the parent commit
// (df2cf75/3449f91), where the function does not exist at all, and pass
// once it's added.

func TestScopedQuestionLeavesQuestionUnchangedWhenUnscoped(t *testing.T) {
	got := scopedQuestion("how do I merge a pull request in this repo", "repo-docs", false)
	want := "how do I merge a pull request in this repo"
	if got != want {
		t.Fatalf("scopedQuestion(unscoped) = %q, want %q", got, want)
	}
}

func TestScopedQuestionPrependsSourceRepoDocsWhenScoped(t *testing.T) {
	got := scopedQuestion("how do I merge a pull request in this repo", "repo-docs", true)
	want := "source:repo-docs how do I merge a pull request in this repo"
	if got != want {
		t.Fatalf("scopedQuestion(scoped) = %q, want %q", got, want)
	}
}

// agent-estate#1111: scopedQuestion's source parameter is what lets the
// same primitive serve the github-stars stratum, not just repo-docs --
// this case fails against the parent commit, where the parameter does not
// exist and the tag is hardcoded to "repo-docs".

func TestScopedQuestionPrependsGivenSourceWhenScoped(t *testing.T) {
	got := scopedQuestion("which starred repo does X", "github-stars", true)
	want := "source:github-stars which starred repo does X"
	if got != want {
		t.Fatalf("scopedQuestion(scoped, github-stars) = %q, want %q", got, want)
	}
}

// agent-estate#1133: isPublishableReachable is the reachable-set test the
// corrected "publishable-reachable score" line is built on. github-stars
// and repo-docs are the only two sources classify.go marks safe to
// default public (internal/knowledge/classify.go); every other
// ExpectedSource -- including "none" -- is excluded from the reachable
// score, never counted as a reachable miss.

func TestIsPublishableReachableTrueForGithubStarsAndRepoDocs(t *testing.T) {
	for _, s := range []goldenset.ExpectedSource{goldenset.SourceGithubStars, goldenset.SourceRepoDocs} {
		if !isPublishableReachable(s) {
			t.Errorf("isPublishableReachable(%q) = false, want true", s)
		}
	}
}

func TestIsPublishableReachableFalseForPrivateSourcesAndNone(t *testing.T) {
	for _, s := range []goldenset.ExpectedSource{
		goldenset.SourceVaultFact, goldenset.SourceCorpusParameter,
		goldenset.SourceLoopsResearch, goldenset.SourceNone,
	} {
		if isPublishableReachable(s) {
			t.Errorf("isPublishableReachable(%q) = true, want false", s)
		}
	}
}

// agent-estate#1133: splitPublishable is what turns one default-mode run
// over all 17 of cases.json's cases into the reachable score's own
// numerator/denominator plus the two counts the line's own text must
// disclose (excludedPrivate, and none-01 reported separately) -- this is
// the fix for the bug the issue reports: before this, the aggregate
// hits/total this function replaces counted all 17 in the denominator,
// reporting "5/17" for a run that found every reachable case there was to
// find.

func TestSplitPublishableExcludesPrivateSourcesFromReachableDenominator(t *testing.T) {
	results := []result{
		{c: goldenset.Case{ID: "v1", ExpectedSource: goldenset.SourceVaultFact}, pass: false},
		{c: goldenset.Case{ID: "c1", ExpectedSource: goldenset.SourceCorpusParameter}, pass: false},
		{c: goldenset.Case{ID: "l1", ExpectedSource: goldenset.SourceLoopsResearch}, pass: false},
		{c: goldenset.Case{ID: "g1", ExpectedSource: goldenset.SourceGithubStars}, pass: true},
		{c: goldenset.Case{ID: "r1", ExpectedSource: goldenset.SourceRepoDocs}, pass: true},
		{c: goldenset.Case{ID: "n1", ExpectedSource: goldenset.SourceNone}, pass: false},
	}
	hits, total, excludedPrivate, none := splitPublishable(results)
	if hits != 2 || total != 2 {
		t.Fatalf("splitPublishable() hits/total = %d/%d, want 2/2 (only github-stars and repo-docs count)", hits, total)
	}
	if excludedPrivate != 3 {
		t.Fatalf("splitPublishable() excludedPrivate = %d, want 3 (vault-fact, corpus-parameter, loops-research)", excludedPrivate)
	}
	if none == nil || none.c.ID != "n1" {
		t.Fatalf("splitPublishable() none = %+v, want the SourceNone case (n1)", none)
	}
}

// agent-estate#1066: buildRatchets/ratchetFailures are the regression-ratchet
// primitives -- a ratcheted line that drops below its recorded floor must
// be reported as a failure, and every ratchet's reason must be non-empty so
// the accepted-cost text this issue requires can never silently go missing.
//
// agent-estate#1152: ratchet is now defined by maxMisses (total-got vs a
// miss budget), not a hardcoded hit floor -- floor() derives the printed
// floor from maxMisses and the CURRENT total, so these tests exercise the
// miss-budget comparison directly rather than a stored minimum.

func TestRatchetOkAtOrAboveFloor(t *testing.T) {
	r := ratchet{name: "x", got: 6, total: 12, maxMisses: 6}
	if !r.ok() {
		t.Fatal("ratchet.ok() = false at exactly the floor, want true")
	}
	if r.floor() != 6 {
		t.Fatalf("ratchet.floor() = %d, want 6", r.floor())
	}
	r.got = 7
	if !r.ok() {
		t.Fatal("ratchet.ok() = false above the floor, want true")
	}
}

func TestRatchetFailsBelowFloor(t *testing.T) {
	r := ratchet{name: "x", got: 5, total: 12, maxMisses: 6}
	if r.ok() {
		t.Fatal("ratchet.ok() = true below the floor, want false")
	}
}

// TestRatchetFloorIsDenominatorIndependent is agent-estate#1152's own
// regression test: a hardcoded hit floor tolerated MORE misses every time a
// passing case was added to the stratum (the exact defect agent-estate#1150
// exposed against the private retrieval score, 17/17 floor-16 -> 22/22
// floor-16, tolerating 1 miss and then 6). Adding a passing case to a
// maxMisses-based ratchet must never change how many misses it tolerates.
func TestRatchetFloorIsDenominatorIndependent(t *testing.T) {
	before := ratchet{name: "x", got: 16, total: 17, maxMisses: 1}
	if !before.ok() {
		t.Fatal("ratchet.ok() = false at 16/17 with maxMisses 1, want true (exactly 1 miss)")
	}
	// Five new cases land, all passing -- total and got both grow by 5, the
	// same shape as agent-estate#1150's five camelcase-01..05 additions.
	after := ratchet{name: "x", got: 21, total: 22, maxMisses: 1}
	if !after.ok() {
		t.Fatal("ratchet.ok() = false at 21/22 with maxMisses 1, want true (still exactly 1 miss)")
	}
	// The regression this guards: if a SIXTH case had been added as a MISS
	// instead of a hit, the old hardcoded-floor-16 ratchet would still read
	// [OK] (21 >= 16) even though the stratum lost ground. The maxMisses
	// ratchet must catch it.
	regressed := ratchet{name: "x", got: 20, total: 22, maxMisses: 1}
	if regressed.ok() {
		t.Fatal("ratchet.ok() = true at 20/22 with maxMisses 1 (2 misses), want false")
	}
}

// TestBuildRatchetsReasonsStateTheirOwnMissBudget is agent-estate#1155's
// finding, closed for this file: a reason that names an accepted cost only
// in prose ("floor at the value measured on add887e") reads as true forever
// and is never checked against the number actually enforced. This mirrors
// agent-estate#1121's TestRankingBasisNamesLiveFieldWeights -- pin the
// number the reason claims to the number the ratchet actually enforces
// (r.maxMisses), not the surrounding sentence, so changing one without the
// other fails the build.
func TestBuildRatchetsReasonsStateTheirOwnMissBudget(t *testing.T) {
	none := &result{c: goldenset.Case{ID: "none-01", ExpectedSource: goldenset.SourceNone}, pass: true, exitCode: 1}
	for _, r := range buildRatchets(4, 12, 4, 12, 16, 17, 5, 5, 7, 7, 8, none) {
		want := fmt.Sprintf("at most %d miss(es)", r.maxMisses)
		if !strings.Contains(r.reason, want) {
			t.Errorf("ratchet %q reason = %q, does not contain %q -- agent-estate#1155 requires the reason state the exact number it enforces", r.name, r.reason, want)
		}
	}
}

func TestBuildRatchetsEveryEntryCarriesAReason(t *testing.T) {
	none := &result{c: goldenset.Case{ID: "none-01", ExpectedSource: goldenset.SourceNone}, pass: true, exitCode: 1}
	rs := buildRatchets(4, 12, 4, 12, 16, 17, 5, 5, 7, 7, 8, none)
	if len(rs) == 0 {
		t.Fatal("buildRatchets() returned no ratchets")
	}
	for _, r := range rs {
		if r.reason == "" {
			t.Errorf("ratchet %q has no reason -- agent-estate#1066 requires the accepted-cost reason travel with the check itself", r.name)
		}
	}
}

// TestBuildRatchetsMaxMissesArePinned closes the gap TestBuildRatchetsReasonsStateTheirOwnMissBudget
// left open: that test only checks the reason string against r.maxMisses,
// which is formatted FROM the same constant it is meant to guard -- a
// six-way mutation of buildRatchets's six maxMisses constants (widen each
// by one, independently) showed only two of six caused any test to fail,
// and both of those failed incidentally (TestRatchetFailuresDetectsRegressionBelowFloor
// and TestBuildRatchetsNoneResultMustBeHitToPass are baseline/none-result
// checks, not a pin on the constant itself). This test hardcodes the
// agreed-on value for every named ratchet and fails the moment any one of
// the six constants in buildRatchets changes, whether tightened or
// loosened, so a widened budget cannot hide behind a reason string that
// updates itself to describe the new, looser number.
func TestBuildRatchetsMaxMissesArePinned(t *testing.T) {
	none := &result{c: goldenset.Case{ID: "none-01", ExpectedSource: goldenset.SourceNone}, pass: true, exitCode: 1}
	rs := buildRatchets(4, 12, 4, 12, 16, 17, 5, 5, 7, 7, 8, none)
	want := map[string]int{
		"natural-language stratum top-3, unscoped":                8,
		"natural-language stratum top-3, scoped source:repo-docs": 8,
		"retrieval score (private)":                               1,
		"publishable-reachable score":                             0,
		"github-stars stratum top-3":                              1,
		"github-stars stratum top-10":                             1,
		"none-01 (absence must report no_match)":                  0,
	}
	if len(rs) != len(want) {
		t.Fatalf("buildRatchets() returned %d ratchets, want %d -- update this test's want map to match", len(rs), len(want))
	}
	for _, r := range rs {
		wantMax, known := want[r.name]
		if !known {
			t.Fatalf("ratchet %q is not in this test's want map -- add its agreed-on maxMisses so a future change to it is pinned", r.name)
		}
		if r.maxMisses != wantMax {
			t.Errorf("ratchet %q maxMisses = %d, want %d -- a maxMisses constant in buildRatchets changed without updating this pin (agent-estate#1152 follow-up: a floor that only some ratchets pin is the same defect with a smaller number)", r.name, r.maxMisses, wantMax)
		}
	}
}

func TestBuildRatchetsAllPassAtRecordedBaselines(t *testing.T) {
	// The two natural-language top-3 values are 4, the agent-estate#1140
	// floor (lowered from add887e's 6 after nl-09/nl-11 were re-authored
	// from need); every other value here is still the add887e baseline.
	none := &result{c: goldenset.Case{ID: "none-01", ExpectedSource: goldenset.SourceNone}, pass: true, exitCode: 1}
	rs := buildRatchets(4, 12, 4, 12, 16, 17, 5, 5, 7, 7, 8, none)
	if failed := ratchetFailures(rs); len(failed) != 0 {
		t.Fatalf("ratchetFailures() at recorded baselines = %+v, want none", failed)
	}
}

func TestBuildRatchetsNoneResultMustBeHitToPass(t *testing.T) {
	miss := &result{c: goldenset.Case{ID: "none-01", ExpectedSource: goldenset.SourceNone}, pass: false, exitCode: 0}
	rs := buildRatchets(4, 12, 4, 12, 16, 17, 5, 5, 7, 7, 8, miss)
	failed := ratchetFailures(rs)
	if len(failed) != 1 {
		t.Fatalf("ratchetFailures() with a missed none-01 = %+v, want exactly 1 failure", failed)
	}
	if failed[0].name != "none-01 (absence must report no_match)" {
		t.Fatalf("ratchetFailures()[0].name = %q, want the none-01 ratchet", failed[0].name)
	}
}

func TestRatchetFailuresDetectsRegressionBelowFloor(t *testing.T) {
	// natural-language top-3 drops from the recorded (post-agent-estate#1140)
	// floor of 4/12 to 3/12 -- a genuine regression, not the known top-10
	// drift this ratchet deliberately excludes, and not the one-time,
	// already-accepted drop from 6 to 4 that #1140 itself produced.
	none := &result{c: goldenset.Case{ID: "none-01", ExpectedSource: goldenset.SourceNone}, pass: true, exitCode: 1}
	rs := buildRatchets(3, 12, 4, 12, 16, 17, 5, 5, 7, 7, 8, none)
	failed := ratchetFailures(rs)
	if len(failed) != 1 || failed[0].name != "natural-language stratum top-3, unscoped" {
		t.Fatalf("ratchetFailures() = %+v, want exactly the unscoped top-3 ratchet failing", failed)
	}
}

func TestRatchetFailuresPassesWhenNoneResultIsNil(t *testing.T) {
	// A checkout where cases.json has stopped carrying a SourceNone case at
	// all must not crash or spuriously fail this ratchet -- see
	// splitPublishable's own doc comment for the same "noneResult may be
	// nil" contract.
	rs := buildRatchets(4, 12, 4, 12, 16, 17, 5, 5, 7, 7, 8, nil)
	if failed := ratchetFailures(rs); len(failed) != 0 {
		t.Fatalf("ratchetFailures() with nil noneResult = %+v, want none", failed)
	}
}

func TestSplitPublishableReachableHitsMissAPrivateMiss(t *testing.T) {
	// A private-source case's own pass/fail must never leak into the
	// reachable score -- a vault-fact case failing in default mode (the
	// expected, correct outcome under disclosure policy) must not lower
	// the reachable score's own hit rate.
	results := []result{
		{c: goldenset.Case{ID: "v1", ExpectedSource: goldenset.SourceVaultFact}, pass: false},
		{c: goldenset.Case{ID: "g1", ExpectedSource: goldenset.SourceGithubStars}, pass: true},
	}
	hits, total, _, _ := splitPublishable(results)
	if hits != 1 || total != 1 {
		t.Fatalf("splitPublishable() hits/total = %d/%d, want 1/1 -- a private-source miss must not enter the reachable denominator", hits, total)
	}
}
