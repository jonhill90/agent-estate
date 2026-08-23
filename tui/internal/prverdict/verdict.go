// Package prverdict reads a PR's own comments and decides whether it
// carries an independent, current APPROVE -- this repo's port of the
// comment-verdict gate agent-supervisor already runs
// (scripts/supervisor/verdict.py, scripts/supervisor/verdict-independence.sh)
// and jonhill90/skills#255 already ported to Python for the skills repo.
// This port follows skills#255's shape and adaptation, not
// agent-supervisor's original, per that PR's own instruction to reuse a
// fresh, reviewed port rather than re-derive one.
//
// Why this exists: every lane in this estate pushes through one shared
// GitHub login, so `gh pr review --approve` is refused as self-review no
// matter which lane is asking, and GitHub's own review state can never
// name which lane approved. A real cross-lane review still gets produced
// today -- posted as a plain PR comment carrying `Verdict: APPROVE`,
// `Review-Lane: <lane>`, `Reviewed-SHA: <sha>` -- but until this package,
// nothing in this repository read it (measured 2026-08-23: `git grep -i
// Reviewed-SHA` on origin/main returned nothing here). This package is
// that reader.
//
// WHAT IS PORTED, and what is not. The comment-scanning and decision-
// classification (the `Verdict:` line regex, the strict approve/reject
// token list, the negation guard, the fence/blockquote exclusion) matches
// skills#255's pr_verdict.py close to line-for-line, translated to Go --
// that logic is pure text processing, proven against real reviewer prose
// across a dozen-plus agent-supervisor issues (#53, #192, #196, #198,
// #213, #232, #475 -- cited inline below at the rule each one fixed), and
// re-deriving it worse from scratch would throw that history away. What is
// NOT ported is agent-supervisor's verdict-independence.sh lane-identity
// resolution (author_lane_for, lane_relation): that machinery answers
// "which lane authored this PR" from a tmux supervisor ledger this
// repository does not have and must not grow one to get (this repo's own
// AGENTS.md: nothing under internal/ shells out to tmux, and a ledger
// reader would be exactly that kind of second reader). Same adaptation as
// skills#255: an Author-Lane: trailer, self-declared in the PR body,
// stands in for ledger-resolved authorship -- the same trust model
// Review-Lane: already has.
//
// THE GATE THIS ANSWERS, exactly:
//   - a decisive verdict (approved/rejected) was posted as a PR comment
//     naming BOTH a Review-Lane: and a Reviewed-SHA:
//   - the reviewing lane is not blank and differs from the PR's own
//     Author-Lane: trailer, which must also be present
//   - Reviewed-SHA: equals the PR's CURRENT head -- exact match only,
//     deliberately narrower than verdict.py's own rebase-tolerant patch-id
//     comparison. That leniency exists there to stop a pure rebase from
//     silently invalidating a real review; porting it needs `git patch-id`
//     machinery this package has no fixture-proven need for yet. Narrower
//     is the safe direction to simplify in -- a stale-SHA false refusal
//     costs a re-review comment; a stale-SHA false pass costs an
//     unreviewed merge.
package prverdict

import (
	"regexp"
	"strings"
)

// Decision is the gate's whole verdict on one PR, at the moment it was
// asked. Never a bare string elsewhere in this package -- see the
// exported constants below for the exhaustive set.
type Decision string

const (
	// Approved: an independent, decisive, current-head APPROVE was found.
	Approved Decision = "approved"
	// Rejected: an independent, decisive REQUEST CHANGES at current head.
	Rejected Decision = "rejected"
	// None: no verdict-bearing comment exists on the PR at all.
	None Decision = "none"
	// Unknown: something IS on record but this package cannot call it --
	// same-lane, stale SHA, missing trailer, unparseable decision text, or
	// a fetch that failed outright. A caller MUST treat Unknown exactly as
	// hard as Rejected: it is never permission to proceed, the same
	// posture agent-supervisor's independence_verdict takes and skills#255
	// ported unchanged.
	Unknown Decision = "unknown"
)

