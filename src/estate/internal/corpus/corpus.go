// Package corpus reads the operator's standing parameters.
//
// These are law. Every brief that reaches an agent is grounded in them, and a
// dispatch that cannot read them REFUSES -- an agent working without the
// parameters is exactly how a month went into a layer the corpus had already
// ruled out.
//
// The corpus lives at ~/corpus, not under ~/.local/state: it is 5,402 prompts
// and, at last measure (agent-estate#1139), 2,472 hard rows across three
// kinds -- 958 parameters, 1,341 directives, 173 corrections -- knowledge,
// not scratch space the harness reuses.
//
// Reading is done through the sqlite3 CLI rather than a driver so this stays
// dependency-free. Note the URI form: the bare -readonly flag has been
// observed failing with "unable to open database file (14)" under WAL
// contention while file:...?mode=ro succeeded on the same file seconds later.
package corpus

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Param struct {
	Key  string // resolved_to, e.g. "tooling=cli_first"
	Kind string // "parameter", "directive", or "correction" -- see Hard()
	Body string
}

func dbPath() (string, error) {
	if p := os.Getenv("ESTATE_CORPUS"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "corpus", "ledger.sqlite3"), nil
}

// Path returns the corpus database path this process will actually read --
// the same resolution Hard() and Audit() use. AGENTS.md documents this path
// in prose ("Before you ask Jon anything -- read this first"); it drifted
// out of sync with the real one once already (agent-estate#942). Exported so
// a test can check the doc against this function instead of a literal.
func Path() (string, error) {
	return dbPath()
}

const sep = "\x1f"

// hardKinds are the item kinds the operator can mark binding. 'question' and
// 'thought' stay excluded even at weight='hard' -- a still-open question or a
// stray musing is not itself the law, only a parameter, directive or
// correction ever is (agent-estate#1139's own measurement: 958 parameters,
// 1,341 directives, 173 corrections, all excluding 138 questions and 28
// thoughts).
const hardKinds = `'parameter','directive','correction'`

// excludedStatuses are the item statuses that must never be presented as law
// even at weight='hard': 'dropped' is a retired decision, 'needs_review' is
// one nobody has confirmed yet (agent-estate#1139). This is the opposite
// direction of the kind/weight filters above -- those are inclusion lists,
// this is an exclusion list, because 'acted', 'acknowledged', 'resolved' and
// 'open' are all still binding and must stay included by default.
var excludedStatuses = map[string]bool{
	"dropped":      true,
	"needs_review": true,
}

// Excluded reports how many hard rows were left out of a Hard() result
// because their status marks them as not-currently-law, broken down by
// status so an agent can tell "there is no law about X" from "the law about
// X was filtered out" (agent-estate#1139 requirement 4: absence is a typed
// value here, never silent).
type Excluded struct {
	Status string
	Count  int
}

// Hard returns every binding parameter, directive, and correction whose
// status hasn't retired or unconfirmed it, plus a report of what was left
// out and why. An error here must stop a dispatch, never be downgraded to
// "none found".
//
// This reads items directly rather than through the live_parameters view --
// that view is parameter-only (`kind = 'parameter'`) and widening it would
// also widen provenance.go's Audit(), which must stay parameter-scoped. The
// `weight != 'retracted'` guard below is redundant with `weight='hard'` today
// (weight is a three-value CHECK: hard/preference/retracted, so a retracted
// row is never also hard) -- it is kept explicit anyway so a future
// re-weighting of the enum can't silently let retracted law back in.
//
// status is intentionally NOT filtered in SQL: it is filtered in Go below,
// after the raw row count is known, so the zero-rows refusal (a corpus that
// cannot be read must never read as "no constraints") is judged against what
// the database actually returned, not against what survived the status
// filter. A hard pool where every row happens to be dropped/needs_review is
// a real, reportable state, not blindness.
func Hard() ([]Param, []Excluded, error) {
	p, err := dbPath()
	if err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(p); err != nil {
		return nil, nil, fmt.Errorf("corpus unreadable at %s: %w", p, err)
	}
	q := `select coalesce(resolved_to,''), kind, status, replace(replace(body, char(10), ' '), char(13), ' ')
	      from items where weight='hard' and weight != 'retracted' and kind in (` + hardKinds + `)`
	cmd := exec.Command("sqlite3", "-separator", sep, "file:"+p+"?mode=ro&immutable=1", q)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("corpus query failed: %w", err)
	}
	var ps []Param
	excludedCount := map[string]int{}
	rows := 0
	s := bufio.NewScanner(strings.NewReader(string(out)))
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		parts := strings.SplitN(s.Text(), sep, 4)
		if len(parts) != 4 {
			continue
		}
		rows++
		status := strings.TrimSpace(parts[2])
		if excludedStatuses[status] {
			excludedCount[status]++
			continue
		}
		ps = append(ps, Param{
			Key:  strings.TrimSpace(parts[0]),
			Kind: strings.TrimSpace(parts[1]),
			Body: strings.TrimSpace(parts[3]),
		})
	}
	if err := s.Err(); err != nil {
		return nil, nil, err
	}
	if rows == 0 {
		// An empty result from a database that exists is blindness, not an
		// operator with no opinions. Refuse.
		return nil, nil, fmt.Errorf("corpus returned zero hard rows -- refusing to treat that as 'no constraints'")
	}
	var excluded []Excluded
	for _, status := range []string{"dropped", "needs_review"} {
		if n := excludedCount[status]; n > 0 {
			excluded = append(excluded, Excluded{Status: status, Count: n})
		}
	}
	return ps, excluded, nil
}

