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
// Read-only, and PUBLIC-BY-STRUCTURE: the default render carries ids,
// times, counts, weights, statuses and mechanical hints -- no prompt text
// and no rule text of any kind -- so it has no credential exposure by
// construction and needs no filter to be safe. The Private render carries
// everything (rule bodies, text_clean, text_raw) for the operator to read
// locally; that is where the question gets answered. Three review rounds on
// #1403 found three different secret shapes a keyword-plus-shape filter
// missed, and a fourth (a passphrase with spaces) needs no finding: free
// text cannot be gated by shape, so the public path publishes none.
//
// The filter survives as an ADVISORY in the private render: a row whose
// text looks like a credential or a personal arrangement is marked "do not
// quote publicly" with the reason, for the human who lifts a quote into an
// issue by hand. It advises; it never gates, and it never withholds.

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
	Body   string `json:"body,omitempty"` // private render only
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
	Clean    string       `json:"text_clean,omitempty"` // private render only
	CleanLen int          `json:"text_clean_length"`    // public: whether and how much cleaned text exists
	RawLen   int          `json:"raw_length"`
	Advisory string       `json:"do_not_quote,omitempty"` // private render only: reason the human should not lift this text into a public place
	Hints    []string     `json:"hints,omitempty"`        // mechanical, labelled, never a verdict
	Items    []ReviewItem `json:"items"`
	raw      string       // never serialised; rendered only under Private
}

// Review is the whole artifact plus the counts that bound it.
type Review struct {
	Prompts       int           `json:"prompts"`
	Params        int           `json:"live_parameters"`
	WithClean     int           `json:"prompts_with_text_clean"`
	ParamsClean   int           `json:"live_parameters_with_text_clean"`
	AdvisoryCount int           `json:"prompts_flagged_do_not_quote"`
	Groups        []ReviewGroup `json:"groups"`
}

