package corpus

// Provenance review (agent-estate#1394, #1395).
//
// The repeatable form of the hand-built 20-row sample on #1394: for every
// live parameter, the rule as stored next to the prompt it was judged from,
// so the operator can answer "did I ask for this, or did you make it up?"
// himself. It presents evidence and leaves the judgement column blank on
// purpose -- a tool that decided "supported" versus "stronger than the
// source" would manufacture the false confidence this work exists to remove.
//
// Read-only. Quotes text_clean only; text_raw is never rendered unless the
// caller passes Private, and then only to a terminal. Rows whose text looks
// like a credential or a personal arrangement are withheld by id with the
// reason stated, never silently dropped, so the count is reportable and the
// row is still reachable with Private.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// ReviewItem is one live parameter as stored.
type ReviewItem struct {
	ItemID string `json:"item_id"`
	Body   string `json:"body"`
	Weight string `json:"weight"`
	Status string `json:"status"`
}

// ReviewGroup is one source prompt and every live parameter judged from it.
type ReviewGroup struct {
	PromptID string       `json:"prompt_id"`
	At       string       `json:"at"`
	Author   string       `json:"author"`
	Session  string       `json:"session,omitempty"`
	Project  string       `json:"project,omitempty"`
	Clean    string       `json:"text_clean,omitempty"`
	RawLen   int          `json:"raw_length"`
	Withheld string       `json:"withheld,omitempty"` // reason, when the row is not rendered publicly
	Hints    []string     `json:"hints,omitempty"`    // mechanical, labelled, never a verdict
	Items    []ReviewItem `json:"items"`
	raw      string       // never serialised; rendered only under Private
}

// Review is the whole artifact plus the counts that bound it.
type Review struct {
	Prompts       int           `json:"prompts"`
	Params        int           `json:"live_parameters"`
	WithClean     int           `json:"prompts_with_text_clean"`
	ParamsClean   int           `json:"live_parameters_with_text_clean"`
	WithheldCount int           `json:"prompts_withheld"`
	Groups        []ReviewGroup `json:"groups"`
}

// sensitive names what must not reach a public artifact: credential shapes,
// credential words, and the operator's own accounts or arrangements
// (CLAUDE.local.md: "anything about his accounts, credentials, or personal
// arrangements ... they are context you hold, not content you publish").
// "token usage"/"token spend" are cost vocabulary, not secrets, and are
// exempted; everything else here errs toward withholding, because a withheld
// row is still listed and reachable, while a leaked one is not recallable.
//
// "token" on its own is cost vocabulary in this corpus ("waste tokens",
// "token usage") far more often than a credential, so only credential-token
// phrases match.
var sensitive = regexp.MustCompile(`(?i)\b(sk-[a-z0-9]{6,}|ghp_[a-z0-9]{6,}|xox[abp]-[a-z0-9-]{6,}|password|passwd|api[ -]?key|secret|bearer|credential|keychain|botfather|(bearer|api|access|auth|bot|oauth|refresh|session) tokens?|friend'?s? account|my friend|switch(ed|ing)? account|claude account|copilot account|subscriptions?|\bsubs\b|hill90admin)\b`)

func sensitiveReason(texts ...string) string {
	for _, t := range texts {
		if m := sensitive.FindString(t); m != "" {
			return "matches the credential/personal-arrangement filter (" + strings.ToLower(m) + ")"
		}
	}
	return ""
}

// ReviewLive reads every live parameter and its source prompt from the
// corpus, grouped by prompt. It never writes. An empty result from a corpus
// that exists is refused as blindness, the same rule Hard() and Audit() apply.
func ReviewLive() (*Review, error) {
	p, err := dbPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(p); err != nil {
		return nil, fmt.Errorf("corpus unreadable at %s: %w", p, err)
	}
	q := `select i.id, i.weight, i.status, replace(replace(i.body,char(10),' '),char(13),' '),
	             p.id, datetime(p.at,'unixepoch'), coalesce(p.author,'unknown'),
	             coalesce(replace(replace(p.text_clean,char(10),' '),char(13),' '),''),
	             replace(replace(p.text_raw,char(10),' '),char(13),' '),
	             coalesce(p.session,''), coalesce(p.project,''), length(p.text_raw)
	      from live_parameters i join prompts p on p.id = i.prompt_id
	      order by p.at, i.id`
	out, err := exec.Command("sqlite3", "-separator", sep, "file:"+p+"?mode=ro&immutable=1", q).Output()
	if err != nil {
		return nil, fmt.Errorf("provenance review query failed: %w", err)
	}
	return parseReview(string(out))
}

