package knowledge

import "testing"

// TestStemCollapsesInflections pins the morphology cases agent-estate#1054
// names explicitly: "model"/"models" and "refresh"/"refreshed" must stem
// to the same root, so a paraphrase's inflected form and an item's own
// text land in the same BM25 term.
func TestStemCollapsesInflections(t *testing.T) {
	cases := []struct {
		a, b string
	}{
		{"model", "models"},
		{"refresh", "refreshed"},
		{"refresh", "refreshing"},
		{"list", "listed"},
		{"list", "listing"},
		{"name", "names"},
		{"date", "dated"},
		{"date", "dates"},
		{"part", "parts"},
	}
	for _, c := range cases {
		got := stem(c.a)
		want := stem(c.b)
		if got != want {
			t.Errorf("stem(%q)=%q, stem(%q)=%q -- want the same stem", c.a, got, c.b, want)
		}
	}
}

// TestStemLeavesUnrelatedWordsDistinct guards against a stemmer so
// aggressive it collapses words that are not the same term -- stemming
// exists to catch inflection, not to widen matching into a fuzzy search.
func TestStemLeavesUnrelatedWordsDistinct(t *testing.T) {
	cases := []struct{ a, b string }{
		{"model", "modal"},
		{"date", "data"},
		{"name", "game"},
	}
	for _, c := range cases {
		if stem(c.a) == stem(c.b) {
			t.Errorf("stem(%q) == stem(%q) == %q -- these are different words, not inflections of each other",
				c.a, c.b, stem(c.a))
		}
	}
}

// TestStemIsIdempotent -- stemming an already-stemmed word must be a
// no-op, otherwise repeated tokenization (queryTerms feeding into
// fieldTermCounts's own vocabulary) could drift.
func TestStemIsIdempotent(t *testing.T) {
	for _, w := range []string{"model", "refresh", "date", "keychain", "recoveri"} {
		if s := stem(w); s != stem(s) {
			t.Errorf("stem(%q) = %q, but stem(stem(%q)) = %q -- not idempotent", w, s, w, stem(s))
		}
	}
}