// The do-not-quote advisory names what a human must not lift into a public
// place: credential shapes, credential words, and the operator's own
// accounts or arrangements (CLAUDE.local.md: "anything about his accounts,
// credentials, or personal arrangements ... they are context you hold, not
// content you publish"). It errs toward flagging, because a needless note
// costs nothing and a missed one costs the human a second look.
//
// Four layers, checked in order, any one of which flags:
//
//  1. keywords -- credential words, known key prefixes, and the operator's
//     own accounts/arrangements;
//
//  2. secret SHAPE, independent of any keyword -- a long hex run, a JWT's
//     three dot-separated segments, a `token=`/`secret:`-style assignment,
//     or any UNBROKEN alphanumeric run of 20+ characters that mixes digits
//     and letters, whatever its case. Separators (`-`, `_`, `.`, `/`) end a
//     run, so a hyphenated deployment name such as `audit-hill90-ui-client`
//     or a path segment is judged piece by piece and passes, while a pasted
//     key's own body -- which has no separators -- is caught;
//
//  3. the word "token" itself, which in this corpus is cost vocabulary
//     ("waste tokens", "token usage", "16k tokens") far more often than a
//     credential. It is withheld unless cost vocabulary sits within ~60
//     characters of it, and always withheld when a value-shaped literal
//     follows it.
//
//  4. a single-case alphabetic run of 20+ characters -- a jammed-together
//     passphrase ("correcthorsebatterystaple"). CamelCase identifiers, which
//     are this corpus's own vocabulary (TempoIngestionErrors,
//     createBoundedSseWriter -- 14 of 970 prompts carry one), mix cases and
//     pass; a passphrase typed in CamelCase would pass with them.
//
// What this is for, stated once: it ADVISES the human who lifts a quote
// into a public place. It does not gate anything. Three review rounds on
// #1403 each found a new shape the previous rule missed (bare "token" with
// a value; single-case keys; alphabetic passphrases), and the next one is
// free -- "correct horse battery staple" with spaces is under 20 characters
// per word and matches nothing here. That is why the public render carries
// no text at all (see the package comment) and this flag lives only in the
// private render. As an advisory, a false positive costs a needless "do not
// quote" note (a 40-hex commit SHA reads as a hex run; "ssh key" flags
// prompts about provisioning one) and a false negative costs nothing the
// human was not already responsible for.
//
// Known misses, named so nobody reads the flag as complete: any secret whose
// longest unbroken run is under 20 characters with no credential word and
// no "token" (UUID-shaped keys, short PINs, "use ab12cd34ef56gh78 to log
// in"); multi-word passphrases; CamelCase passphrases.
//
// History: a first cut withheld 145 of 970 prompts on the bare word
// "token"; the second exempted it and let "my token is 9f8e7d…" through;
// the third scored hyphenated identifiers as keys and missed single-case
// keys; the fourth missed alphabetic runs. Flagged now: 120 of 970 with the
// alphabetic rule adding none (measured 2026-09-11).
var sensitiveKeywords = regexp.MustCompile(`(?i)\b(sk-[a-z0-9]{6,}|ghp_[a-z0-9]{6,}|xox[abp]-[a-z0-9-]{6,}|password|passwd|passphrase|api[ -]?key|secret|bearer|creds?|credentials?|keychain|botfather|private key|ssh key|friend'?s? account|my friend|switch(ed|ing)? account|claude account|copilot account|subscriptions?|\bsubs\b|hill90admin)\b`)
var secretHexRun = regexp.MustCompile(`\b[0-9a-fA-F]{24,}\b`)
var secretJWT = regexp.MustCompile(`\b[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)
var secretAssignment = regexp.MustCompile(`(?i)\b(token|secret|password|passwd|api[_ -]?key|apikey)\s*[:=]\s*\S{8,}`)
var alnumRun = regexp.MustCompile(`[A-Za-z0-9]{20,}`)
var singleCaseAlphaRun = regexp.MustCompile(`\b(?:[a-z]{20,}|[A-Z]{20,})\b`)
var tokenWord = regexp.MustCompile(`(?i)\btokens?\b`)

// a literal of 12+ key characters within three words after "token"; the
// digit requirement is checked in Go so "tokens per 5-hour block" passes.
var tokenThenValue = regexp.MustCompile(`(?i)\btokens?\b\W+(?:\w+\W+){0,3}?([A-Za-z0-9_.-]{12,})`)
var costVocab = regexp.MustCompile(`(?i)\b(waste\w*|wasting|burn\w*|spend\w*|spent|sav\w*|usage|use[ds]?|using|cost\w*|budge\w*|count\w*|input|output|cached?|cache|context|million|thousand|per|quota|limit\w*|window|consum\w*|expensive|cheap\w*|efficien\w*|resources?|managed|min\W?max\w*|k)\b|\b\d+k?\b|\$|%`)

// secretShapedLiteral reports an unbroken alphanumeric run of 20+ characters
// that mixes digits and letters, regardless of case. Separators end a run,
// so hyphenated identifiers and paths are judged segment by segment; corpus
// ids (it-…, mp-…) carry a 16-hex body and fall under the floor.
func secretShapedLiteral(t string) string {
	for _, s := range alnumRun.FindAllString(t, -1) {
		hasDigit := strings.ContainsAny(s, "0123456789")
		hasLetter := strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
		if hasDigit && hasLetter {
			return s
		}
	}
	return ""
}

// tokenWithoutCostContext reports a "token"/"tokens" whose surrounding ~60
// characters carry no cost vocabulary -- read as the credential noun.
func tokenWithoutCostContext(t string) bool {
	for _, loc := range tokenWord.FindAllStringIndex(t, -1) {
		lo, hi := loc[0]-60, loc[1]+60
		if lo < 0 {
			lo = 0
		}
		if hi > len(t) {
			hi = len(t)
		}
		window := t[lo:loc[0]] + " " + t[loc[1]:hi]
		if !costVocab.MatchString(window) {
			return true
		}
	}
	return false
}

func sensitiveReason(texts ...string) string {
	const prefix = "matches the credential/personal-arrangement filter ("
	for _, t := range texts {
		if m := sensitiveKeywords.FindString(t); m != "" {
			return prefix + strings.ToLower(m) + ")"
		}
		if secretHexRun.MatchString(t) {
			return prefix + "hex run)"
		}
		if secretJWT.MatchString(t) {
			return prefix + "jwt shape)"
		}
		if secretAssignment.MatchString(t) {
			return prefix + "key=value assignment)"
		}
		if s := secretShapedLiteral(t); s != "" {
			return prefix + "secret-shaped literal)"
		}
		if singleCaseAlphaRun.MatchString(t) {
			return prefix + "single-case alphabetic run, passphrase-shaped)"
		}
		if m := tokenThenValue.FindStringSubmatch(t); m != nil && strings.ContainsAny(m[1], "0123456789") {
			return prefix + "token followed by a value)"
		}
		if tokenWithoutCostContext(t) {
			return prefix + "token)"
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
			g.Advisory = reason
			r.AdvisoryCount++
		}
		g.CleanLen = len(g.Clean)
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
		hs = append(hs, "source has no cleaned text")
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

// Render writes the artifact. Public output is ids and structure only --
// no rule text, no cleaned text, no raw text, no advisory reason. Private
// output carries all of it, marked so it cannot be mistaken for publishable,
// with a do-not-quote advisory on rows the filter flags.
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
		v.Groups = make([]ReviewGroup, len(page))
		copy(v.Groups, page)
		if !o.Private {
			// Public JSON is structure only: no rule text, no source text,
			// no advisory (its reason can name what it matched).
			for i := range v.Groups {
				v.Groups[i].Clean = ""
				v.Groups[i].Advisory = ""
				items := make([]ReviewItem, len(v.Groups[i].Items))
				for j, it := range v.Groups[i].Items {
					it.Body = ""
					items[j] = it
				}
				v.Groups[i].Items = items
			}
			v.AdvisoryCount = 0
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	fmt.Fprintf(w, "# Provenance review -- %d source prompts behind %d live parameters\n\n", r.Prompts, r.Params)
	fmt.Fprintf(w, "Showing prompts %d-%d of %d (sorted by live parameters per source, then time). %d prompts (%d live parameters) have cleaned text.\n\n",
		start+1, end, r.Prompts, r.WithClean, r.ParamsClean)
	if o.Private {
		fmt.Fprintf(w, "*** PRIVATE RENDER: rule text, cleaned text and raw prompt text are included. Do not paste this anywhere public. %d prompts carry a do-not-quote advisory. ***\n\n", r.AdvisoryCount)
	} else {
		fmt.Fprintln(w, "Public render: ids and structure only -- no prompt text and no rule text. Run with --private to read the rules and their sources locally; that is where the question gets answered.")
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "Hints are mechanical and labelled. The judgement column -- supported / stronger than the source / not supported / cannot tell -- is yours; nothing here fills it in.")
	for i, g := range page {
		fmt.Fprintf(w, "\n## %d. prompt %s -- %s -- recorded author: %s -- %d live parameter(s)", start+i+1, g.PromptID, g.At, g.Author, len(g.Items))
		if g.Project != "" {
			fmt.Fprintf(w, " -- project: %s", g.Project)
		}
		fmt.Fprintln(w)
		if o.Private {
			if g.Advisory != "" {
				fmt.Fprintf(w, "\nDO NOT QUOTE PUBLICLY: %s.\n", g.Advisory)
			}
			if g.Clean != "" {
				fmt.Fprintf(w, "\nSource (text_clean): \"%s\"\n", g.Clean)
			} else {
				fmt.Fprintf(w, "\nSource: no cleaned text (raw length %d chars).\n", g.RawLen)
			}
			fmt.Fprintf(w, "\nRaw (PRIVATE, never publish): %s\n", g.raw)
		} else if g.CleanLen > 0 {
			fmt.Fprintf(w, "\nSource: cleaned text exists (%d chars; raw %d chars); read it with --private.\n", g.CleanLen, g.RawLen)
		} else {
			fmt.Fprintf(w, "\nSource: no cleaned text (raw length %d chars); read it with --private.\n", g.RawLen)
		}
		if len(g.Hints) > 0 {
			fmt.Fprintln(w, "\nHints (mechanical, not verdicts):")
			for _, h := range g.Hints {
				fmt.Fprintf(w, "- %s\n", h)
			}
		}
		if o.Private {
			fmt.Fprintln(w, "\n| # | rule as stored (weight, status) | your judgement |")
			fmt.Fprintln(w, "|---|---|---|")
			for j, it := range g.Items {
				fmt.Fprintf(w, "| %d | %s (%s, %s) `%s` | supported / stronger than the source / not supported / cannot tell |\n", j+1, it.Body, it.Weight, it.Status, it.ItemID)
			}
		} else {
			fmt.Fprintln(w, "\n| # | item | weight | status | your judgement |")
			fmt.Fprintln(w, "|---|---|---|---|---|")
			for j, it := range g.Items {
				fmt.Fprintf(w, "| %d | `%s` | %s | %s | supported / stronger than the source / not supported / cannot tell |\n", j+1, it.ItemID, it.Weight, it.Status)
			}
		}
	}
	if end < len(r.Groups) {
		fmt.Fprintf(w, "\n---\n%d more prompts; rerun with --offset %d.\n", len(r.Groups)-end, end)
	}
	return nil
}
