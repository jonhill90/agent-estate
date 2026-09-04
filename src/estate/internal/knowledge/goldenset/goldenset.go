// Package goldenset holds the fixed golden set for `estate knowledge
// query` (agent-estate#1023, the follow-on measurement PR to #1022's
// query/get commands, itself closing #1019) and the loader the runner in
// cmd/goldenquery reads it through.
//
// THE RULE THAT MAKES THIS HONEST: every case's ExpectedIdentifier was
// chosen by reading the source material directly (a vault fact file, a
// live_parameters row, a starred repo's own description, a
// Loops-Research file) and deciding which item is the authoritative
// answer -- never by running `estate knowledge query` first and copying
// back whatever it returned. Doing the latter would encode the current
// ranking as the definition of correct and make the score 100% forever.
// See each case's own "rationale" field in cases.json for the source
// evidence that fixed its answer in advance.
//
// ExpectedIdentifier is deliberately NOT the compiled index's own Item.ID
// (a 14-char clock value internal/knowledge's idClock assigns fresh on
// every `estate knowledge` regenerate -- see id.go's own doc comment).
// That value is not stable across two runs of the same compile, so it
// cannot be "the authoritative source ID recorded in advance" the issue
// asks for. What IS stable is each item's own Permalink: a vault fact's
// file path, a corpus row's "corpus:item:<id>", or a GitHub star's own
// html_url. ExpectedIdentifier is checked against a returned Match's
// Permalink with strings.HasSuffix -- a full URL or "corpus:item:..."
// string is checked whole (a string is trivially its own suffix); a
// vault fact or Loops-Research file is checked by basename only, since
// $AGENT_MEMORY_VAULT and the Loops-Research checkout path are
// per-machine and the slug is what is actually stable.
package goldenset

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed cases.json
var casesJSON []byte

// ExpectedSource names which of estate knowledge's four compiled sources
// (or "none") a case's answer comes from -- see internal/knowledge's own
// Item.Source values, which these mirror exactly except for "none".
type ExpectedSource string

const (
	SourceVaultFact       ExpectedSource = "vault-fact"
	SourceCorpusParameter ExpectedSource = "corpus-parameter"
	SourceGithubStars     ExpectedSource = "github-stars"
	SourceLoopsResearch   ExpectedSource = "loops-research"
	// SourceNone marks a case whose honest answer is that nothing in the
	// index answers it -- the absence path #1019 requires be measured.
	SourceNone ExpectedSource = "none"
)

// Case is one golden-set question: a real question, its authoritative
// answer decided in advance, and the one-line evidence for why that
// answer is authoritative (Rationale). ExpectedIdentifier is empty only
// for a SourceNone case.
type Case struct {
	ID                 string         `json:"id"`
	Question           string         `json:"question"`
	ExpectedSource     ExpectedSource `json:"expected_source"`
	ExpectedIdentifier string         `json:"expected_identifier,omitempty"`
	Rationale          string         `json:"rationale"`
	// Note documents anything a reader of the raw case shouldn't have to
	// infer -- e.g. that a case is the deliberate low-term-overlap one.
	Note string `json:"note,omitempty"`
}

// Load parses the embedded cases.json. It only ever fails if cases.json
// itself is malformed -- there is no filesystem path to miss at runtime.
func Load() ([]Case, error) {
	var cases []Case
	if err := json.Unmarshal(casesJSON, &cases); err != nil {
		return nil, fmt.Errorf("goldenset: cases.json is malformed: %w", err)
	}
	return cases, nil
}
