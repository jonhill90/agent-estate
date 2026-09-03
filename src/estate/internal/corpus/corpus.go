// Package corpus reads the operator's standing parameters.
//
// These are law. Every brief that reaches an agent is grounded in them, and a
// dispatch that cannot read them REFUSES -- an agent working without the
// parameters is exactly how a month went into a layer the corpus had already
// ruled out.
//
// The corpus lives at ~/corpus, not under ~/.local/state: it is 5,402 prompts
// and 958 binding parameters -- knowledge, not scratch space the harness reuses.
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

// Hard returns every binding parameter. An error here must stop a dispatch,
// never be downgraded to "none found".
func Hard() ([]Param, error) {
	p, err := dbPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(p); err != nil {
		return nil, fmt.Errorf("corpus unreadable at %s: %w", p, err)
	}
	q := `select coalesce(resolved_to,''), replace(replace(body, char(10), ' '), char(13), ' ')
	      from live_parameters where weight='hard'`
	cmd := exec.Command("sqlite3", "-separator", sep, "file:"+p+"?mode=ro&immutable=1", q)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("corpus query failed: %w", err)
	}
	var ps []Param
	s := bufio.NewScanner(strings.NewReader(string(out)))
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		k, b, ok := strings.Cut(s.Text(), sep)
		if !ok {
			continue
		}
		ps = append(ps, Param{Key: strings.TrimSpace(k), Body: strings.TrimSpace(b)})
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		// An empty result from a database that exists is blindness, not an
		// operator with no opinions. Refuse.
		return nil, fmt.Errorf("corpus returned zero hard parameters -- refusing to treat that as 'no constraints'")
	}
	return ps, nil
}

// Grounding renders the preamble prepended to every brief. Parameters whose
// text matches the task are surfaced first, but the full count is always
// stated and the agent is required to query the rest itself -- a filter built
// from the task can only ever confirm the task, never stop it.
func Grounding(task string, ps []Param) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# OPERATOR PARAMETERS -- THESE ARE LAW\n\n")
	fmt.Fprintf(&b, "There are %d binding parameters on record. They are not advice, and they\n"+
		"outrank this brief. If this task extends something a parameter rules out,\n"+
		"STOP and say so rather than doing it well.\n\n", len(ps))

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
		if len(hits) >= 25 {
			break
		}
	}
	if len(hits) > 0 {
		fmt.Fprintf(&b, "## Matched to this task (%d of %d shown -- NOT the whole law)\n\n", len(hits), len(ps))
		for _, p := range hits {
			if p.Key != "" {
				fmt.Fprintf(&b, "- **%s** — %s\n", p.Key, p.Body)
			} else {
				fmt.Fprintf(&b, "- %s\n", p.Body)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("## Required before you act\n\n" +
		"The list above was selected by matching words in your task. A filter built\n" +
		"from the task can only confirm the task; it can never stop it. Query the\n" +
		"corpus yourself for the domain you are about to touch before you touch it.\n\n")
	return b.String()
}
