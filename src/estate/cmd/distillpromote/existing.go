package main

import (
	"os"
	"path/filepath"
	"strings"
)

// agent-estate#1339: two independent mechanisms write into the vault with
// zero cross-awareness of each other -- vault-view (internal/vaultview)
// publishes one Parameter note per hard corpus item, unconditionally, and
// this command promotes a subset of that same population to Fact notes.
// Measured on the live vault, 2026-09-11: of 107 facts this command would
// promote today, 101 (94%) already have a vault-view Parameter note for the
// same corpus_item, and 56 (52%) already have an existing Fact note for the
// same corpus_item -- 44 of those 56 are an EXACT repeat (same corpus_item,
// same subject: a prior run's own output), the other 12 are a genuinely
// different subject angle on the same underlying statement (see
// distillpromote_test.go's own worked example: "do not merge your own PRs"
// promoted once under "lane" and once under "merge", each with entirely
// distinct evidence -- not redundant with each other, only their
// headline/description collide).
//
// This file's two scans are read-only, over the vault the caller already
// pointed *vault at -- never a write, never a mutation of anything under
// "01 - Notes". Both existingParams and existingFacts key on corpus_item,
// the join key the investigation verified holds across every duplicate
// cluster it found (agent-estate#1339's own issue comment).

// factRef is one existing Fact note's identity, enough to report and to
// cite from a newly-written sibling Fact -- never enough to imply this
// package wrote it: the id/subject came straight off the file on disk,
// whatever generator produced it (`process:distill-judged`, the pre-#1290
// pipeline that wrote the 233 Facts live today, or this command's own
// `process:distill-promote`, which has never yet run with -apply against
// this vault -- see the issue's own provenance finding). Both must be
// scanned the same way, or the 233 already-live Facts would be invisible to
// this exact check and the duplication it exists to stop would recur on the
// very first real -apply run.
type factRef struct {
	ID      string
	Subject string
}

// frontmatterField reads one YAML-ish "key: value" line from a note's raw
// text, the same line-scan shape vaultview.go's own field() uses (never
// imported -- vaultview does not export it, and a second five-line copy is
// cheaper and safer than moving it to a third package for one caller).
// Values are optionally double-quoted; the quotes are stripped, matching
// how quote()/%q write them in both vaultview.go and this command's own
// render().
func frontmatterField(raw, key string) string {
	for _, line := range strings.Split(raw, "\n") {
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), "\"")
		}
	}
	return ""
}

// existingParams reads every note under 01p - Parameters and returns
// corpus_item -> note id, for every note that actually carries one. A note
// missing corpus_item (malformed, or mid-write) is skipped rather than
// treated as a match against nothing -- the same "cannot tell is not a
// match" discipline the gate package applies to its own refusals.
func existingParams(vault string) (map[string]string, error) {
	out := map[string]string{}
	dir := filepath.Join(vault, "01 - Notes", "01p - Parameters")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		s := string(b)
		ci := frontmatterField(s, "corpus_item")
		if ci == "" {
			continue
		}
		id := frontmatterField(s, "id")
		if id == "" {
			id = strings.TrimSuffix(e.Name(), ".md")
		}
		out[ci] = id
	}
	return out, nil
}

// existingFacts reads every note under 01f - Facts and returns
// corpus_item -> every (id, subject) pair on file for it, regardless of
// which pipeline generated the note -- see this file's own doc comment on
// why both `process:distill-judged` and `process:distill-promote` notes
// must be scanned the same way. A corpus_item with two Facts under two
// subjects (the "do not merge your own PRs" shape, agent-estate#1339)
// returns both; that is the caller's decision to preserve, not something
// this scan collapses.
func existingFacts(vault string) (map[string][]factRef, error) {
	out := map[string][]factRef{}
	dir := filepath.Join(vault, "01 - Notes", "01f - Facts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		s := string(b)
		ci := frontmatterField(s, "corpus_item")
		if ci == "" {
			continue
		}
		id := frontmatterField(s, "id")
		if id == "" {
			id = strings.TrimSuffix(e.Name(), ".md")
		}
		out[ci] = append(out[ci], factRef{ID: id, Subject: frontmatterField(s, "subject")})
	}
	return out, nil
}
