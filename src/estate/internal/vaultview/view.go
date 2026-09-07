// Package vaultview projects the five hard-item selections recorded in the
// legacy parameter views. The corpus remains authoritative; no transcript is copied.
package vaultview

import (
	"encoding/json"
	"fmt"
	"github.com/jonhill90/agent-estate/estate/internal/notemeta"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const NotesDir = "01 - Notes/01p - Parameters"
const marker = "generated: process:vault-view"

type Row struct {
	Item   string `json:"item"`
	Prompt string `json:"prompt"`
	At     int64  `json:"at"`
	Kind   string `json:"kind"`
	Weight string `json:"weight"`
	Status string `json:"status"`
	Body   string `json:"body"`
	Title  string `json:"title"`
}
type Result struct {
	Changed int               `json:"changed"`
	Counts  map[string]int    `json:"counts"`
	Mapping map[string]string `json:"mapping"`
}

// Read uses the selections stated in all five legacy view banners, not the
// narrower live_parameters view. SQLite is opened read-only.
func Read(db string) ([]Row, error) {
	q := `select i.id item,i.prompt_id prompt,p.at,i.kind,i.weight,i.status,i.body,coalesce(i.resolved_to,'') title from items i join prompts p on p.id=i.prompt_id where i.weight='hard' and i.kind in ('parameter','correction','directive','question','thought') order by p.at,i.id`
	b, err := exec.Command("sqlite3", "-json", "file:"+db+"?mode=ro", q).Output()
	if err != nil {
		return nil, fmt.Errorf("read corpus: %w", err)
	}
	var rows []Row
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, fmt.Errorf("selection empty")
	}
	err = json.Unmarshal(b, &rows)
	return rows, err
}
func field(raw, key string) string {
	for _, line := range strings.Split(raw, "\n") {
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), "\"")
		}
	}
	return ""
}
func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// Write preserves assigned IDs by corpus_item, refuses unmanaged targets and
// computes the entire write set before touching files. Repeating unchanged input
// writes zero files. Callers own batch backups and validation before retirement.
func Write(vault string, rows []Row) (Result, error) { return write(vault, rows, true) }

