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

// authorityScope names, per corpus kind, what a projection actually is --
// evidence, never itself standing law. corpus.go's own principle holds
// here too: a vault tag is not proof of StandingLawSet membership
// (internal/corpus/standinglaw.go). A directive in particular is often a
// one-time task instruction, not an ongoing rule, and nothing in the
// corpus schema (kind/weight/status; there is no lifecycle column)
// distinguishes "resolved" from "still binding" -- so the fix is explicit
// scope language on every projection, not an invented resolved/unresolved
// judgment this package has no evidence to make. This replaces the old
// blanket "standing-rule" tag (agent-estate#1286's own class of risk:
// a tag reading as an authority claim, applied to every non-dropped hard
// item regardless of whether it was ever meant to bind more than once).
var authorityScope = map[string]string{
	"parameter":  "corpus parameter -- evidence, not itself standing law; see internal/corpus/standinglaw.go's StandingLawSet for what actually binds every task",
	"directive":  "corpus directive -- often a one-time task instruction, possibly already resolved; evidence, not itself standing law; see internal/corpus/standinglaw.go's StandingLawSet for what actually binds every task",
	"correction": "corpus correction -- evidence, not itself standing law; see internal/corpus/standinglaw.go's StandingLawSet for what actually binds every task",
	"question":   "corpus question -- not a decision",
	"thought":    "corpus thought -- not a decision",
}

// minSubjectWords is the deliberately conservative floor below which body
// text cannot be said to determine a subject -- SPEC §2 requires an
// unresolved fragment stay explicitly labelled rather than forced into an
// invented title. Anything at or above this is treated as readable prose
// a deterministic excerpt can honestly represent.
const minSubjectWords = 3
const maxTitleChars = 90
const maxDescChars = 220

// deriveSubject builds a readable title/description from a corpus item's
// body as a deterministic excerpt -- never an invented expansion. Body
// text too short or unclear to determine a subject returns resolved=false;
// the caller must label that explicitly, not guess.
func deriveSubject(body string) (title, desc string, resolved bool) {
	flat := strings.Join(strings.Fields(body), " ")
	if len(strings.Fields(flat)) < minSubjectWords {
		return "", "", false
	}
	title = truncateAtWord(firstSentence(flat), maxTitleChars)
	if title == "" {
		return "", "", false
	}
	desc = truncateAtWord(flat, maxDescChars)
	return title, desc, true
}

// firstSentence returns the text up to the first sentence-ending
// punctuation or newline, or the whole string if none is found.
func firstSentence(s string) string {
	if i := strings.IndexAny(s, ".!?\n"); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// truncateAtWord caps s at max characters without splitting a word --
// accurate and readable, per SPEC §2's requirement on a deterministic
// excerpt, rather than a mid-word cut.
func truncateAtWord(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "…"
}

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
		if row.Status == "dropped" {
			status = "deprecated"
		} else if row.Status == "needs_review" {
			status = "draft"
		}

		// Editorial override: a title/description a human has reviewed and
		// accepted (`editorial: reviewed` in the previous file) survives
		// regeneration verbatim -- SPEC §2's "accepted editorial title/
		// description must survive regeneration," using the same managed
		// publication mechanism rather than a new sidecar store.
		prevRaw := old[id+".md"]
		editorial := field(prevRaw, "editorial") == "reviewed"
		resolution := "resolved"
		var title, desc string
		if editorial {
			title = field(prevRaw, "title")
			desc = field(prevRaw, "description")
			if title == "" {
				editorial = false // malformed previous frontmatter -- fall through, do not trust a blank editorial title
			}
		}
		if !editorial {
			title = row.Title
			derivedTitle, derivedDesc, ok := deriveSubject(row.Body)
			switch {
			case title != "":
				// row.Title (resolved_to) is already a resolved, trusted
				// subject from upstream -- keep it, pair with a derived
				// description when the body supports one.
				if ok {
					desc = derivedDesc
				} else {
					desc = "Consult the cited corpus item directly for full context."
				}
			case ok:
				title = derivedTitle
				desc = derivedDesc
			default:
				// SPEC §2: a fragment needing context gets a reviewed
				// editorial proposal, never an invented expansion. Label
				// it, do not guess -- and exclude it from authority-bearing
				// use via both the explicit resolution field and status.
				resolution = "unresolved"
				title = "Unresolved — " + kinds[row.Kind] + " " + row.Item
				desc = "Fragment lacks a determinable subject -- unresolved, needs editorial review. Consult the cited corpus item directly."
				if status != "deprecated" {
					// Exclude from authority-bearing use without overriding
					// an explicit corpus-status retraction -- a dropped row
					// stays deprecated, it does not get promoted back to
					// draft just because its body is also a fragment.
					status = "draft"
				}
			}
		}
		if resolution == "resolved" {
			if scope, ok := authorityScope[row.Kind]; ok {
				desc = strings.TrimSpace(desc) + " (" + scope + ")"
			}
		}

		tags := []string{"note", stamp.Format("01-2006")}
		if resolution == "unresolved" {
			tags = append(tags, "needs-editorial-review")
		}
		body := regexp.MustCompile(`(^|[\s(])#([A-Za-z0-9_]+)`).ReplaceAllString(row.Body, `${1}\#${2}`)
		// No repetitive footer: provenance (corpus_item, prompt_id, source)
		// already lives once, in frontmatter -- SPEC §2.
		s := fmt.Sprintf("---\ntype: %s\ntitle: %s\ndescription: %s\ntags: [%s]\nid: %s\ncorpus_item: %s\nprompt_id: %s\ncreated: %s\nupdated: %s\nsource: %s\n%s\nstatus: %s\nresolution: %s\ncorpus_status: %s\nweight: %s\n---\n\n# %s\n\n%s\n", kinds[row.Kind], quote(title), quote(desc), strings.Join(tags, ", "), quote(id), quote(row.Item), quote(row.Prompt), stamp.Format(time.RFC3339), stamp.Format(time.RFC3339), quote("corpus:item:"+row.Item+"; prompt:"+row.Prompt), marker, status, resolution, quote(row.Status), quote(row.Weight), strings.ReplaceAll(title, "#", "\\#"), body)
		s, err = notemeta.Merge(s, prevRaw)
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
