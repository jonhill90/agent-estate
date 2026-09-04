package goldenset

import (
	"strings"
	"testing"
)

// These tests check the github-stars stratum's own shape (#1111) --
// coverage, no duplicate ids, every case has what a case needs -- never
// whether a query against the real index scores well. That measurement is
// cmd/goldenquery's job, run against a live compiled index, not a `go test`
// fixture. Mirrors natural_cases_test.go's checks on natural_cases.json
// without editing that file, per #1073's own precedent for a separate
// stratum getting its own loader and its own shape tests.

func TestLoadStarsParsesEmbeddedCases(t *testing.T) {
	cases, err := LoadStars()
	if err != nil {
		t.Fatalf("LoadStars() error: %v", err)
	}
	if len(cases) != 8 {
		t.Fatalf("len(cases) = %d, want 8 -- agent-estate#1063's own measured eight-case set", len(cases))
	}
}

func TestEveryStarCaseHasQuestionAndRationale(t *testing.T) {
	cases, err := LoadStars()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.ID == "" {
			t.Errorf("case with empty ID: %+v", c)
		}
		if seen[c.ID] {
			t.Errorf("duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
		if c.Question == "" {
			t.Errorf("case %s has no question", c.ID)
		}
		if c.Rationale == "" {
			t.Errorf("case %s has no rationale for why its expected identifier is authoritative", c.ID)
		}
		if c.ExpectedSource != SourceNone && c.ExpectedIdentifier == "" {
			t.Errorf("case %s expects a real source but has no expected_identifier", c.ID)
		}
		if c.ExpectedSource == SourceNone && c.ExpectedIdentifier != "" {
			t.Errorf("case %s expects no_match but carries an expected_identifier %q", c.ID, c.ExpectedIdentifier)
		}
	}
}

// TestStarCasesAreAllGithubStars documents the stratum's own scope: #1111
// is the github-stars natural-language stratum only, the complement of
// #1073's repo-docs-only stratum.
func TestStarCasesAreAllGithubStars(t *testing.T) {
	cases, err := LoadStars()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.ExpectedSource != SourceGithubStars {
			t.Errorf("case %s has expected_source %q, want %q -- #1111's stratum is github-stars only", c.ID, c.ExpectedSource, SourceGithubStars)
		}
	}
}

// TestStarCasesExpectedIdentifiersArePublicRepoPaths guards the constraint
// this fixture must never violate: every expected answer is a publishable
// github.com URL, keyed the same way stars-01..03 in cases.json already
// are -- never an it- id, never operator/corpus/vault content.
func TestStarCasesExpectedIdentifiersArePublicRepoPaths(t *testing.T) {
	cases, err := LoadStars()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if len(c.ExpectedIdentifier) < len("https://github.com/") || c.ExpectedIdentifier[:len("https://github.com/")] != "https://github.com/" {
			t.Errorf("case %s expected_identifier %q is not a github.com URL", c.ID, c.ExpectedIdentifier)
		}
	}
}

// TestEveryStarCaseHasTargetText guards agent-estate#1115's own
// measurement primitive: cmd/goldenquery's overlapFraction needs a
// TargetText to measure a case's question against, and a case missing it
// would silently drop out of the stratum's reported mean rather than
// failing loudly here.
func TestEveryStarCaseHasTargetText(t *testing.T) {
	cases, err := LoadStars()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.TargetText == "" {
			t.Errorf("case %s has no target_text -- agent-estate#1115's overlap measurement needs one", c.ID)
		}
	}
}

// TestStarCaseQuestionsDoNotEchoOwnerOrRepoName guards the exact defect
// agent-estate#1115 was filed over: stars-nl-05's original question said
// "starred dotfiles repo" against a target literally named
// mathiasbynens/dotfiles, making the hit near self-retrieval rather than
// evidence of ranking quality. This checks every case's question against
// its own target's owner and repo-name segments (case-insensitively) so a
// future edit cannot reintroduce that one case's defect unnoticed -- it
// does not (and cannot) catch the broader description-text leak #1115's
// own comment measured, which is why cmd/goldenquery's overlapFraction
// exists as a continuously-run measurement rather than a one-time gate.
func TestStarCaseQuestionsDoNotEchoOwnerOrRepoName(t *testing.T) {
	cases, err := LoadStars()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		full := strings.TrimPrefix(c.ExpectedIdentifier, "https://github.com/")
		parts := strings.SplitN(full, "/", 2)
		if len(parts) != 2 {
			t.Errorf("case %s expected_identifier %q does not look like https://github.com/<owner>/<repo>", c.ID, c.ExpectedIdentifier)
			continue
		}
		owner, repo := parts[0], parts[1]
		q := strings.ToLower(c.Question)
		if strings.Contains(q, strings.ToLower(owner)) {
			t.Errorf("case %s question contains its own target's owner %q -- near self-retrieval, see agent-estate#1115", c.ID, owner)
		}
		if strings.Contains(q, strings.ToLower(repo)) {
			t.Errorf("case %s question contains its own target's repo name %q -- near self-retrieval, see agent-estate#1115", c.ID, repo)
		}
	}
}

// TestStarCasesIDsAreDistinctFromOtherStrata guards the keying #1111
// requires: no github-stars case may collide with a cases.json or
// natural_cases.json id.
func TestStarCasesIDsAreDistinctFromOtherStrata(t *testing.T) {
	golden, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	natural, err := LoadNatural()
	if err != nil {
		t.Fatal(err)
	}
	otherIDs := map[string]bool{}
	for _, c := range golden {
		otherIDs[c.ID] = true
	}
	for _, c := range natural {
		otherIDs[c.ID] = true
	}
	stars, err := LoadStars()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range stars {
		if otherIDs[c.ID] {
			t.Errorf("github-stars case id %q collides with a cases.json or natural_cases.json id", c.ID)
		}
	}
}