// ExitCode is the process exit code a CLI wrapper (cmd/prverdict) returns
// for Decision -- fixed here, not left to each caller to reinvent, so
// "gate passed" is always "exit 0" everywhere this package is used.
func (d Decision) ExitCode() int {
	switch d {
	case Approved:
		return 0
	case Rejected:
		return 1
	case None:
		return 2
	default:
		return 3
	}
}

// Result is the gate's decision plus the human-readable reason -- the
// reason is never discarded, because "unknown" alone does not tell an
// operator whether the fix is "post a review" or "the reviewer named the
// wrong SHA."
type Result struct {
	Decision Decision
	Detail   string
}

// ---------------------------------------------------------------------
// Ported near-verbatim from agent-supervisor's verdict.py by way of
// skills#255's pr_verdict.py (that PR's own docstring names the
// agent-supervisor issue each rule fixed; kept here so a reader auditing
// THIS file does not have to go find either repo).
// ---------------------------------------------------------------------

// verdictLineRE recognises a verdict line on its CONTENT, not its
// emphasis (agent-supervisor#192: `**Verdict:` alone missed plain
// `Verdict: APPROVE` and the heading form `## Verdict: APPROVE`). Matches
// per line, an optional `#`..`######` heading marker, optional `**`/`*`
// opening emphasis, the literal word "Verdict" in any case, then `:`.
// agent-supervisor#213: the `.*?` prefix allows arbitrary lead-in text
// before the label ("## Independent review verdict: APPROVE") while
// staying fail-closed -- "verdict" merely mentioned in prose without an
// immediately following colon still does not match.
var verdictLineRE = regexp.MustCompile(`(?i)^#{0,6}\s*.*?\*{0,2}verdict:\**\s*(.*)$`)

// agent-supervisor#198: whole-token match, not substring -- `Verdict: NOT
// APPROVED` and `Verdict: DISAPPROVE` were misread as "approved" under a
// substring test because both contain "APPROVE". A negation anywhere in
// the text is unrecognised outright, ahead of the token match.
// agent-supervisor#475: two more REJECTED tokens measured off real review
// comments (CHANGES REQUESTED, and its hyphenated form via
// normaliseDecisionText's hyphen-fold below).
var negationMarkers = []string{"NOT", "DIS", "NO", "N'T"}

var approvedTokens = map[string]bool{
	"APPROVE":  true,
	"APPROVED": true,
}

var rejectedTokens = map[string]bool{
	"REQUEST CHANGES":   true,
	"REQUEST-CHANGES":   true,
	"REJECTED":          true,
	"CHANGES REQUESTED": true,
}

// classifyDecisionText: decisionText is already normalised (markup and
// punctuation stripped, whitespace collapsed, upper-cased) by the caller.
// Returns ("approved"|"rejected", true), or ("", false) for anything not
// an exact match, including a negated one -- an unrecognised decision must
// not be guessed at.
func classifyDecisionText(decisionText string) (Decision, bool) {
	for _, marker := range negationMarkers {
		if strings.Contains(decisionText, marker) {
			return "", false
		}
	}
	if approvedTokens[decisionText] {
		return Approved, true
	}
	if rejectedTokens[decisionText] {
		return Rejected, true
	}
	return "", false
}

var (
	leadingEmphasisRE = regexp.MustCompile("^[*_`]+")
	emphasisMarkerRE  = regexp.MustCompile("[*_`]")
	whitespaceRunRE   = regexp.MustCompile(`\s+`)
)

