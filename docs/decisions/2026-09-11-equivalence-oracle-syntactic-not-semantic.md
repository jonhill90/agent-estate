# 2026-09-11 — the retrieval-equivalence oracle draws a syntactic line, not a semantic one (#1407)

**Status:** decided, as a kept behaviour under attack, not as a fix. agent-estate#1318.

## What the oracle does

`cmd/goldenquery -baseline` scored a case as a hit only when the returned
item's permalink carried the fixture author's chosen identifier. Six of 26
cases returned the designated sentence verbatim under a *different* id and
were scored as misses, pricing retrieval work against the wrong number
(17 apparent ranking failures; 11 genuine). #1407 adds a second, credited
score for a verbatim-equivalent answer, attributed per row, sitting beside
the untouched strict score — never replacing it.

An item is credited only if its text contains the designated text
verbatim (normalised for case/markdown/whitespace only) **and** either
**opens** with it (the candidate's own first statement *is* the sentence)
or **cites** the designated item's id inline. A mid-body quote, however
exact, and any paraphrase, however good, are refused — "nothing here
widens past verbatim containment... because a reader's acceptance cannot
live in a test" (the file's own comment, confirmed by the code).

## The bug this closed, and the line it drew instead

Round one found two real false credits: a leading `>` blockquote was
normalised away as decoration before the "opens" check, so a document
that *quotes* the designated sentence and then contradicts it read as
"opens"; and `designatedText` had no length floor, so a short, generic
sentence could ride a coincidental shared opening phrase. Both closed —
blockquote lines are no longer stripped, they're skipped outright when
reading a candidate's own first statement; a term floor (reused from
`internal/corpus`'s own pre-existing admission-floor convention, not
invented for this fixture) refuses assessment below it.

## The kept behaviour, attacked at its sharpest edge

The second reviewer built a sharper counterexample than either fixed bug:
a document that opens with the exact designated sentence, then **actively
retracts it** — no blockquote, no digression, an explicit "no longer
holds — a break-glass SSH path is now permitted with sign-off." This
*is* credited, correctly, by the shipped code, and the reviewer's own
finding is that it should be:

> The line the fix actually draws is syntactic, not semantic: a
> blockquote marker is a piece of markdown syntax whose entire job is to
> say "not my own words," detectable with zero understanding of meaning;
> a retraction ("no longer holds", "correction:", "superseded") requires
> actually understanding negation and supersession — exactly the category
> of judgment this mechanism has refused everywhere else in the file...
> Asking `firstStatement` to also detect a retraction would be asking it
> to stop being mechanical. So the code is on the side the PR claims —
> the line is principled and consistently drawn — but the comment's own
> example ("discusses something else") undersells how sharp the accepted
> risk actually is.

This is the decision worth keeping: a **syntactic** distinction
(blockquote = a markdown marker whose only job is "not my own words,"
checkable with zero understanding of meaning) is inside the oracle's
mandate; a **semantic** one (retraction = requires understanding negation
and supersession) is not, on the same grounds the oracle already refuses
paraphrase everywhere else — a reader's acceptance cannot live in a test.
The PR's own prose ("discusses something else") described the accepted
risk as topic drift; the actual accepted risk is active self-contradiction,
which is sharper than the stated version but not a different rule — the
review's non-blocking suggestion was to name the retraction case as
explicitly as the `cites` clause already names its own limit two sentences
later, not to change the code.

## Why this is worth a record and not just a review comment

The line drawn here — mechanical/syntactic detection stays in scope,
anything requiring understanding of meaning does not, even at its
sharpest and least comfortable edge — will bind every future extension of
this oracle to a new equivalence shape. It currently exists in one PR
review comment on `#1407`, not in `equivalence.go`'s own doc comment, and
not anywhere a future author touching this file would read before adding
a fifth clause.

## References

- agent-estate#1407 — the PR (`7bf1d557`→REQUEST-CHANGES two real false
  credits, `592c5f70`→APPROVE with the retraction case argued and kept)
- agent-estate#1318 — the retrieval-baseline stratum this oracle scores
