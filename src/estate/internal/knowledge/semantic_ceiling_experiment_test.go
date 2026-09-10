package knowledge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge/goldenset"
)

// TestSemanticCeilingExperiment measures agent-estate#1255's semantic/
// embedding candidate mechanism -- NOT a fix, a ceiling: how many of the
// retrieval baseline's 17 ranking failures could a cosine-similarity
// rerank possibly move into the top 10, and how many of the 9 current
// hits that same rerank would break.
//
// Same discipline as agent-estate#1338's rare/high-IDF ceiling: calls
// Query itself, unmodified, with a limit large enough to return the whole
// real candidate list -- never a reimplementation of Query's own
// filtering (currentMemoryItem, tag filters, minMatchedTerms, the
// vault-fact title rescue, isDistilledRule, the privacy cut) or its BM25
// math. The only thing this measures replacing is the ORDER candidates
// are ranked in: BM25 score is swapped for cosine similarity between the
// question's own embedding and each candidate's Tier1+Tier2 text
// embedding, computed by a real, local embedding model -- never a second,
// synthetic scoring function.
//
// Opt-in on BOTH ESTATE_RANKING_EXPERIMENT_INDEX (the same private-index
// convention #1338 established) and a reachable local embedding endpoint
// (ESTATE_EMBEDDING_ENDPOINT, default http://localhost:1234/v1 -- LM
// Studio's own default). Skips loudly, never fails, when either is
// absent: a bare `go test ./...` must never depend on a private index or
// a locally-running model CI does not have.
func TestSemanticCeilingExperiment(t *testing.T) {
	idxPath := os.Getenv("ESTATE_RANKING_EXPERIMENT_INDEX")
	if idxPath == "" {
		t.Skip("ESTATE_RANKING_EXPERIMENT_INDEX not set -- opt-in ceiling measurement only, see agent-estate#1255")
	}
	endpoint := strings.TrimSuffix(os.Getenv("ESTATE_EMBEDDING_ENDPOINT"), "/")
	if endpoint == "" {
		endpoint = "http://localhost:1234/v1"
	}
	model := os.Getenv("ESTATE_EMBEDDING_MODEL")
	if model == "" {
		model = "text-embedding-nomic-embed-text-v1.5"
	}
	if _, err := embedTexts(endpoint, model, []string{"connectivity probe"}); err != nil {
		t.Skip("no reachable local embedding endpoint at " + endpoint + " with model " + model +
			" -- opt-in semantic ceiling measurement only, see agent-estate#1255: " + err.Error())
	}

	res, err := Read(idxPath)
	if err != nil {
		t.Fatalf("could not read experiment index %s: %v", idxPath, err)
	}
	byID := make(map[string]Item, len(res.Items))
	for _, it := range res.Items {
		byID[it.ID] = it
	}

	// Embed every item in the index ONCE, keyed by ID -- candidate pools
	// across the 26 cases overlap heavily, and this avoids re-embedding
	// the same item 26 times. The embedded text is Tier1+Tier2, the exact
	// same field pair searchableText (bm25.go) concatenates for BM25 --
	// this is the fairest comparison available: the same content, a
	// different similarity function over it, not a richer or poorer view
	// of the item than BM25 itself sees.
	ids := make([]string, 0, len(res.Items))
	texts := make([]string, 0, len(res.Items))
	for _, it := range res.Items {
		ids = append(ids, it.ID)
		texts = append(texts, strings.TrimSpace(it.Tier1+". "+it.Tier2))
	}
	// Cached at a private scratch path this test itself names, keyed by
	// index path + item count -- re-embedding the same ~7,700 items on
	// every iteration of this test costs ~2.5 minutes for nothing new;
	// the cache is opt-in-only content (never committed, never the
	// shared index) and is skipped entirely if ESTATE_SEMANTIC_CACHE is
	// unset, so a first, from-scratch run is still the default.
	var itemVecs map[string][]float64
	cachePath := os.Getenv("ESTATE_SEMANTIC_CACHE")
	if cachePath != "" {
		if cached, cerr := loadVecCache(cachePath); cerr == nil && len(cached) == len(ids) {
			itemVecs = cached
			t.Logf("loaded %d cached item embeddings from %s (skipped re-embedding)", len(itemVecs), cachePath)
		}
	}
	if itemVecs == nil {
		t0 := time.Now()
		itemVecs, err = embedAllBatched(endpoint, model, ids, texts, 500)
		if err != nil {
			t.Fatalf("embedding the index (%d items): %v", len(ids), err)
		}
		t.Logf("embedded %d index items in %s (local model %s at %s)", len(ids), time.Since(t0).Round(time.Millisecond), model, endpoint)
		if cachePath != "" {
			if werr := saveVecCache(cachePath, itemVecs); werr != nil {
				t.Logf("could not write embedding cache to %s (non-fatal): %v", cachePath, werr)
			}
		}
	}

	cases, err := goldenset.LoadRetrievalBaseline()
	if err != nil {
		t.Fatalf("could not load retrieval baseline cases: %v", err)
	}
	questions := make([]string, len(cases))
	for i, c := range cases {
		questions[i] = c.Question
	}
	qVecsList, err := embedTexts(endpoint, model, questions)
	if err != nil {
		t.Fatalf("embedding the %d case questions: %v", len(questions), err)
	}

	bigLimit := len(res.Items) + 1
	origHits, origFails := 0, 0
	fixed, regressed := 0, 0
	var fixedIDs, regIDs []string
	present, absent := 0, 0
	var absentIDs []string
	var absentBounds []string

	for i, c := range cases {
		out := Query(idxPath, c.Question, bigLimit, true) // always --private, unscoped -- this stratum's own contract
		origRank := 0
		for j, m := range out.Matches {
			if c.ExpectedIdentifier != "" && strings.HasSuffix(m.Permalink, c.ExpectedIdentifier) {
				origRank = j + 1
				break
			}
		}
		origHit := origRank >= 1 && origRank <= 10
		if origHit {
			origHits++
		} else {
			origFails++
		}
		if origRank > 0 {
			present++
		} else {
			absent++
			absentIDs = append(absentIDs, c.ID)
			// Secondary, exploratory bound only (not part of the ceiling
			// above): this case never enters Query's own candidate list
			// at all, so no rerank of that list can ever reach it. But a
			// semantic mechanism need not inherit BM25's minMatchedTerms
			// gate -- it could be built to search the WHOLE index by
			// embedding alone. This checks, ignoring every eligibility
			// filter Query applies (privacy, tags, currentness,
			// dedup), where the expected item ranks by raw cosine
			// similarity against every embedded item -- an upper bound
			// on reachability, not a claim about what a real, filtered
			// implementation would return.
			if bounds := boundAbsentCase(c, byID, itemVecs, qVecsList[i]); bounds != "" {
				absentBounds = append(absentBounds, bounds)
			}
			continue
		}

		type scoredCand struct {
			id  string
			sim float64
			hit bool
		}
		var scored []scoredCand
		for _, m := range out.Matches {
			v, ok := itemVecs[m.ID]
			if !ok {
				continue // should not happen -- every item in Query's output is in res.Items
			}
			isHit := c.ExpectedIdentifier != "" && strings.HasSuffix(m.Permalink, c.ExpectedIdentifier)
			scored = append(scored, scoredCand{m.ID, cosineSim(qVecsList[i], v), isHit})
		}
		sort.SliceStable(scored, func(a, b int) bool { return scored[a].sim > scored[b].sim })
		newRank := 0
		for j, s := range scored {
			if s.hit {
				newRank = j + 1
				break
			}
		}
		newHit := newRank >= 1 && newRank <= 10
		switch {
		case !origHit && newHit:
			fixed++
			fixedIDs = append(fixedIDs, c.ID)
			t.Logf("  FIXED  %s: BM25 rank %d -> semantic rank %d (of %d real candidates)", c.ID, origRank, newRank, len(scored))
		case origHit && !newHit:
			regressed++
			regIDs = append(regIDs, c.ID)
			t.Logf("  REGRESSED %s: BM25 rank %d -> semantic rank %d (of %d real candidates)", c.ID, origRank, newRank, len(scored))
		}
	}

	t.Logf("coverage: %d/%d expected identifiers present somewhere in Query's own real candidate list (score>0, cleared minMatchedTerms), %d absent from that pipeline entirely regardless of rank: %v -- identical population to agent-estate#1338's own coverage line, since this is the same Query() and the same fixture", present, len(cases), absent, absentIDs)
	t.Logf("reproduced baseline: %d/%d top-10 hits, %d/%d ranking failures (present, not top-10)", origHits, len(cases), origFails, len(cases))
	for _, b := range absentBounds {
		t.Logf("  %s", b)
	}
	t.Logf("CEILING: single-setting cosine-similarity rerank of Query's own real candidate list: %d/%d ranking failures fixed %v, %d HIT->MISS regression(s) %v", fixed, origFails, fixedIDs, regressed, regIDs)
}

