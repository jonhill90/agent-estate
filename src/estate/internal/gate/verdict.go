package gate

// Comment verdict-line parsing, ported from src/tui's internal/prverdict
// (itself a port of jonhill90/skills#255's pr_verdict.py, itself a port of
// agent-supervisor's verdict.py) into this module -- src/estate and src/tui
// are separate Go modules, so this is a copy, not an import. What differs
// from prverdict.Resolve: that package compares a comment's Review-Lane:
// trailer against a PR body's self-declared Author-Lane: trailer, because
// src/tui has no ledger and self-declared trailers are the only authorship
// signal it can reach. This package has a ledger (internal/ledger), so
// authorship and reviewer-completion are established there (see gate.go and
// review.go); this file's only job is constraint 4 of agent-estate#926 --
// "approval must be parsed from the verdict LINE, not matched anywhere in
// the body" -- applied to the ONE reviewer lane the ledger has already
// confirmed completed a review of this PR.
//
// agent-supervisor#192/#198/#213/#475 are the fixed bugs this regex and
// classifier already carry (see prverdict.go's doc comment for detail);
// they are not re-derived here.

import (
	"regexp"
	"strings"
)

// verdictLineRE recognises a verdict line on its content, not its emphasis.
var verdictLineRE = regexp.MustCompile(`(?i)^#{0,6}\s*.*?\*{0,2}verdict:\**\s*(.*)$`)

// Whole-token match, not substring: "Verdict: NOT APPROVED" contains
// "APPROVE" and must not read as an approval.
var negationMarkers = []string{"NOT", "DIS", "NO", "N'T"}

var approvedTokens = map[string]bool{"APPROVE": true, "APPROVED": true}

var rejectedTokens = map[string]bool{
	"REQUEST CHANGES":   true,
	"REQUEST-CHANGES":   true,
	"REJECTED":          true,
	"CHANGES REQUESTED": true,
}

type verdictDecision string

const (
	verdictApproved verdictDecision = "approved"
	verdictRejected verdictDecision = "rejected"
)

func classifyDecisionText(decisionText string) (verdictDecision, bool) {
	for _, marker := range negationMarkers {
		if strings.Contains(decisionText, marker) {
			return "", false
		}
	}
	if approvedTokens[decisionText] {
		return verdictApproved, true
	}
	if rejectedTokens[decisionText] {
		return verdictRejected, true
	}
	return "", false
}

var (
	leadingEmphasisRE = regexp.MustCompile("^[*_`]+")
	emphasisMarkerRE  = regexp.MustCompile("[*_`]")
	whitespaceRunRE   = regexp.MustCompile(`\s+`)
)

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

type verdictLine struct {
	decision verdictDecision
	ok       bool
	text     string
}

// scanVerdictLines never reads a line inside a fenced code block or a
// markdown blockquote (GitHub's "quote reply" shape) -- a verdict quoted as
// an example, or quoted from an earlier comment, must not be read as this
// comment restating it. This is exactly the trap agent-estate#926 names:
// "every council comment in this repo quotes prior verdicts in its seat
// table, so a substring match reads a REQUEST CHANGES that quotes an
// approval as an approval."
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

// reviewLaneRE / reviewedSHARE anchor to `[ \t]*` (never bare `\s*`) between
// the trailer's colon and its value, and end each pattern with `\r?$` on
// its OWN line. A bare `\s*` there can walk across the newline when the
// trailer's value is blank and capture the NEXT line as if it were the
// value (jonhill90/skills#258/#260, agent-tui#112) -- ported unchanged
// because the same regex shape is used here.
var (
	reviewLaneRE  = regexp.MustCompile(`(?im)^[ \t]*Review-Lane:[ \t]*(.*)\r?$`)
	reviewedSHARE = regexp.MustCompile(`(?im)^[ \t]*Reviewed-SHA:[ \t]*([A-Za-z0-9]+)[ \t]*\r?$`)
	// authorLaneRE mirrors reviewLaneRE's shape for the PR body's own
	// Author-Lane: trailer (repo convention, AGENTS.md/CLAUDE.md). Used only
	// as a contradiction check in gate.go, never as the authorship source
	// itself -- see authorFromHeadRef and authorLaneForDispatchID.
	authorLaneRE = regexp.MustCompile(`(?im)^[ \t]*Author-Lane:[ \t]*(.*)\r?$`)
)

// parseAuthorLaneTrailer reads the PR body's own Author-Lane: trailer, if
// any. It is deliberately the same parseTrailer(reviewLaneRE-shaped) helper
// used for Review-Lane: and Reviewed-SHA:, so a blank value or a trailer
// with no match behaves identically to those two known-safe cases.
func parseAuthorLaneTrailer(body string) (string, bool) {
	return parseTrailer(authorLaneRE, body)
}

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

// laneVerdict is this package's own wiring on top of the ported scanner
// above: find the reviewer's OWN verdict comment for lane, and resolve it.
// verdictComment finds the LAST comment carrying both a qualifying
// Verdict: line and a Review-Lane: trailer matching lane exactly -- "last"
// so a lane that first REQUEST CHANGES and later re-reviews APPROVE is read
// as its current verdict, and an unrecognised decision on a later comment
// is never silently overridden by an earlier approval underneath it.
type laneVerdict struct {
	found       bool
	decision    verdictDecision
	ok          bool
	reason      string
	reviewedSHA string
	hasSHA      bool
}

