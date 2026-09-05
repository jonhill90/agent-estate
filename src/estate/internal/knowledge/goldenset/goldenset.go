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
// per-machine and the slug is what is actually stable. A repo-docs case
// (agent-estate#1034) is checked the same way, by "<relative
// path>.md#<anchor>" -- repoRoot itself is per-machine, but the relative
// path and the section's own anchor are not.
package goldenset

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed cases.json
var casesJSON []byte

// naturalCasesJSON is agent-estate#1073's own fixture: the twelve
// natural-language questions #1069 measured by hand at top-3 5/12, top-10
// 10/12 on a7d413c, checked in so that measurement stops living only in a
// session transcript (this repo's own rule -- see AGENTS.md's "Invariants"
// section, invariant 1). It answers a different question from cases.json:
// cases.json asks whether retrieval can find a known item; this asks
// whether a caller who does not already know the answer lands on it. Every
// target was chosen from its document's own section title before any query
// ran (see each case's own rationale) -- never re-derived from what
// retrieval currently returns. #1069 settled that this stratum's score must
// never be averaged with cases.json's -- report both, separately, leading
// with the weaker.
//
//go:embed natural_cases.json
var naturalCasesJSON []byte

// starCasesJSON is agent-estate#1111's own fixture: the github-stars
// natural-language stratum. #1073's own natural-language stratum is
// repo-docs only (TestNaturalCasesAreAllRepoDocs enforces it); #1063's
// follow-on comment measured that repo-docs is only 23% of the public
// store (118 of 509 items) and github-stars the other 77% (391 of 509),
// so no number goldenquery printed before this fixture could move on a
// github-stars regression. The eight cases here answer the same question
// #1073 answers for repo-docs -- does a caller who does not already know
// the answer land on it -- for the source that was previously invisible.
// Targets were chosen from each starred repo's own description before
// any query ran, same discipline as natural_cases.json and cases.json.
// Kept as its own loader, not folded into LoadNatural, for the same
// reason natural_cases.json is kept separate from cases.json: this
// stratum must never be averaged with either of the other two.
//
//go:embed star_cases.json
var starCasesJSON []byte

// ExpectedSource names which of estate knowledge's five compiled sources
// (or "none") a case's answer comes from -- see internal/knowledge's own
// Item.Source values, which these mirror exactly except for "none".
type ExpectedSource string

const (
	SourceVaultFact       ExpectedSource = "vault-fact"
	SourceCorpusParameter ExpectedSource = "corpus-parameter"
	// SourceCorpusDirective is agent-estate#1150's own addition: the
	// corpus-directive-targeted stratum inside cases.json (camelcase-01
	// through camelcase-05) that measures whether camelCase token
	// splitting perturbs retrieval over the operator's own directive
	// text specifically, not just the smaller corpus-parameter sample
	// #1151 already covered. See internal/knowledge/corpus.go's
	// corpusSourceName for why this is a distinct Source from
	// "corpus-parameter" rather than folded into it.
	SourceCorpusDirective ExpectedSource = "corpus-directive"
	SourceGithubStars     ExpectedSource = "github-stars"
	SourceLoopsResearch   ExpectedSource = "loops-research"
	// SourceRepoDocs is agent-estate#1034's own source: AGENTS.md and
	// docs/**/*.md, indexed by heading.
	SourceRepoDocs ExpectedSource = "repo-docs"
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
	// TargetText is the target item's own record text -- verbatim, the
	// same string a query against the real index would score the
	// question against (e.g. a github-stars item's "<repo> -- <starred
	// description>" tier1) -- kept so cmd/goldenquery can measure how
	// much of Question's own vocabulary the fixture author copied from
	// the answer rather than derived from a caller's actual need
	// (agent-estate#1115: the merged star_cases.json questions reused 67%
	// of their target's own words, which is why every case landed at
	// rank 1 and the stratum had no headroom left to detect a
	// regression). Empty for a stratum that has not wired this
	// measurement yet -- cmd/goldenquery skips the overlap line for any
	// case missing it rather than reporting a false zero.
	TargetText string `json:"target_text,omitempty"`
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

// LoadNatural parses the embedded natural_cases.json -- the
// natural-language stratum (agent-estate#1073). It only ever fails if
// natural_cases.json itself is malformed -- there is no filesystem path to
// miss at runtime. Kept as a separate loader, not folded into Load, because
// the two sets must never be scored together (#1069): a caller that wants
// one stratum should not have to filter the other back out.
func LoadNatural() ([]Case, error) {
	var cases []Case
	if err := json.Unmarshal(naturalCasesJSON, &cases); err != nil {
		return nil, fmt.Errorf("goldenset: natural_cases.json is malformed: %w", err)
	}
	return cases, nil
}

// LoadStars parses the embedded star_cases.json -- the github-stars
// natural-language stratum (agent-estate#1111). It only ever fails if
// star_cases.json itself is malformed -- there is no filesystem path to
// miss at runtime. Kept as a separate loader from both Load and
// LoadNatural so this stratum's score is never averaged with either.
func LoadStars() ([]Case, error) {
	var cases []Case
	if err := json.Unmarshal(starCasesJSON, &cases); err != nil {
		return nil, fmt.Errorf("goldenset: star_cases.json is malformed: %w", err)
	}
	return cases, nil
}
