// Command distillpromote builds the facts layer the vault has never had.
//
// The problem it solves, measured 2026-09-07: 3,070 parameter notes against
// 119 facts, and zero rows in the links table. Every prompt became a note and
// nothing was ever distilled, so an agent reading memory faces 3,070 pieces of
// evidence and no statement of what is actually true.
//
// Lexical clustering (internal/distill) finds only near-verbatim repeats --
// measured 6.2% coverage at its loosest useful setting. So this takes the
// other route: for each SUBJECT in the governed vocabulary, it selects the
// parameters that belong to that subject and promotes the most central of
// them to a fact, linking the rest as its evidence.
//
// Centrality, not novelty: the promoted parameter is the one whose terms
// overlap the subject's shared vocabulary most. That is the item that says
// what the others say, rather than a variant with extra specifics.
//
// It never invents wording. A fact's statement is always the verbatim body of
// a real corpus item, so the facts layer cannot contain a sentence Jon did not
// cause to be written. Everything else on the note -- subject, evidence links,
// provenance -- is mechanical.
//
// agent-estate#1339: before promoting, it checks the vault as it actually
// stands, not just its own prior output. vault-view (internal/vaultview)
// publishes a Parameter note per hard corpus item independently of this
// command, with zero cross-awareness in either direction -- measured on the
// live vault, 217 duplicate clusters (442 files) are exactly this shape.
// existing.go's dedupe skips a (corpus_item, subject) pair this command has
// already promoted (a prior run's own output, an exact repeat with nothing
// new to say), but does NOT refuse merely because a Parameter note or a
// Fact under a DIFFERENT subject already exists for the same corpus_item --
// see the issue's own "do not merge your own PRs" case: promoted once under
// "lane" and once under "merge", each with real, distinct evidence, and
// neither redundant with the other. Publishing anyway, with the sibling
// note(s) named under "Related notes" rather than silently duplicated, is
// this command's answer to that -- it does not retire or edit anything else
// in the vault; that is a migration's job, out of scope here.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/corpus"
	"github.com/jonhill90/agent-estate/estate/internal/distill"
)

const sep = "\x1f"

type fact struct {
	Subject  string
	Chosen   distill.Item
	Evidence []distill.Item
	// Related is every OTHER existing vault note (Parameter or Fact) already
	// on file for this same corpus_item, discovered by dedupe (agent-estate#1339)
	// -- never nil-vs-empty-significant, just the ids to cite. Populated
	// before render() so the written note names its own siblings instead of
	// silently duplicating them uncredited.
	Related []string
}