// boundAbsentCase reports, for a case Query's own pipeline never
// surfaces at all, where the expected item would rank by RAW cosine
// similarity against every embedded item in the index -- no privacy cut,
// no tag filter, no dedup, no minMatchedTerms. This is an upper bound on
// what a from-scratch semantic implementation could ever reach for this
// question, not a claim that today's Query, or any particular semantic
// implementation, would actually return it -- see this test's own
// caller for how it is reported.
func boundAbsentCase(c goldenset.Case, byID map[string]Item, itemVecs map[string][]float64, qVec []float64) string {
	if c.ExpectedIdentifier == "" {
		return ""
	}
	var expectedID string
	for id, it := range byID {
		if strings.HasSuffix(it.Permalink, c.ExpectedIdentifier) {
			expectedID = id
			break
		}
	}
	if expectedID == "" {
		return fmt.Sprintf("%s: expected_identifier %q not found in the index at all -- cannot bound", c.ID, c.ExpectedIdentifier)
	}
	targetVec, ok := itemVecs[expectedID]
	if !ok {
		return fmt.Sprintf("%s: expected item %s has no embedding -- cannot bound", c.ID, expectedID)
	}
	targetSim := cosineSim(qVec, targetVec)
	rank := 1
	for id, v := range itemVecs {
		if id == expectedID {
			continue
		}
		if cosineSim(qVec, v) > targetSim {
			rank++
		}
	}
	return fmt.Sprintf("%s: absent from Query's pipeline, but raw cosine rank %d/%d against the WHOLE index ignoring every eligibility filter (privacy/tags/dedup) -- an upper bound only, not a claim any real implementation reaches it", c.ID, rank, len(itemVecs))
}