// Grounding renders the preamble prepended to every brief. Rows whose text
// matches the task are surfaced first, but the full count is always stated
// and the agent is required to query the rest itself -- a filter built from
// the task can only ever confirm the task, never stop it.
//
// Selection and cap contract (agent-estate#1139): matching is a plain
// word-overlap filter over the whole hard pool (parameters, directives,
// corrections); it never shells out to `estate knowledge query` -- that
// ladder is a separate, concurrently-changing surface and coupling this path
// to it mid-change would be a collision, not a design choice. What caps the
// rendered preamble is maxPreambleBytes, enforced below, not the item count:
// at live-corpus scale (~236KB raw across directives+corrections alone) an
// uncapped render is a dump truck, not a preamble. A "N of M shown" line
// always states what was left out.
//
// excluded reports the hard rows that were left out of ps for being
// 'dropped' or 'needs_review' (agent-estate#1139 requirement 4) -- stated
// here with the same "shown -- N left out" discipline as the byte cap below,
// so a lane can never mistake "filtered out" for "no law on this exists".
func Grounding(task string, ps []Param, excluded []Excluded) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# OPERATOR PARAMETERS -- THESE ARE LAW\n\n")
	fmt.Fprintf(&b, "There are %d binding parameters, directives, and corrections on record. They\n"+
		"are not advice, and they outrank this brief. If this task extends something\n"+
		"one of them rules out, STOP and say so rather than doing it well.\n\n", len(ps))

	if len(excluded) > 0 {
		total := 0
		parts := make([]string, len(excluded))
		for i, e := range excluded {
			total += e.Count
			parts[i] = fmt.Sprintf("%s: %d", e.Status, e.Count)
		}
		fmt.Fprintf(&b, "%d additional row(s) were excluded as not-currently-law (%s) -- retired or\n"+
			"unconfirmed decisions are not law and are not counted above.\n\n", total, strings.Join(parts, ", "))
	}

	words := strings.Fields(strings.ToLower(task))
	var hits []Param
	for _, p := range ps {
		low := strings.ToLower(p.Key + " " + p.Body)
		for _, w := range words {
			if len(w) > 4 && strings.Contains(low, w) {
				hits = append(hits, p)
				break
			}
		}
		if len(hits) >= maxMatches {
			break
		}
	}

	footer := "## Required before you act\n\n" +
		"The list above was selected by matching words in your task. A filter built\n" +
		"from the task can only confirm the task; it can never stop it. Query the\n" +
		"corpus yourself for the domain you are about to touch before you touch it.\n\n"

	if len(hits) > 0 {
		// Reserve room for the header already written, the footer still to
		// come, and the section heading below, so the byte ceiling bounds
		// the WHOLE rendered preamble, not just the matched-items block.
		budget := maxPreambleBytes - b.Len() - len(footer) - 200
		var lines []string
		used := 0
		shown := 0
		for _, p := range hits {
			line := renderHit(p)
			if used+len(line) > budget {
				break
			}
			lines = append(lines, line)
			used += len(line)
			shown++
		}
		fmt.Fprintf(&b, "## Matched to this task (%d of %d shown -- NOT the whole law)\n\n", shown, len(hits))
		for _, l := range lines {
			b.WriteString(l)
		}
		b.WriteString("\n")
	}
	b.WriteString(footer)
	return b.String()
}

// maxMatches bounds how many task-matched rows are even considered for
// rendering; maxPreambleBytes bounds what the RENDERED preamble actually
// costs, which is the number that matters -- a directive's body alone can run
// well past what 25 short parameters used to cost. maxItemBytes bounds any
// single row so one long directive can't alone exhaust the budget and starve
// every other match. These are vars, not consts, purely so
// TestGroundingCapIsLoadBearing (corpus_test.go) can raise them to prove the
// cap is enforcing something real, then restore them -- production always
// runs with the values below.
var (
	maxMatches       = 25
	maxPreambleBytes = 16 * 1024
	maxItemBytes     = 400
)

// renderHit renders one matched row as a single Markdown bullet line,
// labelling directives and corrections so the lane can see which kind of law
// it is reading -- a correction ("this was got wrong once already") carries
// different force than a parameter.
func renderHit(p Param) string {
	body := p.Body
	if len(body) > maxItemBytes {
		body = body[:maxItemBytes] + "…"
	}
	label := ""
	switch p.Kind {
	case "directive":
		label = "[directive] "
	case "correction":
		label = "[correction] "
	}
	if p.Key != "" {
		return fmt.Sprintf("- %s**%s** — %s\n", label, p.Key, body)
	}
	return fmt.Sprintf("- %s%s\n", label, body)
}