func main() {
	vault := flag.String("vault", os.Getenv("AGENT_MEMORY_VAULT"), "vault root")
	perSubject := flag.Int("per-subject", 3, "facts to promote per subject")
	minEvidence := flag.Int("min-evidence", 3, "subject needs at least this many parameters")
	apply := flag.Bool("apply", false, "write the notes; default is a dry run")
	flag.Parse()

	if *vault == "" {
		fmt.Fprintln(os.Stderr, "AGENT_MEMORY_VAULT is not set")
		os.Exit(1)
	}
	vocab, err := readVocab(*vault)
	if err != nil {
		fmt.Fprintln(os.Stderr, "vocabulary:", err)
		os.Exit(1)
	}
	items, err := readItems()
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(1)
	}

	// A parameter belongs to a subject when its body names that subject. An
	// item can belong to several -- a rule about reviewing a merge is about
	// both -- and that is correct: the same rule is reachable from either.
	bySubject := map[string][]distill.Item{}
	for _, s := range vocab {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(s) + `[a-z]*\b`)
		for _, it := range items {
			if re.MatchString(strings.ToLower(it.Body)) {
				bySubject[s] = append(bySubject[s], it)
			}
		}
	}

	var facts []fact
	subjects := make([]string, 0, len(bySubject))
	for s := range bySubject {
		subjects = append(subjects, s)
	}
	sort.Strings(subjects)

	// Dedup by STATEMENT, not by item id. The corpus contains distinct items
	// with identical bodies -- the same prompt reaching two agents produces two
	// items -- so keying on id promoted the same sentence three times under one
	// subject. Normalised body is the real identity of a fact.
	promoted := map[string]bool{}
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	for _, s := range subjects {
		group := bySubject[s]
		if len(group) < *minEvidence {
			continue
		}
		for _, c := range central(group, *perSubject) {
			if promoted[norm(c.Body)] {
				continue // a rule spanning two subjects is stated once
			}
			promoted[norm(c.Body)] = true
			ev := make([]distill.Item, 0, len(group))
			seenEv := map[string]bool{norm(c.Body): true}
			for _, m := range group {
				if seenEv[norm(m.Body)] {
					continue // evidence lists distinct statements, not repeats
				}
				seenEv[norm(m.Body)] = true
				ev = append(ev, m)
			}
			if len(ev) > 12 {
				ev = ev[:12] // a fact cites its evidence, it does not reprint the corpus
			}
			facts = append(facts, fact{Subject: s, Chosen: c, Evidence: ev})
		}
	}

	// agent-estate#1339: dedup against the vault as it actually stands, not
	// just against this run's own output. Two independent mechanisms write
	// into the vault -- vault-view publishes a Parameter note per hard
	// corpus item, unconditionally; this command promotes a subset of that
	// same population to Fact notes -- and until now neither checked the
	// other. See existing.go's own doc comment for the measured overlap.
	existingP, err := existingParams(*vault)
	if err != nil {
		fmt.Fprintln(os.Stderr, "existing parameters:", err)
		os.Exit(1)
	}
	existingF, err := existingFacts(*vault)
	if err != nil {
		fmt.Fprintln(os.Stderr, "existing facts:", err)
		os.Exit(1)
	}
	facts, skipped := dedupe(facts, existingP, existingF)

	fmt.Printf("subjects with enough evidence: %d\n", len(subjects))
	fmt.Printf("facts to promote:              %d\n", len(facts)+skipped)
	fmt.Printf("already promoted (same corpus_item, same subject) -- skipped: %d\n", skipped)
	related := 0
	for _, f := range facts {
		if len(f.Related) > 0 {
			related++
		}
	}
	fmt.Printf("publishing alongside an existing Parameter/Fact note for the same corpus_item: %d\n", related)
	if !*apply {
		fmt.Println("\ndry run -- nothing written. sample:")
		for i, f := range facts {
			if i >= 8 {
				break
			}
			rel := ""
			if len(f.Related) > 0 {
				rel = fmt.Sprintf(" (related: %s)", strings.Join(f.Related, ", "))
			}
			fmt.Printf("\n  [%s] %s\n    %s\n    (+%d evidence)%s\n", f.Subject, f.Chosen.ID, trunc(f.Chosen.Body, 120), len(f.Evidence), rel)
		}
		return
	}

	dir := filepath.Join(*vault, "01 - Notes", "01f - Facts")
	used := existingIDs(*vault)
	written := 0
	for _, f := range facts {
		id := nextID(f.Chosen.At, used)
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(render(id, f)), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
		written++
	}
	fmt.Printf("facts written: %d\n", written)
}

// dedupe is agent-estate#1339's own check: before writing a NEW Fact for
// (corpus_item, subject), has this exact pair already been published?
//
// Two different answers for two different overlaps, deliberately not the
// same rule -- a naive "one note per corpus_item" would destroy real
// content (see this command's own comment above the fact struct, and the
// issue's own "do not merge your own PRs" worked example: two Facts,
// SUBJECT "lane" and SUBJECT "merge", sharing one corpus_item, each with
// its own real, distinct evidence list; neither is redundant with the
// other, only their headline/description collide):
//
//   - An existing FACT for the SAME corpus_item AND the SAME subject is an
//     exact repeat -- a prior run's own output, or (today, since this tool
//     has never run with -apply against the live vault) the pre-#1290
//     pipeline's output landing on the identical (item, subject) pair this
//     run would also choose. There is no new information in writing it
//     again: SKIPPED, counted, never silent.
//   - An existing Fact for the SAME corpus_item under a DIFFERENT subject,
//     or an existing PARAMETER note for the same corpus_item at all (94% of
//     candidates, measured -- vault-view's own coverage is close to
//     universal), is NOT a reason to refuse: that Fact's evidence and
//     synthesis for THIS subject does not exist anywhere else in the vault.
//     Refusing here would make this command permanently promote nothing,
//     since virtually every candidate it ever considers already has a
//     Parameter note by construction (both draw from the same hard-item
//     population). Published anyway, with Related populated so the note
//     names what else already exists for its own corpus_item, rather than
//     silently duplicating it uncredited -- the concrete answer to "publish,
//     or mark the relationship": both, for this case.
//
// What this does NOT do: retire, edit, or merge the existing Parameter or
// Fact note this new Fact is related to -- see this repo's own vault-write
// discipline (no writes outside the file this command itself creates) and
// this PR's own report on why a migration for the 442 files already on
// disk is separate, future work. A newly-written Fact's Related field is
// exactly the input such a migration would need: for each Fact naming a
// Related Parameter note, retire that Parameter note (matching vault-view's
// own "status: deprecated" retirement convention) once its content is
// confirmed to be a strict subset of the Fact's, the same judgement the
// issue investigation made by hand for this cluster's own 190137.md.
func dedupe(facts []fact, params map[string]string, existing map[string][]factRef) (kept []fact, skipped int) {
	for _, f := range facts {
		exact := false
		var related []string
		if pid, ok := params[f.Chosen.ID]; ok {
			related = append(related, pid)
		}
		for _, ref := range existing[f.Chosen.ID] {
			if ref.Subject == f.Subject {
				exact = true
				break
			}
			related = append(related, ref.ID)
		}
		if exact {
			skipped++
			continue
		}
		f.Related = related
		kept = append(kept, f)
	}
	return kept, skipped
}

