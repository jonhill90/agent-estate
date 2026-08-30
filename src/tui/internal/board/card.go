package board

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/src/tui/internal/lane"
)

// Column is one of the board's five projected states. Nothing assigns a
// Column directly -- Derive computes it fresh from GitHub, the ledger, and
// live lane state on every call, per agent-tui#6's rule that a card's
// column is COMPUTED, never stored. There is no Column-typed field
// anywhere else in this package that a caller could set instead of calling
// Derive.
type Column string

const (
	Backlog    Column = "Backlog"
	InProgress Column = "In progress"
	InReview   Column = "In review"
	Blocked    Column = "Blocked"
	Done       Column = "Done"
)

// Columns is every column, left to right, in board order.
var Columns = []Column{Backlog, InProgress, InReview, Blocked, Done}

// AgeWarnThreshold is how long a card sits unmoved before the view flags
// it. Two hours, not an arbitrary round number: as#95 sat CONFLICTING for
// two hours before a human noticed by reading a report (agent-tui#6's own
// motivating example for "ageing"), so that is the bar a card must clear
// before this board would have caught it sooner than that report did.
const AgeWarnThreshold = 2 * time.Hour

// Aged reports whether this card has looked the way it looks right now for
// at least AgeWarnThreshold -- "a card that has not moved should say so."
func (c Card) Aged() bool { return c.Age >= AgeWarnThreshold }

// blockedLaneStates is the "lane menu-blocked/hung" half of the Blocked
// rule (agent-tui#6's table). Named here as its own slice, not a literal
// inline the derivation switches on, so a test can assert against it
// directly (card_test.go does) rather than re-deriving the same list.
var blockedLaneStates = map[string]bool{
	"menu-blocked": true,
	"hung":         true,
}

// Card is one projected row of the board: one GitHub issue, plus whatever
// PR and ledger task currently relate to it. Every field is a plain fact
// or a value derived from plain facts in this same call -- see Derive.
type Card struct {
	Repo   Repo
	Number int
	Title  string
	URL    string

	Column        Column
	BlockedReason string // why Column == Blocked; empty otherwise

	// Ledger linkage. Lane/Session are empty when no ledger task currently
	// covers this issue (Backlog, or a Done issue dispatched so long ago its
	// tasks row is gone).
	Lane    string
	Session string // the part of Lane before ":" -- lanes.sh's own session:window naming

	// PR linkage. PRNumber is 0 when no PR's closingIssuesReferences names
	// this issue.
	PRNumber         int
	PRURL            string
	MergeStateStatus string

	// Timestamps. Zero means "unknown", never "now" -- a caller computing
	// age from a zero time gets a huge, obviously-wrong duration rather
	// than a plausible-looking small one.
	DispatchedAt    time.Time // ledger tasks.created_at, zero if never dispatched
	EnteredColumnAt time.Time // best-effort proxy for "since when has this been true" -- see below
	CompletedAt     time.Time // ledger tasks.completed_at or the PR's mergedAt/closedAt, zero unless Done

	// Age is now - EnteredColumnAt: how long this card has looked the way
	// it looks right now. CycleTime is now - DispatchedAt for everything
	// still in flight, or CompletedAt - DispatchedAt once Done -- "cycle
	// time, from the ledger's own timestamps" (agent-tui#6 item 2). Both
	// are zero when their source timestamp is zero (Backlog has never been
	// dispatched, so CycleTime is always zero there).
	Age       time.Duration
	CycleTime time.Duration
}

// laneStates indexes live lane.Lane rows (the same "lanes" MCP payload the
// rail already fetches) by name, for Derive's lane-state lookups.
func laneStates(lanes []lane.Lane) map[string]lane.Lane {
	m := make(map[string]lane.Lane, len(lanes))
	for _, l := range lanes {
		m[l.Name] = l
	}
	return m
}

// bestTaskRow picks, among rows dispatched against the same (repo, issue
// number), the most recently updated one -- record_dispatch never
// overwrites a prior row for a retried issue, it inserts a new task id
// (as#127's reconciler docstring), so more than one row can name the same
// source_ref; the freshest is the one describing current work.
func bestTaskRow(rows []TaskRow, repo Repo, number int) (TaskRow, bool) {
	numStr := strconv.Itoa(number)
	var best TaskRow
	found := false
	for _, r := range rows {
		if r.SourceKind != "issue" || r.Number != numStr {
			continue
		}
		if !strings.EqualFold(r.Repo.Owner, repo.Owner) || !strings.EqualFold(r.Repo.Name, repo.Name) {
			continue
		}
		if !found || r.UpdatedAt > best.UpdatedAt {
			best = r
			found = true
		}
	}
	return best, found
}

// bestPR picks, among PRs in the same repo whose closingIssuesReferences
// names this issue, an open one if any exist (that is the one currently
// governing the card's column), else the most recently updated closed or
// merged one (for Done cards, so CompletedAt has a real value).
func bestPR(prs []PR, number int) (PR, bool) {
	var openPR, latestOther PR
	haveOpen, haveOther := false, false
	for _, p := range prs {
		if !p.ClosesIssue(number) {
			continue
		}
		if p.State == "OPEN" {
			if !haveOpen || p.UpdatedAt > openPR.UpdatedAt {
				openPR = p
				haveOpen = true
			}
			continue
		}
		if !haveOther || p.UpdatedAt > latestOther.UpdatedAt {
			latestOther = p
			haveOther = true
		}
	}
	if haveOpen {
		return openPR, true
	}
	if haveOther {
		return latestOther, true
	}
	return PR{}, false
}

