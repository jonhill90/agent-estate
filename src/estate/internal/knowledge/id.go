package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
)

// itemID derives a stable Item.ID from permalink -- the field
// agent-estate#1026 established as this package's actual durable
// identifier (a vault fact's own file path, a corpus row's own
// "corpus:item:<id>", a GitHub star's own html_url, a Loops-Research
// file's own path). Two Generate calls over the same unchanged sources
// produce the same permalink for the same item, so they produce the
// same id.
//
// This replaces the wall-clock idClock that used to assign ids here: it
// issued a fresh 14-char YYYYMMDDHHmmss value, one second later per
// item, on every single Generate call -- stable within one run, but
// never stable across two. `query` returns ids and `get <id>` consumes
// them; that two-step is #1019's own progressive disclosure, and an id
// that dies at the next regenerate breaks it the moment anything durable
// (an issue, a brief, a decision record) cites one. See id_test.go's
// TestItemIDIsStableAcrossTwoGenerateCalls for the regression this
// closes, and TestItemIDChangesWhenPermalinkChanges for what it does
// NOT promise: an id is a function of permalink alone, so an item whose
// permalink itself changes (a vault fact renamed, a corpus row's id
// reassigned) gets a new id -- that is a different citation target, not
// instability.
//
// sha256 truncated to the first 8 bytes (16 hex chars), prefixed "it-",
// matches the shape the corpus source's own permalink already carries
// (corpus.go's `corpus:item:<id>`, itself an "it-<hex>" value minted by
// the corpus, not by this package) -- one id convention across the
// index, not two. 8 bytes of a cryptographic hash over an index of
// ~1,600 items keeps collision risk far below anything else this index
// already tolerates (the birthday bound on 2^64 at n=1,600 is
// astronomically small); a genuine collision would mean two DIFFERENT
// permalinks hashing to the same id, which #1026 did not ask this
// package to detect or resolve.
func itemID(permalink string) string {
	sum := sha256.Sum256([]byte(permalink))
	return "it-" + hex.EncodeToString(sum[:8])
}