// binding words mark a statement as a RULE rather than an observation.
// Centrality alone picks the most typical sentence about a subject, which is
// not the same as the governing one: for "commit to main" it returned "never
// read a workflow from main and attribute its behaviour to an older run"
// instead of the rule that forbids committing to main at all. Imperative
// language is the cheapest available signal for which is which.
var bindingRe = regexp.MustCompile(`(?i)\b(never|always|must|do not|don't|only ever|no exceptions|is forbidden|is binding|required|shall)\b`)

// central returns the n items whose terms overlap the group's shared
// vocabulary most, preferring statements that read as rules.
func central(group []distill.Item, n int) []distill.Item {
	shared := map[string]int{}
	for _, m := range group {
		for t := range distill.Terms(m.Body) {
			shared[t]++
		}
	}
	type scored struct {
		it distill.Item
		s  float64
	}
	var ss []scored
	for _, m := range group {
		ts := distill.Terms(m.Body)
		if len(ts) == 0 {
			continue
		}
		sum := 0
		for t := range ts {
			sum += shared[t]
		}
		// Normalised so a long body does not win on length alone.
		score := float64(sum) / float64(len(ts))
		if bindingRe.MatchString(m.Body) {
			score *= 1.6 // a rule outranks an observation about the same subject
		}
		ss = append(ss, scored{m, score})
	}
	sort.Slice(ss, func(i, j int) bool {
		if ss[i].s != ss[j].s {
			return ss[i].s > ss[j].s
		}
		// Parameters outrank directives: a parameter is a standing rule.
		if (ss[i].it.Kind == "parameter") != (ss[j].it.Kind == "parameter") {
			return ss[i].it.Kind == "parameter"
		}
		return ss[i].it.ID < ss[j].it.ID
	})
	var out []distill.Item
	for i := 0; i < len(ss) && i < n; i++ {
		out = append(out, ss[i].it)
	}
	return out
}