func cosineSim(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// embeddingRequest/Response mirror the OpenAI-compatible /embeddings
// shape LM Studio's local server speaks -- the same API surface this
// package's own HTTP callers elsewhere in the repo already assume for
// local model access, never a remote endpoint (agent-estate#1255's own
// brief: a local model only, the private index is never sent anywhere).
type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

func embedTexts(endpoint, model string, texts []string) ([][]float64, error) {
	body, err := json.Marshal(embeddingRequest{Model: model, Input: texts})
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 180 * time.Second}
	req, err := http.NewRequest(http.MethodPost, endpoint+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding endpoint %s returned %d: %s", endpoint, resp.StatusCode, string(raw))
	}
	var parsed embeddingResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("could not parse embedding response: %w (body: %s)", err, string(raw))
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embedding endpoint returned %d vectors for %d inputs", len(parsed.Data), len(texts))
	}
	out := make([][]float64, len(parsed.Data))
	for i, d := range parsed.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// loadVecCache/saveVecCache persist itemVecs to a private scratch JSON
// file named entirely by ESTATE_SEMANTIC_CACHE (opt-in, never a default
// path, never committed) -- pure iteration-speed convenience for this
// measurement, not part of the ceiling it reports.
func loadVecCache(path string) (map[string][]float64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string][]float64
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func saveVecCache(path string, m map[string][]float64) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// embedAllBatched embeds ids/texts in fixed-size batches (the local
// server handles a few hundred inputs per call comfortably; batching
// keeps any one request bounded rather than sending several thousand
// inputs in a single call) and returns a map keyed by id.
func embedAllBatched(endpoint, model string, ids, texts []string, batchSize int) (map[string][]float64, error) {
	out := make(map[string][]float64, len(ids))
	for start := 0; start < len(ids); start += batchSize {
		end := start + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		vecs, err := embedTexts(endpoint, model, texts[start:end])
		if err != nil {
			return nil, fmt.Errorf("batch [%d:%d]: %w", start, end, err)
		}
		for i, v := range vecs {
			out[ids[start+i]] = v
		}
	}
	return out, nil
}
