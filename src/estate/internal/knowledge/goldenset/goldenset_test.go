package goldenset

import "testing"

// These tests check the golden set's own shape -- coverage, no duplicate
// ids, every case has what a case needs -- never whether a query against
// the real index scores well. That measurement is cmd/goldenquery's job,
// run against a live compiled index, not a `go test` fixture.

func TestLoadParsesEmbeddedCases(t *testing.T) {
	cases, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cases) < 10 || len(cases) > 20 {
		t.Fatalf("len(cases) = %d, want 10-20 per #1019's acceptance criterion", len(cases))
	}
}

func TestEveryCaseHasQuestionAndRationale(t *testing.T) {
	cases, err := Load()
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

// TestCoversAllFourSources is #1019's own requirement: "A set that only
// exercises one source measures one source."
func TestCoversAllFourSources(t *testing.T) {
	cases, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[ExpectedSource]bool{
		SourceVaultFact:       false,
		SourceCorpusParameter: false,
		SourceGithubStars:     false,
		SourceLoopsResearch:   false,
	}
	for _, c := range cases {
		if _, ok := want[c.ExpectedSource]; ok {
			want[c.ExpectedSource] = true
		}
	}
	for src, ok := range want {
		if !ok {
			t.Errorf("no golden-set case covers source %q", src)
		}
	}
}

// TestHasAtLeastOneNoMatchCase is the absence-path requirement: without
// one, the no_match branch of Query is never exercised by this measure.
func TestHasAtLeastOneNoMatchCase(t *testing.T) {
	cases, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.ExpectedSource == SourceNone {
			return
		}
	}
	t.Error("no case in the golden set expects no_match -- the absence path is never measured")
}

// TestHasAtLeastOneLowOverlapCase requires at least one case to document,
// via Note, that it deliberately shares few terms with its answer's
// indexed text -- #1019's guard against a set that only asks questions
// phrased like the data.
func TestHasAtLeastOneLowOverlapCase(t *testing.T) {
	cases, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Note != "" {
			return
		}
	}
	t.Error("no case documents deliberately low term overlap with its answer")
}