func render(id string, f fact) string {
	at := time.Unix(f.Chosen.At, 0).UTC().Format(time.RFC3339)
	var b strings.Builder
	b.WriteString("---\ntype: Fact\n")
	fmt.Fprintf(&b, "title: %q\n", title(f.Chosen.Body))
	fmt.Fprintf(&b, "description: %q\n", oneline(f.Chosen.Body, 280))
	fmt.Fprintf(&b, "tags: [%q, \"fact\", \"distilled\"]\n", f.Subject)
	fmt.Fprintf(&b, "id: %q\ncorpus_item: %q\nprompt_id: %q\n", id, f.Chosen.ID, f.Chosen.Prompt)
	fmt.Fprintf(&b, "created: %s\nupdated: %s\n", at, at)
	fmt.Fprintf(&b, "subject: %q\nweight: \"hard\"\ncorpus_status: %q\n", f.Subject, f.Chosen.Status)
	fmt.Fprintf(&b, "source: \"corpus:item:%s; prompt:%s\"\n", f.Chosen.ID, f.Chosen.Prompt)
	b.WriteString("generated: process:distill-promote\nstatus: stable\n---\n\n")

	fmt.Fprintf(&b, "# %s\n\n%s\n\n", title(f.Chosen.Body), strings.TrimSpace(f.Chosen.Body))
	b.WriteString("## Why this is a fact and not just a note\n\n")
	fmt.Fprintf(&b, "This is the most central statement Jon made about **%s**: the one whose\n", f.Subject)
	b.WriteString("wording overlaps most with everything else he said on the subject. It is\n")
	b.WriteString("his own words, verbatim from the corpus -- nothing here was composed.\n\n")
	if len(f.Evidence) > 0 {
		fmt.Fprintf(&b, "## Evidence (%d further statements on this subject)\n\n", len(f.Evidence))
		for _, e := range f.Evidence {
			fmt.Fprintf(&b, "- %s `%s`\n", oneline(e.Body, 150), e.ID)
		}
		b.WriteString("\n")
	}
	if len(f.Related) > 0 {
		// agent-estate#1339: this corpus item is ALSO published as one or
		// more other notes (a vault-view Parameter note, and/or a Fact
		// under a different subject) -- named here rather than silently
		// duplicated. Never edited or retired by this command; see this
		// PR's own report for what a future migration would do with this.
		b.WriteString("## Related notes\n\n")
		b.WriteString("This corpus item is also published as:\n\n")
		for _, id := range f.Related {
			fmt.Fprintf(&b, "- `%s`\n", id)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Provenance\n\n")
	fmt.Fprintf(&b, "- corpus item `%s` (%s, %s)\n", f.Chosen.ID, f.Chosen.Kind, f.Chosen.Status)
	fmt.Fprintf(&b, "- source prompt `%s`, %s\n", f.Chosen.Prompt, at)
	return b.String()
}

func title(body string) string {
	s := oneline(body, 400)
	if i := strings.IndexAny(s, ".;"); i > 20 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 95 {
		cut := string(r[:92])
		if j := strings.LastIndex(cut, " "); j > 0 {
			cut = cut[:j]
		}
		s = cut + "..."
	}
	return s
}

// oneline truncates by RUNE, not by byte. Cutting mid-character produced
// invalid UTF-8 in 5 generated facts and the vault validator refused to read
// them at all -- a whole-file failure from a one-byte slice.
func oneline(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func nextID(at int64, used map[string]bool) string {
	t := time.Unix(at, 0).UTC()
	for {
		id := t.Format("20060102150405")
		if !used[id] {
			used[id] = true
			return id
		}
		t = t.Add(time.Second)
	}
}

func existingIDs(vault string) map[string]bool {
	out := map[string]bool{}
	filepath.Walk(filepath.Join(vault, "01 - Notes"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(p, ".md") {
			out[strings.TrimSuffix(filepath.Base(p), ".md")] = true
		}
		return nil
	})
	return out
}

// readVocab reads the governed associative vocabulary from the vault, so this
// cannot invent a subject Jon never governed.
func readVocab(vault string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(vault, "99 - Meta", "tags.md"))
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile("(?m)^\\| `([a-z][a-z0-9-]*)` \\|")
	var out []string
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		// "corpus" and "vault" are the subject of nearly every item; as
		// subjects they group everything and separate nothing.
		if m[1] == "corpus" || m[1] == "vault" || m[1] == "note" || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out, nil
}

func readItems() ([]distill.Item, error) {
	path, err := corpus.Path()
	if err != nil {
		return nil, err
	}
	q := `select i.id, i.kind, i.status, p.at, i.prompt_id, replace(i.body, char(10), ' ')
	  from items i join prompts p on p.id = i.prompt_id
	  where i.weight = 'hard' and i.status not in ('dropped','needs_review')
	    and i.kind = 'parameter'`
	out, err := exec.Command("sqlite3", "-separator", sep, "file:"+path+"?mode=ro&immutable=1", q).Output()
	if err != nil {
		return nil, err
	}
	var items []distill.Item
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		f := strings.Split(sc.Text(), sep)
		if len(f) != 6 {
			continue
		}
		at, _ := strconv.ParseInt(f[3], 10, 64)
		items = append(items, distill.Item{ID: f[0], Kind: f[1], Status: f[2], At: at, Prompt: f[4], Body: f[5]})
	}
	return items, nil
}
