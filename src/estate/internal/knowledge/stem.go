package knowledge

// stem reduces a lowercased, already-tokenized word to a crude
// morphological root, so "model"/"models" and "refresh"/"refreshed"
// count as the same term to the scorer instead of two unrelated ones
// (agent-estate#1054: "stemming alone may carry several cases -- morphology,
// not semantics"). This implements Porter's own step 1 (1a/1b/1c) --
// the suffix rules covering English plurals and the -ed/-ing verb
// endings, which is most of what a corpus this size actually needs --
// and deliberately stops there rather than porting the algorithm's full
// five steps (derivational suffixes like -ational/-iveness): those exist
// to normalise vocabulary breadth a general-purpose search engine sees,
// not the fixed, hand-written vocabulary of vault facts and corpus
// parameters this index carries. A partial, well-understood port beats a
// hand-rolled heuristic list of suffixes with no reference to check it
// against.
func stem(word string) string {
	if len(word) <= 2 {
		return word
	}
	word = step1a(word)
	word = step1b(word)
	word = step1c(word)
	return word
}

// isConsonant reports whether the byte at i is a consonant under
// Porter's own definition: every letter except a/e/i/o/u is a consonant,
// and y is a consonant only when it is not preceded by another
// consonant (so "y" in "cry" is a vowel, but the "y" in "yellow" is a
// consonant) -- Porter's rule exactly, needed by measure() below.
func isConsonant(word string, i int) bool {
	switch word[i] {
	case 'a', 'e', 'i', 'o', 'u':
		return false
	case 'y':
		if i == 0 {
			return true
		}
		return !isConsonant(word, i-1)
	default:
		return true
	}
}

// measure computes Porter's m: a word decomposes into
// [C](VC)^m[V] where C and V are (possibly empty) runs of consonants
// and vowels; m counts the VC repeats and gates several of the suffix
// rules below (a suffix is only stripped when the remaining stem has
// enough "syllables" to still be a real word, e.g. "feed" must not lose
// its own -eed).
func measure(word string) int {
	i, n, m := 0, len(word), 0
	for i < n && isConsonant(word, i) {
		i++
	}
	for i < n {
		for i < n && !isConsonant(word, i) {
			i++
		}
		if i >= n {
			break
		}
		for i < n && isConsonant(word, i) {
			i++
		}
		m++
	}
	return m
}

// containsVowel reports whether word has a vowel anywhere -- Porter's
// *v* condition, used to tell a real verb ending ("hopping" -> stem
// "hopp" contains a vowel) from a stem that would become vowel-less
// nonsense if the suffix were stripped.
func containsVowel(word string) bool {
	for i := range word {
		if !isConsonant(word, i) {
			return true
		}
	}
	return false
}

// endsDoubleConsonant reports whether word ends in two identical
// consonants ("hopp", "add") -- Porter's *d condition, the trigger for
// dropping one of them after an -ing/-ed strip.
func endsDoubleConsonant(word string) bool {
	n := len(word)
	if n < 2 {
		return false
	}
	return word[n-1] == word[n-2] && isConsonant(word, n-1)
}

// endsCVC reports whether word's last three letters are
// consonant-vowel-consonant with the final consonant not w, x or y --
// Porter's *o condition ("hop" qualifies, "hoy" and "how" do not).
func endsCVC(word string) bool {
	n := len(word)
	if n < 3 {
		return false
	}
	last := word[n-1]
	if last == 'w' || last == 'x' || last == 'y' {
		return false
	}
	return isConsonant(word, n-3) && !isConsonant(word, n-2) && isConsonant(word, n-1)
}

// step1a strips plural suffixes -- SSES->SS, IES->I, SS->SS (no-op), S->
// (nothing, when the stem left behind is more than one letter).
func step1a(word string) string {
	switch {
	case hasSuffix(word, "sses"):
		return word[:len(word)-2]
	case hasSuffix(word, "ies"):
		return word[:len(word)-3] + "i"
	case hasSuffix(word, "ss"):
		return word
	case hasSuffix(word, "s") && len(word) > 2:
		return word[:len(word)-1]
	}
	return word
}

// step1b strips verb-ending suffixes -- -EED (only when the stem has
// measure > 0), -ED and -ING (only when the remaining stem contains a
// vowel, i.e. it's a real verb form and not, say, the -ed in "bed") --
// then repairs the stem the strip can leave malformed: -AT/-BL/-IZ get
// an E back ("conflat" -> "conflate"), a doubled non-L/S/Z consonant
// loses one letter ("hopp" -> "hop"), and a short CVC stem gets an E
// added ("hop" -> "hope") so it doesn't collide with an unrelated short
// word.
func step1b(word string) string {
	switch {
	case hasSuffix(word, "eed"):
		stem := word[:len(word)-3]
		if measure(stem) > 0 {
			return stem + "ee"
		}
		return word
	case hasSuffix(word, "ed") && containsVowel(word[:len(word)-2]):
		word = word[:len(word)-2]
	case hasSuffix(word, "ing") && containsVowel(word[:len(word)-3]):
		word = word[:len(word)-3]
	default:
		return word
	}

	switch {
	case hasSuffix(word, "at"), hasSuffix(word, "bl"), hasSuffix(word, "iz"):
		return word + "e"
	case endsDoubleConsonant(word) && !hasSuffix(word, "l") && !hasSuffix(word, "s") && !hasSuffix(word, "z"):
		return word[:len(word)-1]
	case measure(word) == 1 && endsCVC(word):
		return word + "e"
	}
	return word
}

// step1c turns a trailing Y into I when the stem before it has a vowel
// ("happy" -> "happi", ready to match "happier"/"happiness" stems later
// -- and consistent with "cry"/"cries" both landing on "cri" via step1a).
func step1c(word string) string {
	if hasSuffix(word, "y") && containsVowel(word[:len(word)-1]) {
		return word[:len(word)-1] + "i"
	}
	return word
}

func hasSuffix(word, suffix string) bool {
	n, m := len(word), len(suffix)
	return n >= m && word[n-m:] == suffix
}
