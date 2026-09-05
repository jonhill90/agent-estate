package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// EXCLUDED IN BULK, with one narrow carve-out. Measured against the real
// corpus (2026-09-04): 3,785 of 3,979 thought rows -- 95.1% -- are already
// weight='retracted' or status='needs_review'/'dropped', meaning the
// corpus's own judging pass already looked at nearly all of them and did
// not promote them to a decided kind. Of the 194 that remain, 166 (86%)
// are weight='preference' -- processed, low-firmness ambient context, not
// a parameter, directive, question or correction anyone failed to capture
// as one, and genuinely excludable on the reasoning above. The other 28
// (14%) carry weight='hard': the corpus's own marker for a binding
// constraint, at status='open'/'acknowledged'/'acted'/'resolved', none
// retracted or dropped. A row the corpus itself marks weight='hard' and
// not retracted is decided and live by this package's own stated purpose
// ("what's decided and live", not "everything ever said aloud") no matter
// which kind column it sits under -- agent-estate#1131: the same defect
// #1035 fixed for directive/question one level down, surviving inside
// this comment's own justification for thought. bulkExcludedThoughtRow
// below states the boundary as a predicate, not a count, because 28 is
// today's number and will drift as the corpus grows; querying it again
// is cheap and this comment does not try to stay numerically current.
// What a reader still loses by the bulk exclusion: transient musings and
// stray context that were never judged actionable enough to become one of
// the other four kinds or to be marked weight='hard' -- recoverable, if
// ever needed, by querying the corpus's own `thought` rows directly.
var corpusKinds = []string{"parameter", "directive", "question", "correction", "thought"}

// bulkExcludedThoughtRow reports whether a thought-kind row (weight,
// status already known not to be 'retracted' -- the caller applies that
// filter first, same as every other kind) falls inside the bulk
// exclusion corpusKinds' own comment describes, rather than the narrow
// weight='hard' carve-out. A thought row is compiled into the index only
// when this returns false.
func bulkExcludedThoughtRow(weight, status string) bool {
	if weight == "hard" {
		return false
	}
	return true
}

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
// resolved" semantics identical across parameter, directive, question and
// correction rather than reading three different filters for three
// different reasons. thought is the one kind in corpusKinds this
// weight != 'retracted' filter does not fully decide on its own --
// bulkExcludedThoughtRow, applied per-row below, narrows it further to the
// weight='hard' carve-out corpusKinds' own comment describes.
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

	// tier3Path is the handle every corpus-derived item's Tier3 cites --
	// agent-estate#1153: the other three tier3 shapes (vault-fact, repo-docs,
	// github-stars) each resolve to something a caller can follow (a path, a
	// path plus a root note, a URL); this one used to give only an item id
	// and a disclaimer, unfollowable by a caller who does not already know
	// ~/corpus/ledger.sqlite3. dbPath is used here rather than a fresh call
	// to internal/corpus.Path(): dbPath is the exact file this function just
	// stat'd and is about to query below, so citing it can never diverge from
	// where the item actually came from -- a second, independent resolution
	// could (a fixture corpus under a test's t.TempDir() does not live at
	// internal/corpus.Path()'s answer, and a caller with ESTATE_CORPUS set
	// between two resolutions could see either literally never actually
	// queried). filepath.Abs resolves dbPath the same way vault-fact's own
	// absolute vault paths are resolved (usable over portable, matching that
	// precedent); its own error is the only unresolvable case here -- dbPath
	// itself is already known non-empty and stat-able by this point, so that
	// error is not a realistic one, but the failure is still stated as a
	// typed absence in the pointer text, never silently dropped or defaulted
	// (agent-estate#1141/#1143: an absent value must say so, not disappear).
	tier3Path := dbPath
	if abs, err := filepath.Abs(dbPath); err == nil {
		tier3Path = abs
	} else {
		tier3Path = fmt.Sprintf("%s (path could not be resolved: %v)", dbPath, err)
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
		if kind == "thought" && bulkExcludedThoughtRow(weight, status) {
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
			Tier3:          "the corpus's own item " + id + " (kind=" + kind + ") in " + tier3Path + " -- not this file",
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
