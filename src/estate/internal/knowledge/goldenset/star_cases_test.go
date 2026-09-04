package goldenset

import "testing"

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