// resolveResultVerdict applies the same Verdict:-line scanner used against a
// PR comment to the ledger's own Result field for a reviewer's completed
// turn -- the second, independent source constraint 4 in gate.go now
// cross-checks the PR comment against (agent-estate#934). There is no
// Review-Lane: trailer to match here: the record is already scoped to the
// one reviewer lane by reviewerRecord's ledger lookup, not by anything
// parsed out of free text.
func resolveResultVerdict(result string) laneVerdict {
	scan := scanVerdictLines(result)
	if len(scan) == 0 {
		return laneVerdict{found: false, reason: "ledger result carries no Verdict: line"}
	}
	decisions := map[verdictDecision]bool{}
	var unrecognised []string
	for _, line := range scan {
		if line.ok {
			decisions[line.decision] = true
		} else {
			unrecognised = append(unrecognised, `"`+line.text+`"`)
		}
	}
	if len(decisions) != 1 {
		reason := "conflicting Verdict: lines in ledger result"
		if len(unrecognised) > 0 {
			reason = "decision text not recognised in ledger result: " + strings.Join(unrecognised, "; ")
		}
		return laneVerdict{found: true, ok: false, reason: reason}
	}
	var verdict verdictDecision
	for d := range decisions {
		verdict = d
	}
	return laneVerdict{found: true, ok: true, decision: verdict}
}

// DispatchBranchPrefix is the one fixed, estate-written string
// internal/isolate.Create prepends to a lane id to name its branch
// ("dispatch/" + id). A lane that works out who it is from its own
// checkout reports that branch name in its Review-Lane: trailer instead
// of the bare id -- agent-estate#943. Stripping this exact, known prefix
// before comparing is safe precisely because the estate itself writes it;
// it is not a step toward substring or suffix matching, which would
// reopen the impersonation hole agent-estate#934 closed. Only ONE leading
// occurrence is stripped -- "dispatch/dispatch/<id>" must still refuse,
// since a doubled prefix is not a form the estate ever writes.
//
// Exported (agent-estate#957) so a Review-Lane: contract stated outside
// this package -- main.reviewerContract, which tells a reviewer lane what
// form the trailer must take -- can assert its own text against this
// literal instead of restating it by hand and drifting out of agreement,
// which is exactly how #957 shipped a contract forbidding a form this
// package deliberately accepts.
const DispatchBranchPrefix = "dispatch/"

func normaliseLaneID(id string) string {
	return strings.TrimPrefix(id, DispatchBranchPrefix)
}

// stripSessionQualifier strips an optional leading "<session>:" qualifier --
// AGENTS.md Invariant 9's documented lane identity form, "<session>:<index>"
// (e.g. "agent-supervisor:1006-...-28789-1") -- returning the id unchanged
// when no qualifier is present. Only the FIRST colon is treated as the
// qualifier boundary: a dispatch id itself never contains one, so splitting
// there is unambiguous.
//
// This is deliberately NOT a substring match. The caller compares the
// entire remainder after the colon against a verified chain lane with exact
// equality (via normaliseLaneID) -- a trailer cannot pass by embedding a
// real id inside a longer string, only by naming exactly that id after an
// optional session prefix (agent-estate#1067).
func stripSessionQualifier(id string) string {
	if idx := strings.IndexByte(id, ':'); idx >= 0 {
		return id[idx+1:]
	}
	return id
}

// AcceptsReviewLane reports whether a Review-Lane: trailer value would be
// read as naming wantLane, under the exact normalisation
// resolveLaneVerdict applies. Exported (agent-estate#957) alongside
// DispatchBranchPrefix for the same reason: a contract stated outside this
// package that describes what the gate accepts for this trailer should
// assert it against the real comparison, not restate the rule by hand.
func AcceptsReviewLane(trailerValue, wantLane string) bool {
	return normaliseLaneID(trailerValue) == normaliseLaneID(wantLane)
}

func resolveLaneVerdict(comments []Comment, lane string) laneVerdict {
	var (
		lastBody string
		lastScan []verdictLine
		found    bool
	)
	wantLane := normaliseLaneID(lane)
	for _, c := range comments {
		reviewLane, hasReviewLane := parseTrailer(reviewLaneRE, c.Body)
		if !hasReviewLane || normaliseLaneID(reviewLane) != wantLane {
			continue
		}
		scan := scanVerdictLines(c.Body)
		if len(scan) == 0 {
			continue
		}
		lastBody = c.Body
		lastScan = scan
		found = true
	}
	if !found {
		return laneVerdict{found: false, reason: "no PR comment carries both a Verdict: line and Review-Lane: " + lane}
	}

	decisions := map[verdictDecision]bool{}
	var unrecognised []string
	for _, line := range lastScan {
		if line.ok {
			decisions[line.decision] = true
		} else {
			unrecognised = append(unrecognised, `"`+line.text+`"`)
		}
	}
	if len(decisions) != 1 {
		reason := "conflicting Verdict: lines in reviewer's own comment"
		if len(unrecognised) > 0 {
			reason = "decision text not recognised: " + strings.Join(unrecognised, "; ")
		}
		return laneVerdict{found: true, ok: false, reason: reason}
	}
	var verdict verdictDecision
	for d := range decisions {
		verdict = d
	}
	sha, hasSHA := parseTrailer(reviewedSHARE, lastBody)
	return laneVerdict{found: true, ok: true, decision: verdict, reviewedSHA: sha, hasSHA: hasSHA}
}