func parseReview(out string) (*Review, error) {
	groups := map[string]*ReviewGroup{}
	var order []string
	params := 0
	s := bufio.NewScanner(strings.NewReader(out))
	s.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for s.Scan() {
		parts := strings.SplitN(s.Text(), sep, 12)
		if len(parts) != 12 {
			continue
		}
		params++
		pid := parts[4]
		g, ok := groups[pid]
		if !ok {
			var rawLen int
			fmt.Sscanf(parts[11], "%d", &rawLen)
			g = &ReviewGroup{PromptID: pid, At: parts[5], Author: parts[6], Clean: strings.TrimSpace(parts[7]),
				raw: strings.TrimSpace(parts[8]), Session: parts[9], Project: shortProject(parts[10]), RawLen: rawLen}
			groups[pid] = g
			order = append(order, pid)
		}
		g.Items = append(g.Items, ReviewItem{ItemID: parts[0], Weight: parts[1], Status: parts[2], Body: strings.TrimSpace(parts[3])})
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if params == 0 {
		return nil, fmt.Errorf("provenance review found zero live parameters -- refusing to report that as an empty review")
	}
	r := &Review{Prompts: len(order), Params: params}
	for _, pid := range order {
		g := groups[pid]
		texts := []string{g.raw, g.Clean}
		for _, it := range g.Items {
			texts = append(texts, it.Body)
		}
		if reason := sensitiveReason(texts...); reason != "" {
			g.Withheld = reason
			r.WithheldCount++
		}
		if g.Clean != "" {
			r.WithClean++
			r.ParamsClean += len(g.Items)
		}
		g.Hints = hints(g)
		r.Groups = append(r.Groups, *g)
	}
	// Most-consequential first: the prompt behind the most live parameters,
	// then oldest first so a reader pages through history in order.
	sort.SliceStable(r.Groups, func(a, b int) bool {
		if len(r.Groups[a].Items) != len(r.Groups[b].Items) {
			return len(r.Groups[a].Items) > len(r.Groups[b].Items)
		}
		return r.Groups[a].At < r.Groups[b].At
	})
	return r, nil
}

func shortProject(p string) string {
	if i := strings.LastIndex(p, "-"); i >= 0 && i+1 < len(p) {
		return p[i+1:]
	}
	return p
}

// hints are mechanical observations that narrow where a reader looks. Each
// is phrased as what was measured, never as a conclusion about the rule.
func hints(g *ReviewGroup) []string {
	var hs []string
	if g.Clean == "" {
		hs = append(hs, "source has no cleaned text; described, not quoted")
	}
	if strings.Contains(g.raw, "?") {
		hs = append(hs, "source contains a question mark")
	}
	if n := len(g.Items); n > 1 {
		hs = append(hs, fmt.Sprintf("%d live parameters were judged from this one source", n))
	}
	sw := words(g.raw)
	for _, it := range g.Items {
		if len(it.Body) > g.RawLen && g.RawLen > 0 {
			hs = append(hs, fmt.Sprintf("%s: rule (%d chars) is longer than its source (%d chars)", it.ItemID, len(it.Body), g.RawLen))
		}
		low := strings.ToLower(it.Body)
		var added []string
		for _, d := range directives {
			if strings.Contains(low, d) && !strings.Contains(strings.ToLower(g.raw), d) {
				added = append(added, d)
			}
		}
		if len(added) > 0 {
			hs = append(hs, fmt.Sprintf("%s: rule uses %q; the source does not", it.ItemID, strings.Join(added, ", ")))
		}
		pw := words(it.Body)
		if len(pw) > 0 && len(sw) > 0 {
			hit := 0
			for w := range pw {
				if sw[w] {
					hit++
				}
			}
			if float64(hit)/float64(len(pw)) < 0.25 {
				hs = append(hs, fmt.Sprintf("%s: fewer than a quarter of the rule's words appear in the source", it.ItemID))
			}
		}
	}
	return hs
}

// ReviewOptions bound one rendering: paging over groups, and whether raw
// text may be shown (terminal only; never in anything published).
type ReviewOptions struct {
	Offset  int
	Limit   int
	Private bool
	JSON    bool
}

// Render writes the artifact. Public output quotes text_clean only and
// withholds sensitive rows by id; Private adds text_raw and the withheld
// rows' text, marked so it cannot be mistaken for publishable.
func (r *Review) Render(w io.Writer, o ReviewOptions) error {
	if o.Limit <= 0 {
		o.Limit = 100
	}
	if o.Offset < 0 {
		o.Offset = 0
	}
	end := o.Offset + o.Limit
	if end > len(r.Groups) {
		end = len(r.Groups)
	}
	start := o.Offset
	if start > end {
		start = end
	}
	page := r.Groups[start:end]
	if o.JSON {
		type out struct {
			Review
			Offset int `json:"offset"`
			Limit  int `json:"limit"`
			Shown  int `json:"shown"`
		}
		v := out{Review: *r, Offset: start, Limit: o.Limit, Shown: len(page)}
		v.Groups = page
		if !o.Private {
			for i := range v.Groups {
				if v.Groups[i].Withheld != "" {
					v.Groups[i].Clean = ""
					v.Groups[i].Items = nil
				}
			}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	fmt.Fprintf(w, "# Provenance review -- %d source prompts behind %d live parameters\n\n", r.Prompts, r.Params)
	fmt.Fprintf(w, "Showing prompts %d-%d of %d (sorted by live parameters per source, then time). %d prompts (%d live parameters) have cleaned text and are quoted; the rest are described, not quoted. %d prompts are withheld here as credential/personal-arrangement matches and listed by id.\n\n",
		start+1, end, r.Prompts, r.WithClean, r.ParamsClean, r.WithheldCount)
	fmt.Fprintln(w, "Hints are mechanical and labelled. The judgement column -- supported / stronger than the source / not supported / cannot tell -- is yours; nothing here fills it in.")
	if o.Private {
		fmt.Fprintln(w, "\n*** PRIVATE RENDER: raw prompt text and withheld rows are included. Do not paste this anywhere public. ***")
	}
	for i, g := range page {
		fmt.Fprintf(w, "\n## %d. prompt %s -- %s -- recorded author: %s -- %d live parameter(s)", start+i+1, g.PromptID, g.At, g.Author, len(g.Items))
		if g.Project != "" {
			fmt.Fprintf(w, " -- project: %s", g.Project)
		}
		fmt.Fprintln(w)
		if g.Withheld != "" && !o.Private {
			fmt.Fprintf(w, "\nWithheld: %s. Items: ", g.Withheld)
			for j, it := range g.Items {
				if j > 0 {
					fmt.Fprint(w, ", ")
				}
				fmt.Fprint(w, it.ItemID)
			}
			fmt.Fprintf(w, ". View locally with --private.\n")
			continue
		}
		if g.Clean != "" {
			fmt.Fprintf(w, "\nSource (text_clean): \"%s\"\n", g.Clean)
		} else {
			fmt.Fprintf(w, "\nSource: cleaned excerpt unavailable (raw length %d chars; private reference: prompt %s; view locally with --private).\n", g.RawLen, g.PromptID)
		}
		if o.Private {
			fmt.Fprintf(w, "\nRaw (PRIVATE, never publish): %s\n", g.raw)
		}
		if len(g.Hints) > 0 {
			fmt.Fprintln(w, "\nHints (mechanical, not verdicts):")
			for _, h := range g.Hints {
				fmt.Fprintf(w, "- %s\n", h)
			}
		}
		fmt.Fprintln(w, "\n| # | rule as stored (weight, status) | your judgement |")
		fmt.Fprintln(w, "|---|---|---|")
		for j, it := range g.Items {
			fmt.Fprintf(w, "| %d | %s (%s, %s) `%s` | supported / stronger than the source / not supported / cannot tell |\n", j+1, it.Body, it.Weight, it.Status, it.ItemID)
		}
	}
	if end < len(r.Groups) {
		fmt.Fprintf(w, "\n---\n%d more prompts; rerun with --offset %d.\n", len(r.Groups)-end, end)
	}
	return nil
}