// WriteBatch updates a deterministic migration prefix without withdrawing rows
// outside that prefix. Only full regeneration can retire missing corpus items.
func WriteBatch(vault string, rows []Row) (Result, error) { return write(vault, rows, false) }
func write(vault string, rows []Row, retireMissing bool) (Result, error) {
	r := Result{Counts: map[string]int{}, Mapping: map[string]string{}}
	if vault == "" {
		return r, fmt.Errorf("vault required")
	}
	dir := filepath.Join(vault, NotesDir)
	old := map[string]string{}
	used := map[string]bool{}
	rootNotes, err := os.ReadDir(filepath.Join(vault, "01 - Notes"))
	if err != nil && !os.IsNotExist(err) {
		return r, err
	}
	for _, e := range rootNotes {
		if regexp.MustCompile(`^\d{12}\.md$`).MatchString(e.Name()) {
			used[strings.TrimSuffix(e.Name(), ".md")] = true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return r, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			return r, err
		}
		s := string(b)
		old[e.Name()] = s
		if e.Name() == "index.md" {
			if !strings.Contains(s, marker) {
				return r, fmt.Errorf("unmanaged index: %s", p)
			}
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		used[id] = true
		if !strings.Contains(s, "\n"+marker+"\n") {
			return r, fmt.Errorf("unmanaged note: %s", p)
		}
		item := field(s, "corpus_item")
		if item == "" || r.Mapping[item] != "" || field(s, "id") != id {
			return r, fmt.Errorf("invalid/duplicate identity: %s", p)
		}
		r.Mapping[item] = id
	}
	rows = append([]Row(nil), rows...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].At == rows[j].At {
			return rows[i].Item < rows[j].Item
		}
		return rows[i].At < rows[j].At
	})
	seen := map[string]bool{}
	writes := map[string]string{}
	kinds := map[string]string{"parameter": "Parameter", "directive": "Directive", "correction": "Correction", "question": "Question", "thought": "Thought"}
	for _, row := range rows {
		if seen[row.Item] || row.Item == "" || kinds[row.Kind] == "" || row.Weight != "hard" {
			return r, fmt.Errorf("invalid row %q", row.Item)
		}
		seen[row.Item] = true
		r.Counts[row.Kind]++
		stamp := time.Unix(row.At, 0).UTC()
		day := stamp.Format("20060102")
		id := r.Mapping[row.Item]
		if id == "" {
			for n := 1; n <= 9999; n++ {
				candidate := day + fmt.Sprintf("%04d", n)
				if !used[candidate] {
					id = candidate
					break
				}
			}
			if id == "" {
				return r, fmt.Errorf("day ID space exhausted: %s", day)
			}
			used[id] = true
			r.Mapping[row.Item] = id
		}
		status := "stable"
		standing := row.Kind != "question" && row.Kind != "thought"
		if row.Status == "dropped" {
			status = "deprecated"
			standing = false
		} else if row.Status == "needs_review" || !standing {
			status = "draft"
			standing = false
		}
		tags := []string{"note", stamp.Format("01-2006")}
		if standing {
			tags = append(tags, "standing-rule")
		}
		title := row.Title
		if title == "" {
			title = kinds[row.Kind] + " " + row.Item
		}
		body := regexp.MustCompile(`(^|[\s(])#([A-Za-z0-9_]+)`).ReplaceAllString(row.Body, `${1}\#${2}`)
		s := fmt.Sprintf("---\ntype: %s\ntitle: %s\ndescription: %s\ntags: [%s]\nid: %s\ncorpus_item: %s\nprompt_id: %s\ncreated: %s\nupdated: %s\nsource: %s\n%s\nstatus: %s\ncorpus_status: %s\nweight: %s\n---\n\n# %s\n\n%s\n\nProjection of corpus item `%s`, source prompt `%s`. The corpus is authoritative. Questions and thoughts are not decisions.\n", kinds[row.Kind], quote(title), quote("Corpus "+row.Kind+"; consult the cited item and source prompt for authority."), strings.Join(tags, ", "), quote(id), quote(row.Item), quote(row.Prompt), stamp.Format(time.RFC3339), stamp.Format(time.RFC3339), quote("corpus:item:"+row.Item+"; prompt:"+row.Prompt), marker, status, quote(row.Status), quote(row.Weight), strings.ReplaceAll(title, "#", "\\#"), body, row.Item, row.Prompt)
		s, err = notemeta.Merge(s, old[id+".md"])
		if err != nil {
			return r, err
		}
		writes[id+".md"] = s
	}
	// A row removed from selection remains addressable but never active.
	for item, id := range r.Mapping {
		if !seen[item] {
			s := old[id+".md"]
			if retireMissing {
				s = regexp.MustCompile(`(?m)^status: .*`).ReplaceAllString(s, "status: deprecated")
			}
			writes[id+".md"] = s
		}
	}
	ids := make([]string, 0, len(r.Mapping))
	for _, id := range r.Mapping {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	index := "---\ntype: reference\ntitle: Corpus item projections\ndescription: Routing to derived corpus items\ncreated: 2026-09-07T00:00:00Z\nsource: process:vault-view\n" + marker + "\n---\n\n# Corpus item projections\n\nDerived from the corpus; draft questions are not accepted decisions.\n\n"
	for _, id := range ids {
		index += "- [[" + id + "]]\n"
	}
	writes["index.md"] = index
	if err := os.MkdirAll(dir, 0700); err != nil {
		return r, err
	}
	names := make([]string, 0, len(writes))
	for name := range writes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if old[name] == writes[name] {
			continue
		}
		p := filepath.Join(dir, name)
		f, err := os.CreateTemp(dir, ".projection-")
		if err != nil {
			return r, err
		}
		tmp := f.Name()
		_, err = f.WriteString(writes[name])
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(tmp, p)
		}
		if err != nil {
			os.Remove(tmp)
			return r, err
		}
		r.Changed++
	}
	return r, nil
}

// Batch selects a deterministic prefix for backed-up live migrations. Full
// regeneration is the default; a prefix is never used to retire existing rows.
func Limit(rows []Row, n string) ([]Row, error) {
	if n == "" {
		return rows, nil
	}
	v, e := strconv.Atoi(n)
	if e != nil || v < 1 {
		return nil, fmt.Errorf("invalid limit")
	}
	if v < len(rows) {
		rows = rows[:v]
	}
	return rows, nil
}
