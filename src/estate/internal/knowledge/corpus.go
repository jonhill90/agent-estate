package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const corpusSep = "\x1f"

// corpusKinds is which of the corpus's five item kinds this package
// compiles into the index, and which it leaves out -- agent-estate#1035.
//
// parameter (1,104) was the only kind compiled before this change; kept.
//
// directive (1,540) and question (509) are added by #1035 itself: the
// gap #1035 exists to close is that a reader querying e.g. "tmux" saw
// only the hard parameters on a subject and never the directives that
// resolved the same question, and never the question itself -- #1020's
// defect read from the retrieval side. Compiling both is the fix.
//
// correction (179) is added too. A correction is "X was believed, it was
// wrong, Y replaced it" -- exactly the shape a reader needs to avoid
// re-asserting something the operator already reversed, and at 179 rows
// it costs the index roughly what question already costs while covering
// a failure mode (reasoning from a superseded belief) neither parameter,
// directive nor question record. Nothing here classifies it as noise the
// way thought is (see below): every correction row is weight='hard' or
// 'preference', none retracted, so the same live filter this function
// already applies to the other three kinds includes all 179 of them
// unchanged.
//
// thought (3,979, the largest kind by more than 2x) is deliberately
// EXCLUDED. Measured against the real corpus (2026-09-04): 3,782 of
// 3,979 thought rows -- 95% -- are already weight='retracted' or
// status='needs_review'/'dropped', meaning the corpus's own judging pass
// already looked at nearly all of them and did not promote them to a
// decided kind. The ~200 that remain are overwhelmingly weight=
// 'preference' at status='acted'/'acknowledged' -- processed, low-firmness
// ambient context, not a parameter, directive, question or correction
// anyone failed to capture as one. Including thought would roughly
// quadruple the index again on top of what directive+question+correction
// already add, for a kind that is mostly noise by the corpus's own
// classification. What a reader loses by this exclusion: transient
// musings and stray context that were never judged actionable enough to
// become one of the other four kinds -- recoverable, if ever needed, by
// querying the corpus's own `thought` rows directly; the compiled index's
// purpose is "what's decided and live", not "everything ever said aloud".
var corpusKinds = []string{"parameter", "directive", "question", "correction"}

// corpusSourceName maps a corpus kind to this package's own Item.Source
// value. parameter keeps "corpus-parameter" -- the value already cited by
// the golden set (goldenset/cases.json) and classify.go's own doc
// comment, so leaving it unchanged is what keeps both intact. The other
// three kinds get their own Source value rather than sharing
// "corpus-parameter": a reader of a Match or a Get result can already
// tell a directive from a parameter by Source alone, before ever reaching
// the kind: structural tag (see corpusSource below) -- belt and braces,
// and each is exactly as informative as "corpus-parameter" already was
// for its own kind.
func corpusSourceName(kind string) string {
	return "corpus-" + kind
}

// corpusSource reads every row of the corpus's own `items` table whose
// kind is one of corpusKinds and whose weight is not 'retracted' -- the
// same filter live_parameters itself applies (`CREATE VIEW live_parameters
// AS SELECT * FROM items WHERE kind = 'parameter' AND weight !=
// 'retracted'`), generalised across kinds because the corpus schema does
// not carry an equivalent live_directives/live_questions/live_corrections
// view for the other three (checked directly against the schema,
// 2026-09-04: only live_parameters, open_questions -- status='open' only,
// narrower than what #1035 asks for -- unacknowledged, needs_review,
// possibility_count and conflicts/capture_health exist). Querying `items`
// directly with the same weight != 'retracted' filter live_parameters
// already encodes keeps this function's own "live set, not merely
// resolved" semantics identical across all four kinds rather than
// reading three different filters for three different reasons.
//
// RAW PROMPTS NEVER LEAVE THE SOURCE. This query selects prompt_id --
// items' own bare id column, the same identifier
// internal/corpus's provenance.go joins against prompts.id -- and
// nothing else from that lineage. There is no column here, and no query
// anywhere in this package, that reaches prompts.text_raw or
// prompts.text_clean. That is the hard rule this function exists to
// keep, not an incidental property of the query below (agent-estate#1031:
// prompt_id makes an item traceable to what the operator actually said,
// without this package ever holding the words themselves).
//
// dbPath must already be a real, stat-able file; the caller (Generate)
// resolves ~/corpus/ledger.sqlite3 (agent-estate#942's own trap: CLAUDE.md
// documents the wrong path) before calling this. Opened, per the
// operator's own stated requirement, only as
// file:<path>?mode=ro&immutable=1.
func corpusSource(dbPath string) (SourceResult, []Item) {
	res := SourceResult{Name: "corpus-items"}
	if dbPath == "" {
		res.Reason = "no corpus path configured"
		return res, nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		res.Reason = fmt.Sprintf("corpus unreadable at %s: %v", dbPath, err)
		return res, nil
	}

	placeholders := make([]string, len(corpusKinds))
	kindArgs := make([]string, len(corpusKinds))
	for i, k := range corpusKinds {
		placeholders[i] = "'" + k + "'" // corpusKinds is a fixed package literal, never user input
		kindArgs[i] = k
	}
	q := fmt.Sprintf(`select id, coalesce(resolved_to,''), coalesce(weight,''), coalesce(status,''),
	      replace(replace(body, char(10), ' '), char(13), ' '), prompt_id, kind
	      from items where kind in (%s) and weight != 'retracted' order by id`,
		strings.Join(placeholders, ","))
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
		parts := strings.SplitN(sc.Text(), corpusSep, 7)
		if len(parts) != 7 {
			continue
		}
		id, resolvedTo, weight, status, body, promptID, kind := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5], parts[6]
		body = strings.TrimSpace(body)
		if body == "" && resolvedTo == "" {
			continue
		}
		tier1 := resolvedTo
		if tier1 == "" {
			tier1 = truncate(body, 120)
		}
		// kind is a structural tag, the same mechanism weight and status
		// already use, not a new field on Item -- agent-estate#1035's own
		// call to make. It composes for free with #1042's exact-tag
		// filter (kind:question matches only questions, same as
		// weight:hard already matches only hard parameters today), and it
		// needs no change to Item's shape or to query.go's scoring, which
		// already folds every StructuralTags entry into searchableText.
		var structural []string
		structural = append(structural, "kind:"+kind)
		if weight != "" {
			structural = append(structural, "weight:"+weight)
		}
		if status != "" {
			structural = append(structural, "status:"+status)
		}
		source := corpusSourceName(kind)
		publishable, basis := classify(source)
		permalink := fmt.Sprintf("corpus:item:%s", id)
		items = append(items, Item{
			ID:             itemID(permalink),
			Source:         source,
			Permalink:      permalink,
			StructuralTags: structural,
			Tier1:          truncate(tier1, 200),
			Tier2:          truncate(body, 400),
			Tier3:          "the corpus's own item " + id + " (kind=" + kind + ") -- not this file",
			Publishable:    publishable,
			PublishBasis:   basis,
			PromptID:       promptID,
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