// normaliseDecisionText: agent-supervisor#213 strips emphasis WRAPPED
// AROUND the decision (`**APPROVE**` after a plain label, not just a bold
// label), truncates at a trailing emphasis marker, and folds a
// `+`-appended trailing action (`APPROVE + MERGE`) down to its first
// segment. agent-supervisor#475: hyphens fold to spaces before the token
// compare so `CHANGES-REQUESTED` and `CHANGES REQUESTED` need only one
// entry in rejectedTokens.
func normaliseDecisionText(rest string) string {
	text := strings.TrimSpace(rest)
	text = leadingEmphasisRE.ReplaceAllString(text, "")
	if loc := emphasisMarkerRE.FindStringIndex(text); loc != nil {
		text = text[:loc[0]]
	}
	text = strings.TrimSpace(text)
	text = strings.TrimRight(text, ".:;,!")
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "-", " ")
	text = whitespaceRunRE.ReplaceAllString(text, " ")
	text = strings.ToUpper(text)
	if idx := strings.Index(text, "+"); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}
	return text
}

// verdictLine is one line of a comment body that matched verdictLineRE,
// with its decision already classified (ok is false when the text is
// present but not recognised).
type verdictLine struct {
	decision Decision
	ok       bool
	text     string
}

// scanVerdictLines returns one verdictLine per line of body matching
// verdictLineRE. agent-supervisor#192: a line inside a fenced code block
// or a markdown blockquote (`>`, GitHub's "quote reply" shape) is never
// consulted -- a verdict quoted as an example, or quoted from an earlier
// comment, must not be read as this comment restating it.
func scanVerdictLines(body string) []verdictLine {
	var lines []string
	inFence := false
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || strings.HasPrefix(line, ">") {
			continue
		}
		lines = append(lines, line)
	}

	var results []verdictLine
	for _, line := range lines {
		match := verdictLineRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		text := normaliseDecisionText(match[1])
		decision, ok := classifyDecisionText(text)
		results = append(results, verdictLine{decision: decision, ok: ok, text: text})
	}
	return results
}

// Lane-identity trailers: a lane id here is free text a lane names
// itself, not a ledger-validated shape (this repo has no tmux ledger to
// validate against, same as skills#255's own adaptation note) -- so the
// trailer's VALUE is taken verbatim, trimmed, and compared case-
// sensitively as an exact string. Review-Lane: binds the reviewing lane;
// Author-Lane: binds the authoring one, in the PR body rather than a
// comment, because the author cannot post a comment on their own PR to
// state it any differently from writing it into the description at open
// time.
// jonhill90/skills#258/#260: the whitespace after the colon is restricted
// to `[ \t]*`, never `\s*` -- `\s*` matches a newline, so a BLANK trailer
// value (`Review-Lane:` with nothing after it) let the pattern's greedy
// post-colon whitespace consume the line break and its capture group
// swallow the NEXT line's text instead of matching an empty string. That
// garbage capture (e.g. "Reviewed-SHA: abc123") is non-empty, so
// parseTrailer returned it as a real lane id -- one that never equals a
// real Author-Lane: value -- which silently defeated the same-lane
// self-review check below. Anchoring to `[ \t]*` keeps the match on the
// trailer's own line; `(.*)$` still can't cross a line boundary on its
// own (Go's regexp/RE2 `.` never matches `\n` without `(?s)`, which this
// package does not set), so this was the only gap. Same bug, same fix,
// ported from skills#260 (Python) which fixed the identical pattern in
// jonhill90/skills's own pr_verdict.py.
var (
	reviewLaneRE  = regexp.MustCompile(`(?im)^[ \t]*Review-Lane:[ \t]*(.*)$`)
	authorLaneRE  = regexp.MustCompile(`(?im)^[ \t]*Author-Lane:[ \t]*(.*)$`)
	reviewedSHARE = regexp.MustCompile(`(?im)^\s*Reviewed-SHA:\s*([A-Za-z0-9]+)\s*$`)
)

func parseTrailer(re *regexp.Regexp, body string) (string, bool) {
	match := re.FindStringSubmatch(body)
	if match == nil {
		return "", false
	}
	value := strings.TrimSpace(match[1])
	if value == "" {
		return "", false
	}
	return value, true
}

// Comment is one PR comment, the only fields this package reads.
type Comment struct {
	Author string
	Body   string
}

