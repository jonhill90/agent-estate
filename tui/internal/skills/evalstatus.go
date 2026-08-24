package skills

import (
	"encoding/json"
	"os"
)

// VerdictCouldNotMeasure is jonhill90/skills#230's fifth verdict value, on
// disk in docs/eval-status.json's own `$comment` alongside
// keep/improve/rename/drop/unevaluated. An eval RAN for this skill but
// produced no reliable skill-attributable signal -- a real, different
// state from VerdictUnevaluated (no eval has run at all). agent-tui#151:
// collapsing the two onto one value would erase exactly the distinction
// this estate insists on (blindness must not render as absence), so this
// package carries it as its own string rather than mapping it onto
// VerdictUnevaluated anywhere in EvalStatusFetcher below.
const VerdictCouldNotMeasure = "could_not_measure"

// evalRecord is one entry from docs/eval-status.json's own "skills" map --
// only the two fields View renders (Verdict, LastEval's source date).
// Evidence is on disk too but nothing in this package's Skill has a column
// for it yet, so it is not decoded here.
type evalRecord struct {
	Verdict string  `json:"verdict"`
	Date    *string `json:"date"`
}

// evalStatusFile is the top-level shape of jonhill90/skills'
// docs/eval-status.json (skills#230, commit e6c33a5) -- a `$comment` field
// this package ignores, plus the map EvalStatusFetcher merges in.
type evalStatusFile struct {
	Skills map[string]evalRecord `json:"skills"`
}

// loadEvalStatus reads path as an evalStatusFile. A missing or unreadable
// file, or one that is not valid JSON in this shape, is a real error --
// callers that want "absent degrades to unevaluated" (EvalStatusFetcher)
// check the error and choose to ignore it themselves, the same "could not
// look is not the same as looked and found nothing" split Scan already
// draws for the skills directory itself.
func loadEvalStatus(path string) (map[string]evalRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f evalStatusFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return f.Skills, nil
}

// EvalStatusFetcher builds a Fetcher combining Scan(skillsDir) with
// evalStatusPath's own docs/eval-status.json (jonhill90/skills#230,
// landed e6c33a5 the day after this package's "no persistence layer yet"
// doc comment was written -- agent-tui#151). Each fetch (including the
// pane's own 30s refresh and [r]) re-reads both sources fresh, matching
// Scan's own no-caching behaviour, so a re-run eval shows up without
// restarting the TUI.
//
// LAST EVAL is filled in from the matching record's date, VERDICT from its
// verdict (VerdictCouldNotMeasure preserved as its own value, never
// flattened onto VerdictUnevaluated -- see that const's own doc comment).
// A skill with no matching Dir in the store, or evalStatusPath == "", or a
// store that does not exist or fails to parse, is left exactly as Scan
// produced it -- Verdict's zero value ("unevaluated") and LastEval nil --
// agent-tui#151's own "degrade to today's honest unevaluated when the file
// is absent rather than erroring" scope line. This is deliberately NOT
// surfaced through the returned error (which stays Scan's own): a caller
// running with no skills-repo checkout configured is an expected, common
// case, not a fetch failure of the skill list itself, so it must not
// paint m.fetchErr red the way a genuinely broken ~/.claude/skills scan
// would.
//
// INVOCATIONS has no source in docs/eval-status.json (agent-tui#151's own
// scope line) and is left untouched -- always nil, always "unknown".
func EvalStatusFetcher(skillsDir, evalStatusPath string) Fetcher {
	return func() ([]Skill, error) {
		out, err := Scan(skillsDir)
		if err != nil {
			return nil, err
		}
		if evalStatusPath == "" {
			return out, nil
		}
		status, err := loadEvalStatus(evalStatusPath)
		if err != nil {
			// Absent or unreadable store: stay honestly unevaluated rather
			// than erroring the whole skill list (this func's own doc
			// comment).
			return out, nil
		}
		for i := range out {
			rec, ok := status[out[i].Dir]
			if !ok {
				continue
			}
			if rec.Verdict != "" {
				out[i].Verdict = rec.Verdict
			}
			if rec.Date != nil && *rec.Date != "" {
				date := *rec.Date
				out[i].LastEval = &date
			}
		}
		return out, nil
	}
}
