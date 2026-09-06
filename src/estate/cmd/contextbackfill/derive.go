// derive.go is contextbackfill's read-and-decide half: for each candidate
// (db.go's selectCandidates), re-parse the SAME source file codex_provenance
// already named, at the SAME position, and decide which of the four typed
// states applies. Nothing here writes; main.go's buildAndApply calls
// writeContext (db.go) only when -apply is set.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/provenance"
	"github.com/jonhill90/agent-estate/estate/internal/rollout"
)

// maxContextRunes bounds how much of a preceding assistant turn is kept --
// stated here, in code, per this task's own requirement 3 ("bound the
// context ... state the cap in code"). A rune count, not a byte count, so
// truncation never lands mid multi-byte character.
const maxContextRunes = 4000

// Typed state names -- every candidate ends up in exactly one of these, and
// every one of them is WRITTEN (see db.go's writeContext), never merely
// reported, so "absence" is a queryable value rather than the empty string
// this task exists to stop reproducing.
const (
	stateDerived          = "derived"
	stateNoPriorTurn      = "no_prior_turn"
	stateSourceUnreadable = "source_unreadable"
	stateRecordUnresolved = "record_unresolved"
	roleAssistant         = "assistant"
	roleNone              = "none"
)

// contextEnvelope is the self-describing value this command ever writes to
// prompts.context -- see the package doc comment's "one rule that must not
// bend". Role states authorship explicitly (never inferred from position);
// State is one of the four typed values above; Text is present only when
// State == stateDerived; Truncated and Detail are always present in the
// marshalled JSON when meaningful, omitted otherwise via omitempty (a
// reader querying json_extract(context, '$.state') never depends on which
// optional fields happen to be present).
type contextEnvelope struct {
	Role      string `json:"role"`
	State     string `json:"state"`
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

func marshalEnvelope(e contextEnvelope) (string, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// capText truncates s to at most max runes, reporting whether it did. It
// never splits a multi-byte rune.
func capText(s string, max int) (string, bool) {
	r := []rune(s)
	if len(r) <= max {
		return s, false
	}
	return string(r[:max]), true
}

// Decision is one candidate's final outcome, kept for reporting even though
// only Envelope is ever written.
type Decision struct {
	Candidate candidate
	Envelope  contextEnvelope
}

// Report is this run's whole evidence -- see cmd/codexingest's own Report
// doc comment for why every count must add up exactly rather than a bare
// "N derived".
type Report struct {
	Watermark              time.Time
	TotalCandidates        int
	Derived                int
	NoPriorTurn            int
	SourceUnreadable       int
	RecordUnresolved       int
	AlreadyProcessed       int
	ExcludedWatermarkRows  int
	ExcludedWatermarkFiles []string
	PromptContextBefore    int
	PromptContextAfter     int
	Applied                bool
}

// buildAndApply groups candidates by source file (preserving first-seen file
// order, exactly as cmd/codexingest's buildPlan does), decides each file's
// candidates together, and writes only when apply is true. A dry run
// performs the identical decision-making with zero calls to writeContext or
// ensurePromptContextTable.
func buildAndApply(dbPath string, watermark time.Time, apply bool) (Report, error) {
	r := Report{Watermark: watermark, Applied: apply}

	if apply {
		if err := ensurePromptContextTable(dbPath); err != nil {
			return r, fmt.Errorf("ensuring prompt_context table: %w", err)
		}
	}
	exists, err := promptContextTableExists(dbPath)
	if err != nil {
		return r, fmt.Errorf("checking prompt_context table: %w", err)
	}
	before := 0
	if exists {
		before, err = countPromptContextRows(dbPath)
		if err != nil {
			return r, fmt.Errorf("counting existing prompt_context rows: %w", err)
		}
	}
	r.PromptContextBefore = before

	already, err := alreadyProcessedPromptIDs(dbPath)
	if err != nil {
		return r, fmt.Errorf("reading already-processed prompt ids: %w", err)
	}

	candidates, err := selectCandidates(dbPath)
	if err != nil {
		return r, fmt.Errorf("selecting candidates: %w", err)
	}
	r.TotalCandidates = len(candidates)

	var files []string
	byFile := map[string][]candidate{}
	for _, c := range candidates {
		if _, seen := byFile[c.SourceFile]; !seen {
			files = append(files, c.SourceFile)
		}
		byFile[c.SourceFile] = append(byFile[c.SourceFile], c)
	}

	seenExcludedFile := map[string]bool{}

	for _, file := range files {
		group := byFile[file]

		info, statErr := os.Stat(file)
		if statErr == nil && info.ModTime().After(watermark) {
			// The source file changed after the watermark this run is
			// pinned to -- deferred to a later run, never derived and
			// never marked source_unreadable (it IS readable, just not
			// safe to trust as of this watermark).
			for range group {
				r.ExcludedWatermarkRows++
			}
			if !seenExcludedFile[file] {
				seenExcludedFile[file] = true
				r.ExcludedWatermarkFiles = append(r.ExcludedWatermarkFiles, file)
			}
			continue
		}

		fa, faErr := rollout.AnalyzeFile(file)
		if faErr != nil {
			for _, c := range group {
				if err := decideAndMaybeWrite(dbPath, &r, already, c, contextEnvelope{
					Role: roleNone, State: stateSourceUnreadable,
					Detail: faErr.Error(),
				}, watermark, apply); err != nil {
					return r, err
				}
			}
			continue
		}

		for _, c := range group {
			env := resolveCandidate(c, fa)
			if err := decideAndMaybeWrite(dbPath, &r, already, c, env, watermark, apply); err != nil {
				return r, err
			}
		}
	}

	after := before
	if apply {
		after, err = countPromptContextRows(dbPath)
		if err != nil {
			return r, fmt.Errorf("counting prompt_context rows after run: %w", err)
		}
	}
	r.PromptContextAfter = after

	sort.Strings(r.ExcludedWatermarkFiles)
	return r, nil
}

// resolveCandidate decides a single candidate's state against a freshly
// re-parsed FileAnalysis of its own source file. It re-validates identity
// (session id, content hash) exactly as cmd/codexingest's buildPlan does for
// "reject source change" -- a mismatch here means the file's shape changed
// since codexingest ran, and the position can no longer be trusted.
func resolveCandidate(c candidate, fa rollout.FileAnalysis) contextEnvelope {
	if c.RecordIndex < 0 || c.RecordIndex >= len(fa.Turns) {
		return contextEnvelope{
			Role: roleNone, State: stateRecordUnresolved,
			Detail: fmt.Sprintf("record index %d does not resolve: file re-parse found %d turn(s)", c.RecordIndex, len(fa.Turns)),
		}
	}
	turn := fa.Turns[c.RecordIndex]
	if turn.SessionID != c.SessionID {
		return contextEnvelope{
			Role: roleNone, State: stateRecordUnresolved,
			Detail: fmt.Sprintf("session id at this position no longer matches (recorded %q, live %q)", c.SessionID, turn.SessionID),
		}
	}
	if hash := provenance.HashContent(turn.Text); hash != c.ContentHash {
		return contextEnvelope{
			Role: roleNone, State: stateRecordUnresolved,
			Detail: fmt.Sprintf("content hash at this position no longer matches (recorded %s, live %s) -- source file changed since ingestion", c.ContentHash, hash),
		}
	}
	if turn.PriorAssistantText == "" {
		return contextEnvelope{Role: roleNone, State: stateNoPriorTurn}
	}
	text, truncated := capText(turn.PriorAssistantText, maxContextRunes)
	return contextEnvelope{Role: roleAssistant, State: stateDerived, Text: text, Truncated: truncated}
}

// decideAndMaybeWrite tallies env into r and, under apply, writes it -- one
// place both the dry-run and apply paths funnel through, so their counts can
// never diverge.
func decideAndMaybeWrite(dbPath string, r *Report, already map[string]bool, c candidate, env contextEnvelope, watermark time.Time, apply bool) error {
	if already[c.PromptID] {
		r.AlreadyProcessed++
		return nil
	}
	switch env.State {
	case stateDerived:
		r.Derived++
	case stateNoPriorTurn:
		r.NoPriorTurn++
	case stateSourceUnreadable:
		r.SourceUnreadable++
	case stateRecordUnresolved:
		r.RecordUnresolved++
	default:
		return fmt.Errorf("unreachable: unknown state %q for prompt %s", env.State, c.PromptID)
	}
	if apply {
		if err := writeContext(dbPath, c, env, watermark.Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("writing context for prompt %s: %w", c.PromptID, err)
		}
		already[c.PromptID] = true
	}
	return nil
}