// Payload is everything Resolve needs about one PR -- the fetch seam's
// return shape, deliberately narrow (this repo's adapter discipline:
// AGENTS.md's "every seam is a func type or small interface").
type Payload struct {
	Body     string // the PR's own body/description
	HeadSHA  string // the PR's current head commit SHA
	Comments []Comment
}

// ---------------------------------------------------------------------
// This package's own wiring: apply the ported logic above to a Payload,
// then the independence/freshness gate agent-supervisor's
// verdict-independence.sh states in shell/jq -- reimplemented directly
// here against the two self-declared trailers, same as skills#255.
// ---------------------------------------------------------------------

// Resolve is the gate's whole decision, in one place. Never panics on a
// malformed Payload -- a payload with no readable head SHA or comments
// resolves to Unknown, the same fail-closed posture every source in this
// package's ported logic already takes for a source it cannot read.
func Resolve(payload Payload) Result {
	if payload.HeadSHA == "" {
		return Result{Decision: Unknown, Detail: "PR payload has no readable head SHA"}
	}

	authorLane, hasAuthorLane := parseTrailer(authorLaneRE, payload.Body)

	// agent-supervisor#198: the LAST comment with at least one qualifying
	// Verdict: line is authoritative, even when its decision cannot be
	// classified -- a rejection phrased in words this scanner does not
	// recognise must not silently fall through to an earlier, since-
	// superseded approval underneath it.
	var (
		lastComment Comment
		lastScan    []verdictLine
		found       bool
	)
	for _, comment := range payload.Comments {
		scan := scanVerdictLines(comment.Body)
		if len(scan) > 0 {
			lastComment = comment
			lastScan = scan
			found = true
		}
	}
	if !found {
		return Result{Decision: None, Detail: "no comment on this PR carries a Verdict: line"}
	}

	authorLogin := lastComment.Author
	if authorLogin == "" {
		authorLogin = "an unknown author"
	}

	decisions := map[Decision]bool{}
	for _, line := range lastScan {
		if line.ok {
			decisions[line.decision] = true
		}
	}

	if len(decisions) != 1 {
		var unrecognised []string
		for _, line := range lastScan {
			if !line.ok {
				unrecognised = append(unrecognised, `"`+line.text+`"`)
			}
		}
		var reason string
		if len(unrecognised) > 0 {
			reason = "decision text not recognised: " + strings.Join(unrecognised, "; ")
		} else {
			reason = "conflicting Verdict: lines in one comment"
		}
		return Result{Decision: Unknown, Detail: "last verdict-bearing comment (by @" + authorLogin + ") unresolved -- " + reason}
	}

	var verdict Decision
	for d := range decisions {
		verdict = d
	}

	reviewLane, hasReviewLane := parseTrailer(reviewLaneRE, lastComment.Body)
	reviewedSHA, hasReviewedSHA := parseTrailer(reviewedSHARE, lastComment.Body)

	var problems []string
	if !hasReviewLane {
		problems = append(problems, "comment has no Review-Lane: trailer")
	}
	if !hasReviewedSHA {
		problems = append(problems, "comment has no Reviewed-SHA: trailer")
	}
	if !hasAuthorLane {
		problems = append(problems, "PR body has no Author-Lane: trailer -- authorship unknown")
	}
	if hasReviewLane && hasAuthorLane && reviewLane == authorLane {
		problems = append(problems, "reviewer lane \""+reviewLane+"\" is the same as the PR's own Author-Lane \""+authorLane+"\" -- self-review")
	}
	if hasReviewedSHA && reviewedSHA != payload.HeadSHA {
		problems = append(problems, "Reviewed-SHA "+reviewedSHA+" does not match current head "+payload.HeadSHA+" -- stale, does not count")
	}

	detail := string(verdict) + " comment by @" + authorLogin
	if hasReviewLane {
		detail += ", Review-Lane " + reviewLane
	}
	if len(problems) > 0 {
		return Result{Decision: Unknown, Detail: detail + " -- " + strings.Join(problems, "; ")}
	}

	return Result{Decision: verdict, Detail: detail + ", Reviewed-SHA " + reviewedSHA + " matches current head"}
}
