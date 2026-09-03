package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const corpusSep = "\x1f"

// corpusSource reads every row of the corpus's own live_parameters view
// (weight != 'retracted') -- 1,104 rows at the time agent-estate#946 was
// written, NOT the internal/corpus package's Hard() (958 rows, weight =
// 'hard' only): this index means to reflect the corpus's own live set,
// not the subset a dispatch is grounded in.
//
// RAW PROMPTS NEVER LEAVE THE SOURCE. This query selects only from
// live_parameters (kind='parameter', already-derived statements, one per
// item.body) -- there is no column here, and no query anywhere in this
// package, that reaches the corpus's own `prompts` table. That is the
// hard rule this function exists to keep, not an incidental property of
// the query below.
//
// dbPath must already be a real, stat-able file; the caller (Generate)
// resolves ~/corpus/ledger.sqlite3 (agent-estate#942's own trap: CLAUDE.md
// documents the wrong path) before calling this. Opened, per the
// operator's own stated requirement, only as
// file:<path>?mode=ro&immutable=1.
func corpusSource(dbPath string, clock *idClock) (SourceResult, []Item) {
	res := SourceResult{Name: "corpus-parameters"}
	if dbPath == "" {
		res.Reason = "no corpus path configured"
		return res, nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		res.Reason = fmt.Sprintf("corpus unreadable at %s: %v", dbPath, err)
		return res, nil
	}

	q := `select id, coalesce(resolved_to,''), coalesce(weight,''), coalesce(status,''),
	      replace(replace(body, char(10), ' '), char(13), ' ')
	      from live_parameters order by id`
	cmd := exec.Command("sqlite3", "-separator", corpusSep, "file:"+dbPath+"?mode=ro&immutable=1", q)
	out, err := cmd.Output()
	if err != nil {
		res.Reason = fmt.Sprintf("corpus query failed: %v", err)
		return res, nil
	}

	var items []Item
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), corpusSep, 5)
		if len(parts) != 5 {
			continue
		}
		id, resolvedTo, weight, status, body := parts[0], parts[1], parts[2], parts[3], parts[4]
		body = strings.TrimSpace(body)
		if body == "" && resolvedTo == "" {
			continue
		}
		tier1 := resolvedTo
		if tier1 == "" {
			tier1 = truncate(body, 120)
		}
		var structural []string
		if weight != "" {
			structural = append(structural, "weight:"+weight)
		}
		if status != "" {
			structural = append(structural, "status:"+status)
		}
		items = append(items, Item{
			ID:             clock.NextID(),
			Source:         "corpus-parameter",
			Permalink:      fmt.Sprintf("corpus:item:%s", id),
			StructuralTags: structural,
			Tier1:          truncate(tier1, 200),
			Tier2:          truncate(body, 400),
			Tier3:          "the corpus's own item " + id + " (live_parameters) -- not this file",
		})
	}
	if err := sc.Err(); err != nil {
		res.Reason = fmt.Sprintf("corpus output could not be read: %v", err)
		return res, nil
	}

	res.OK = true
	res.Count = len(items)
	return res, items
}
