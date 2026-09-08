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
	// Context is the sanitized window of what was being discussed when the
	// operator said this, carried from the corpus prompt. It is REQUIRED for a
	// projection to be usable: a parameter without its context is a sentence an
	// agent cannot interpret -- "A sanitized version of the surrounding context
	// must be stored with the parameter in agent memory, so an agent reading
	// the note understands what it means" (corpus it-ad6b9208e64ff82, hard).
	//
	// It is rendered by the producer rather than preserved by notemeta.Merge,
	// because Merge only carries tags and a Relations section forward. Context
	// added out of band was silently destroyed on the next regeneration --
	// measured 2026-09-07, 3,217 notes lost it in one pass. Deriving it from
	// the corpus every time makes that unlosable.
	Context string `json:"context"`
}
type Result struct {
	Changed int               `json:"changed"`
	Counts  map[string]int    `json:"counts"`
	Mapping map[string]string `json:"mapping"`
}

// Read uses the selections stated in all five legacy view banners, not the
// narrower live_parameters view. SQLite is opened read-only.
func Read(db string) ([]Row, error) {
	// p.context is selected, not optional: a projection without the context it
	// was said in is a sentence an agent cannot interpret (it-ad6b9208e64ff82).
	q := `select i.id item,i.prompt_id prompt,p.at,i.kind,i.weight,i.status,i.body,coalesce(i.resolved_to,'') title,coalesce(p.context,'') context from items i join prompts p on p.id=i.prompt_id where i.weight='hard' and i.kind in ('parameter','correction','directive','question','thought') order by p.at,i.id`
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
// oneLine collapses whitespace and truncates by RUNE. Truncating by byte
// split a multi-byte character mid-sequence and produced files the vault
// validator could not read at all -- a whole-file failure from a one-byte
// slice, measured 2026-09-07.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// firstClause reduces a statement to something that reads as a title: its
// first sentence or clause, capped. Returns "" when the body cannot yield one,
// so the caller can fall back rather than emit a fragment.
func firstClause(body string) string {
	s := oneLine(body, 400)
	for _, sep := range []string{". ", " -- ", " — ", "; "} {
		if i := strings.Index(s, sep); i > 25 && i < 96 {
			s = s[:i]
			break
		}
	}
	s = strings.TrimRight(s, " .;:—-")
	if r := []rune(s); len(r) > 95 {
		cut := string(r[:92])
		if j := strings.LastIndex(cut, " "); j > 0 {
			cut = cut[:j]
		}
		s = cut + "..."
	}
	if len([]rune(s)) < 12 {
		return ""
	}
	return s
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
		// A note id is a real timestamp: YYYYMMDDHHMMSS, per inmaps-spec §8's
		// own "pure timestamp ID" line and the Luhmann stable-address
		// rationale it cites. The earlier YYYYMMDD + 4-digit-sequence form
		// (the same section's later, contradicting line) produced ids whose
		// last four digits ran past 59 and were not times at all. Several
		// items derive from one prompt and therefore share a second, so a
		// collision walks the second forward -- deterministic, still a valid
		// timestamp, and stable because ids are never renamed once assigned.
		id := r.Mapping[row.Item]
		if id == "" {
			for t := stamp; ; t = t.Add(time.Second) {
				candidate := t.Format("20060102150405")
				if !used[candidate] {
					id = candidate
					break
				}
			}
			used[id] = true
			r.Mapping[row.Item] = id
		}
		status := "stable"
		standing := row.Kind != "question" && row.Kind != "thought"
		spentDirective := false
		if row.Status == "dropped" {
			status = "deprecated"
			standing = false
		} else if row.Status == "needs_review" || !standing {
			status = "draft"
			standing = false
		} else if row.Kind == "directive" && row.Status == "acted" {
			// A directive is a one-time order, not a durable constraint. Once
			// the corpus itself records it as acted on, it is spent -- the
			// task happened, it did not become law. Tagging it standing-rule
			// anyway is exactly how 1,237 of the vault's 2,862 standing-rule
			// notes turned out to be finished errands instead of live rules
			// (measured 2026-09-07), leaving the tag unable to distinguish
			// anything. Status stays "stable": the record itself is accurate
			// and won't change, so this is not corrected or under review --
			// only its authority is scoped down.
			standing = false
			spentDirective = true
		}
		tags := []string{"note", stamp.Format("01-2006")}
		if standing {
			tags = append(tags, "standing-rule")
		}
		body := regexp.MustCompile(`(^|[\s(])#([A-Za-z0-9_]+)`).ReplaceAllString(row.Body, `${1}\#${2}`)
		// A title names the subject. It used to fall back to "<Kind> <item-id>"
		// -- "Directive it-b29425780b4cd06c" -- which names nothing a reader or
		// an agent can act on, and made every hub entry unreadable. Fall back to
		// the statement itself; the item id is already in corpus_item, so
		// nothing is lost by not repeating it as a title.
		title := row.Title
		if title == "" {
			title = firstClause(body) // escaped body: a title is rendered inline too
		}
		if title == "" {
			title = kinds[row.Kind] + " " + row.Item
		}
		// The description restates the rule itself. It used to read "Corpus
		// <kind>; consult the cited item and source prompt for authority",
		// which told a reader nothing and made every note look identical in
		// any list -- the notes Jon called junk because "they all say the same
		// bullshit".
		desc := oneLine(body, 280) // escaped body, not row.Body: an inline #tag in a
		// description is still an inline tag to Obsidian, and the test that caught
		// this exists because an unescaped one silently creates a phantom tag.
		if desc == "" {
			desc = "Corpus " + row.Kind + "; consult the cited item and source prompt for authority."
		}
		s := fmt.Sprintf("---\ntype: %s\ntitle: %s\ndescription: %s\ntags: [%s]\nid: %s\ncorpus_item: %s\nprompt_id: %s\ncreated: %s\nupdated: %s\nsource: %s\n%s\nstatus: %s\ncorpus_status: %s\nweight: %s\n---\n\n# %s\n\n%s\n", kinds[row.Kind], quote(title), quote(desc), strings.Join(tags, ", "), quote(id), quote(row.Item), quote(row.Prompt), stamp.Format(time.RFC3339), stamp.Format(time.RFC3339), quote("corpus:item:"+row.Item+"; prompt:"+row.Prompt), marker, status, quote(row.Status), quote(row.Weight), strings.ReplaceAll(title, "#", "\\#"), body)
		if c := oneLine(row.Context, 400); c != "" {
			s += "\n## Context\n\nWhat was being discussed when this was said, from the source session:\n\n> " + c + "\n"
		}
		s += fmt.Sprintf("\n## Provenance\n\n- corpus item `%s` (%s, %s)\n- source prompt `%s`\n", row.Item, row.Kind, row.Status, row.Prompt)
		if spentDirective {
			s += "\n## Scope\n\nThis is a one-time task instruction, not an ongoing rule -- it may already be resolved. Whether it still applies is a question for the corpus item cited above, not this note.\n"
		}
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
	// No index.md here. Routing for these notes is the generated topic hubs in
	// "02 - MOCs" -- a second index inside the notes directory duplicates that
	// hub, and Jon's recorded parameter is that per-area index files become
	// title-named MOC hubs rather than files all called index.md. Writing both
	// produced two competing routing surfaces over the same notes.
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
