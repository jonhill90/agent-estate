package goldenset

import "testing"

// These tests check the retrieval-baseline stratum's own shape
// (agent-estate#1315) -- coverage, no duplicate ids, every case has what
// a case needs -- never whether a query against the real index scores
// well. That measurement is this task's own cmd/goldenquery -baseline
// run against a live compiled index, not a `go test` fixture. Mirrors
// star_cases_test.go's checks on star_cases.json without editing that
// file, per #1073's own precedent for a separate stratum getting its own
// loader and its own shape tests.

func TestLoadRetrievalBaselineParsesEmbeddedCases(t *testing.T) {
	cases, err := LoadRetrievalBaseline()
	if err != nil {
		t.Fatalf("LoadRetrievalBaseline() error: %v", err)
	}
	if len(cases) != 26 {
		t.Fatalf("len(cases) = %d, want 26 -- agent-estate#1315's own measured baseline set, every question written before its answer was looked up", len(cases))
	}
}

func TestEveryRetrievalBaselineCaseHasQuestionAndRationale(t *testing.T) {
	cases, err := LoadRetrievalBaseline()
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

// TestRetrievalBaselineCasesAreVaultOrCorpus documents the stratum's own
// scope: agent-estate#1315 is about the layer distilledRuleWeight
// operates on -- vault facts -- and the operator's own corpus parameters
// that motivate them, not repo-docs (#1073 already covers that) or
// github-stars (#1111 already covers that). A few repo-docs cases are
// included deliberately (rb-02..rb-08, rb-24) where they reproduce the
// issue's own worked example or a comparably idiomatic phrasing gap, but
// the stratum's majority and its reason for existing is vault-fact and
// corpus-parameter.
func TestRetrievalBaselineCasesAreVaultOrCorpus(t *testing.T) {
	cases, err := LoadRetrievalBaseline()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		switch c.ExpectedSource {
		case SourceVaultFact, SourceCorpusParameter, SourceRepoDocs:
			// allowed
		default:
			t.Errorf("case %s has expected_source %q, want vault-fact, corpus-parameter or repo-docs", c.ID, c.ExpectedSource)
		}
	}
}

// TestRetrievalBaselineCasesIDsAreDistinctFromOtherStrata guards the
// keying every stratum requires: no retrieval-baseline case may collide
// with a cases.json, natural_cases.json or star_cases.json id.
func TestRetrievalBaselineCasesIDsAreDistinctFromOtherStrata(t *testing.T) {
	golden, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	natural, err := LoadNatural()
	if err != nil {
		t.Fatal(err)
	}
	stars, err := LoadStars()
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
	for _, c := range stars {
		otherIDs[c.ID] = true
	}
	baseline, err := LoadRetrievalBaseline()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range baseline {
		if otherIDs[c.ID] {
			t.Errorf("retrieval-baseline case id %q collides with another stratum's id", c.ID)
		}
	}
}
