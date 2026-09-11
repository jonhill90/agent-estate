# The answer to "which parameters did I actually ask for" — 2026-09-11

Jon asked, this session, for exactly the question this document answers: to
distill the parameters down into something he could check against, so he
could tell which things he had actually asked for from which had been
invented on his behalf (`hp-15f4cb437a2d31c3`). `text_clean` is NULL for
that row, so it is described here rather than quoted, per this document's
own rule below — the request itself is not exempt from it.

Every number below was re-derived today, read-only, against
`~/corpus/corpus.sqlite3` (`file:...?mode=ro`, Python — the `sqlite3` CLI
fails on this file with error 14) and against `estate provenance-review`,
`estate textclean`, and `estate corpus-audit`, all merged tonight. Nothing
was written to the corpus, the vault, or the shared knowledge index. This is
not a tour of those tools — it is the answer they make possible, assembled
in one place for the first time.

## The two things that matter more than any percentage

**1. Nobody can answer "did Jon ask for this" today — only "does this item
match the prompt it is linked to."** `prompts.author` is `'unknown'` for
every one of the corpus's 12,516 rows, with no exception, re-checked directly
just now. The schema and capture-time mechanism to record authorship exist
(agent-estate#1398, #1401) and correctly register a supervisor/Director
message going forward — but nothing has gone back and labelled the existing
backlog, on purpose: a guessed label was judged worse than none (an
independent heuristic check found 4 false negatives — missed agent messages
that would become false Jon attributions — and either 0 or 1 false
positives depending on which of two sources you read: #1395's own issue
body and the PR that declined to act on it both say 0, but #1398's review
of that same 30-prompt check counted 1, and agent-estate#1394's own thread
flags the two as unreconciled, not settled. Either way the classifier is a
floor, not a fact, and nothing in this corpus treats it as one). So the
honest chain today is: *rule → linked prompt*, not *rule → Jon*. If the
linked prompt is itself something a supervisor or Director wrote to a lane,
a perfectly faithful rule still is not something he asked for. That
two-layer gap — faithfulness to the prompt, and authorship of the prompt —
is the actual finding, and it is more useful than any single clean
percentage would be.

**2. Most parameters cannot be quoted back to him at all, because
`text_clean` was never populated for them.** Of the corpus's 12,516 prompts,
only **181** (1.4%) have it. Narrowed to the prompts that actually sit behind
something the system enforces — the 970 distinct prompts behind the 1,359
live parameters — only **70** have it, covering **76 of the 1,359 parameter
rows (5.6%)**. `CLAUDE.local.md` is unambiguous: quote `text_clean`, never
`text_raw`; if a parameter worth citing has no cleaned text, describe it and
cite it by id, never quote the raw form. That rule is why almost everything
below is a description, not a quotation, and why running
`textclean --apply` — his decision, not made — is the single change that
would open the other 94.4%.

## What is actually enforced, and how much of it is checkable

Re-derived directly, matching `estate provenance-review --json`'s own
aggregate fields exactly:

| Measure | Count |
|---|---:|
| Live parameters (`live_parameters` view) | 1,359 — 1,213 hard, 146 preference |
| Distinct source prompts behind them | 970 |
| Live parameter rows whose source has `text_clean` | 76 of 1,359 (5.6%) |
| Distinct source prompts with `text_clean` | 70 of 970 (7.2%) |
| Prompts flagged do-not-quote (credential-shaped) | 0 |
| Corpus-wide: prompts with `text_clean` | 181 of 12,516 (1.4%) |
| Corpus-wide: `prompts.author` populated (not `'unknown'`) | 0 of 12,516 |

`estate textclean` (report mode, read-only, run against the live corpus
today — it has never been run with `-apply`): of the 900 prompts behind a
live parameter that lack `text_clean`, **259 would be cleaned** by the
whitelist-only fixer (a real spelling/apostrophe/capitalisation fix,
independently meaning-checked before being proposed), **641 are already
clean as written** (copied verbatim — nothing in the narrow whitelist
matched), **0 were refused**. Refusal is a real, expected outcome of this
tool (a change that cannot be verified meaning-preserving is left `NULL`
rather than guessed) — zero refusals today just means none of these 900
needed a fix the guard couldn't verify.

## Does the recorded rule say what its source says?

`estate corpus-audit`, re-run today: **1,213 hard parameters audited; 345
(28.4%) assert more than the prompt behind them** — a real instruction with
generalised or stronger obligation bolted on (`must`, `only`, `never` added
where the source didn't have it), not invention from nothing.

A broader, independently-sampled measurement exists for the same question
over a wider population — agent-estate#1394's own divergence census, run
against the 2,972-row **dispatch-eligible** population (every `weight=hard`
parameter, directive, and correction, excluding dropped/needs-review — a
larger set than the 1,359 live parameters, because it includes directives and
corrections too). Its population counts were re-checked against the live
corpus for this document and match exactly, row for row and kind for kind, so
the sample below is re-verified structurally, not just inherited:

| Classification | Sample rows (of 300) | Fraction |
|---|---:|---:|
| Faithful | 249 | 83.0% |
| Stronger than source | 22 | 7.3% |
| Not in linked source | 11 | 3.7% |
| Answer-for-question | 1 | 0.3% |
| Cannot tell / unresolved | 17 | 5.7% |
| **Divergent (middle three rows)** | **34** | **11.3%** |

95% sampling envelope over the full 2,972-row population, computed by exact
hypergeometric inversion: **8.1%–21.5%**. That is a statement about sampling
uncertainty on this one measurement, not a confidence interval Jon should
read as "the true rate is probably in the middle" — the census's own
authors called it a bounded, moderate-confidence estimate, not a census.

## Who wrote the source prompts — the layer the divergence numbers cannot see

Faithfulness to a prompt says nothing about who wrote the prompt. A separate
assessment (agent-estate#1395, Fable, no stake in the vault's contents)
measured that directly and reached the harder number:

> about half of the "hard" parameters and distilled facts trace to a prompt
> written by an agent (the supervisor or the Director talking to a lane),
> not by Jon.

Mechanism: the capture hook records every submitted prompt identically,
whether Jon typed it or a supervisor session sent it into a lane's pane.
Nothing captured at the time distinguished the two, and the mechanisms that
now partially can (`director_pane_reason`, noise-stripping, the judge's own
machine-authored flag) were all added after the affected population — mostly
August traffic — was already captured. The heuristic classifier behind the
"about half" figure agreed with hand judgement on 25 of 25 sampled notes, and
an independent reviewer's separate 30-prompt check found 4 false negatives
and either 0 or 1 false positives — #1395's own body and the PR that read
it both say 0, #1398's review of the same check says 1, and #1394's thread
has not reconciled the two — so the real fraction is a **floor** either way
(the reviewer's own estimate ran ~50–53%, not lower), never an upper bound
in Jon's favour.

## Twenty parameters, checked by hand, against their actual source

agent-estate#1394's own comment thread built and published a 20-row,
`text_clean`-only comparison — deliberately oversampled toward the rows that
*can* be quoted (10 of 20, against 5.6% in the real population), so it shows
shape, not rate. It is already public; reproduced here as a representative
slice rather than duplicated in full. A few of the twenty, verbatim from that
already-published table:

| # | the rule as stored | the source prompt's actual text | what the source supports |
|---|---|---|---|
| 2 | Do not deploy real production until it is actually ready — once it is up it must be ready. (hard) | *"We are not ready to deploy the real prod. Once that is up it needs to be ready, yeah? I mean we don't have a database or an admin UI or all kinds of things."* | supported |
| 5 | Sources must be valid: a source that resolves to a broken link or 404 is not acceptable. (hard) | *"Sources should be valid. We cannot have sources that go to broken links with a 404 page not found."* | supported |
| 9 | Tell Jon if he needs to install anything rather than silently requiring it. (preference) | *"Yeah, let me know if I need to install anything."* | supported |
| 18 | In the platform repo, if a fix would change what a deploy DOES rather than what it reports, say so explicitly and let **Jon** decide before writing it. (hard) | `text_clean` unavailable — described, not quoted, per the source table's own rule. A 2,030-character supervisor message to a lane, reviewing a merged PR, telling the lane that if a fix would alter what a deploy does rather than what it reports, it should say so and let **the supervisor** decide. | **stronger than the source, and misattributed** — the speaker's own "me" became "Jon" in the stored rule |
| 19 | Close administrative loose ends yourself in future rather than asking — bookkeeping is not a decision. (hard) | A supervisor message to a lane, stated about the supervisor's own behaviour, not Jon's. | **cannot tell** — the text supports the rule; nothing shows Jon asked for it |

Row 18 is the sharpest example in the whole assembled record: the exact "a
supervisor's own words become Jon's instruction" mechanism #1395 describes
at scale, caught in a hand-checked sample of twenty.

## What running `textclean --apply` would actually buy him

Today, `text_clean` is the *only* signal `estate knowledge get`'s disclosure
logic uses to decide a source is quotable
(`internal/knowledge/disclosure.go`'s `DisclosureAvailableClean` — non-empty
`text_clean` plus publishable, nothing else, checked directly against the
merged source for this document). **That cuts both ways.** Running
`-apply` — against a scratch copy with a verified backup, never the live
corpus; the tool refuses that outright — would, per today's report run,
populate `text_clean` for all 900 currently-unclean prompts behind live
parameters (259 with a real, verified fix; 641 copied verbatim because
nothing in the whitelist matched; 0 refused), closing the second finding
above for the whole live-parameter set. But it would do that with **no author information attached
at all**: a newly-cleaned, supervisor-authored prompt becomes
`available_clean` exactly like a newly-cleaned prompt Jon typed himself,
because nothing downstream reads `author` yet. That is not a defect in the
`textclean` tool — it does exactly what it says, a meaning-preserving
spelling pass, nothing more — it is a gap in what happens *after* cleaning,
and it is the same gap agent-estate#1394's own correction thread named
before `textclean` existed. Applying it now would answer "what does this
say" for most of the enforced set. It would not, on its own, answer "who
said it" for any of them.

## Where to actually do the row-by-row work

`estate provenance-review` (agent-estate#1403, merged) is the tool built for
exactly this — not a report, a worksheet. Public mode (the default) shows
structure and mechanical hints only, no text. `--private` renders each
source prompt and every rule judged from it, locally, with the
do-not-quote advisory on anything credential-shaped, and leaves the
judgement column — supported / stronger than the source / not supported /
cannot tell — for the reader to fill in. It groups the 970 source prompts by
how many live parameters they support, so the highest-leverage prompts to
review are first. Nothing about this task changed, decided, or wrote back
any of them; the 51-item handoff agent-estate#1394's own investigation
prepared (34 divergent, 17 unresolved, with full source evidence) is a
ready-made starting queue for whoever does that review.

## What this document did not do

It did not re-run the 300-row blind classification or the authorship
heuristic — both are substantial independent studies, already run twice
(once challenged by a `devils-advocate` pass each time), and re-deriving
their *population counts* today (which matched, exactly, row for row) is
what "re-derive every number" reasonably means for numbers of that shape. It
did not label a single row's author, clean a single row's text, or change
any status, weight, or rule. It did not quote a single character of
`text_raw`.

## References

- agent-estate#1394 — the original measurement, its correction, the 20-row
  comparison, and the divergence census
- agent-estate#1395 — the authorship assessment
- agent-estate#1398, #1401 — author capture schema and wiring (forward-only;
  no backlog label)
- agent-estate#1403 — `provenance-review`, the review surface
- agent-estate#1405 — `textclean`, merged after six review rounds; never
  run with `-apply` against the live corpus