// parseTime parses a gh --json timestamp (RFC3339) or an empty string,
// which becomes the zero time rather than an error -- gh omits closedAt,
// mergedAt etc. for rows they don't apply to, and that omission is a fact
// ("never closed"), not a parse failure.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func unixOrZero(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

func unixPtrOrZero(sec *int64) time.Time {
	if sec == nil {
		return time.Time{}
	}
	return unixOrZero(*sec)
}

// laneSession returns the part of a lane name before its first ":" --
// lanes.sh names every lane "<session>:<window>" (README.md, lane.go).
// A name with no colon (shouldn't happen against a real lanes.sh, but
// Derive must not panic on it) is its own session.
func laneSession(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[:i]
	}
	return name
}

// Derive projects one board's worth of Cards from plain facts. now is
// passed in, never read from time.Now() internally, so Age/CycleTime are
// reproducible in tests. issuesByRepo/prsByRepo are keyed by
// Repo.GitHubID(). taskRows and lanes are exactly what ledger.go and the
// existing "lanes" MCP fetch already produce -- Derive does no I/O itself.
func Derive(now time.Time, repos []Repo, issuesByRepo map[string][]Issue, prsByRepo map[string][]PR, taskRows []TaskRow, lanes []lane.Lane) []Card {
	states := laneStates(lanes)
	var cards []Card

	for _, repo := range repos {
		issues := issuesByRepo[repo.GitHubID()]
		prs := prsByRepo[repo.GitHubID()]

		for _, issue := range issues {
			card := Card{
				Repo:   repo,
				Number: issue.Number,
				Title:  issue.Title,
				URL:    issue.URL,
			}

			taskRow, haveTask := bestTaskRow(taskRows, repo, issue.Number)
			if haveTask {
				card.Lane = taskRow.Lane
				card.Session = laneSession(taskRow.Lane)
				card.DispatchedAt = unixOrZero(taskRow.CreatedAt)
			}

			pr, havePR := bestPR(prs, issue.Number)
			if havePR {
				card.PRNumber = pr.Number
				card.PRURL = pr.URL
				card.MergeStateStatus = pr.MergeStateStatus
			}

			var laneState lane.Lane
			haveLaneState := false
			if haveTask && taskRow.Lane != "" {
				laneState, haveLaneState = states[taskRow.Lane]
			}
			laneBlocked := haveLaneState && blockedLaneStates[laneState.State]

			taskOpen := haveTask && taskRow.TaskStatus != "" &&
				taskRow.TaskStatus != "complete" && taskRow.TaskStatus != "failed" && taskRow.TaskStatus != "cancelled"

			switch {
			case issue.State == "CLOSED" || (havePR && pr.State == "MERGED"):
				card.Column = Done
				card.CompletedAt = unixPtrOrZero(taskRow.CompletedAt)
				if card.CompletedAt.IsZero() {
					card.CompletedAt = parseTime(pr.MergedAt)
				}
				if card.CompletedAt.IsZero() {
					card.CompletedAt = parseTime(pr.ClosedAt)
				}
				if card.CompletedAt.IsZero() {
					card.CompletedAt = parseTime(issue.ClosedAt)
				}
				card.EnteredColumnAt = card.CompletedAt

			case havePR && pr.State == "OPEN" && pr.MergeStateStatus == "CONFLICTING":
				card.Column = Blocked
				card.BlockedReason = "PR #" + strconv.Itoa(pr.Number) + " is conflicting"
				card.EnteredColumnAt = parseTime(pr.UpdatedAt)

			case havePR && pr.State == "OPEN" && laneBlocked:
				card.Column = Blocked
				card.BlockedReason = "lane " + taskRow.Lane + " is " + laneState.State
				card.EnteredColumnAt = parseTime(pr.UpdatedAt)

			case havePR && pr.State == "OPEN":
				card.Column = InReview
				card.EnteredColumnAt = parseTime(pr.CreatedAt)

			case taskOpen && laneBlocked:
				card.Column = Blocked
				card.BlockedReason = "lane " + taskRow.Lane + " is " + laneState.State
				card.EnteredColumnAt = unixOrZero(taskRow.UpdatedAt)

			case taskOpen:
				card.Column = InProgress
				card.EnteredColumnAt = card.DispatchedAt

			default:
				card.Column = Backlog
				card.EnteredColumnAt = parseTime(issue.CreatedAt)
			}

			if !card.EnteredColumnAt.IsZero() {
				card.Age = now.Sub(card.EnteredColumnAt)
			}
			switch {
			case card.Column == Done && !card.DispatchedAt.IsZero() && !card.CompletedAt.IsZero():
				card.CycleTime = card.CompletedAt.Sub(card.DispatchedAt)
			case card.Column != Done && !card.DispatchedAt.IsZero():
				card.CycleTime = now.Sub(card.DispatchedAt)
			}

			cards = append(cards, card)
		}
	}

	sort.SliceStable(cards, func(i, j int) bool {
		if cards[i].Repo.GitHubID() != cards[j].Repo.GitHubID() {
			return cards[i].Repo.GitHubID() < cards[j].Repo.GitHubID()
		}
		return cards[i].Number < cards[j].Number
	})
	return cards
}
