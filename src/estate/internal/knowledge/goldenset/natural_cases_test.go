package goldenset

import "testing"

// These tests check the natural-language stratum's own shape (#1073) --
// coverage, no duplicate ids, every case has what a case needs -- never
// whether a query against the real index scores well. That measurement is
// cmd/goldenquery's job, run against a live compiled index, not a `go test`
// fixture. Mirrors goldenset_test.go's checks on cases.json without editing
// that file, per #1073's own instruction.

func TestLoadNaturalParsesEmbeddedCases(t *testing.T) {
	cases, err := LoadNatural()
	if err != nil {
		t.Fatalf("LoadNatural() error: %v", err)
	}
	if len(cases) != 21 {
		t.Fatalf("len(cases) = %d, want 21 -- #1073's original twelve-case set plus agent-estate#1169's nine per-file cases (nl-13..nl-21)", len(cases))
	}
}

func TestEveryNaturalCaseHasQuestionAndRationale(t *testing.T) {
	cases, err := LoadNatural()
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

// TestNaturalCasesAreAllRepoDocs documents the stratum's own scope: #1073
// is the repo-docs (AGENTS.md/docs/**/*.md) natural-language stratum only.
// The operator-knowledge stratum (#1053) is explicitly excluded -- its
// questions paraphrase private directives, and whether that may be checked
// into a public repository is the operator's call, not this fixture's.
func TestNaturalCasesAreAllRepoDocs(t *testing.T) {
	cases, err := LoadNatural()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.ExpectedSource != SourceRepoDocs {
			t.Errorf("case %s has expected_source %q, want %q -- #1073's stratum is repo-docs only", c.ID, c.ExpectedSource, SourceRepoDocs)
		}
	}
}

// TestNaturalCasesIDsAreDistinctFromGoldenSet guards the keying #1073
// requires: no natural-language case may collide with a cases.json id.
func TestNaturalCasesIDsAreDistinctFromGoldenSet(t *testing.T) {
	golden, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	goldenIDs := map[string]bool{}
	for _, c := range golden {
		goldenIDs[c.ID] = true
	}
	natural, err := LoadNatural()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range natural {
		if goldenIDs[c.ID] {
			t.Errorf("natural-language case id %q collides with a cases.json id", c.ID)
		}
	}
}
