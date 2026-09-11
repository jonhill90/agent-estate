# 2026-09-11 — provenance-review: when to stop calibrating a filter and change the shape of the artifact (#1403)

**Status:** decided and shipped. agent-estate#1394, agent-estate#1395.

## The tool, and the design question it forced

`estate provenance-review` renders every live parameter beside the prompt it
was judged from, so Jon can answer "did I ask for this, or did you make it
up?" himself. `text_raw` was private-only from the first design, never
serialized to the public path at all; the public render surfaced rule
bodies and cleaned text (`text_clean`, where available) gated by a
credential filter that withheld rows it judged sensitive from that public
set.

## Three review rounds, each attacking the filter and finding a new shape

Each round broke the filter with a constructed, plausible attack string —
not an observed live leak — reproducible against the shipped code; the
author closed the finding without narrowing the corpus, then the next
round broke the fix:

1. **Bare `token`.** The unqualified word "token" introducing a secret
   value slipped through entirely (`"paste the token I gave you into
   .env"` itself carries no actual secret — it demonstrates the exemption
   matches on the bare word alone, not a real credential leaking), because
   the exemption for cost-talk (`"watch token usage..."`) matched on the
   word alone. Closed with a cost-context rule plus a shape-only layer (hex
   runs, JWT segments, `key=value` assignments).
2. **Case-mixing.** The shape layer required `hasLower && hasUpper` (or
   punctuation) on an unbroken run, so a purely lowercase 80-character
   secret passed at any length — and, found in the same round, the fix's
   own false-positive cost: `secretShapedLiteral("audit-hill90-ui-client")`
   fired on an ordinary hyphenated identifier, withholding a benign prompt.
   Closed by requiring `hasDigit && hasLetter` instead of case-mixing, and
   by ending a run at any separator (`-_./`) so identifiers are judged
   segment by segment.
3. **No digit.** The digit requirement from round 2's fix meant a purely
   alphabetic secret was never caught at any length — Diceware-style
   recovery phrases and made-up passphrases carry no digit by construction.
   The reviewer's own suggested close, a 20-character alphabetic floor,
   would have re-flagged the same prompt round 2's shape rule had already
   false-positived on (`mp-ad05191fd122d49f`) — not via
   `audit-hill90-ui-client` itself, which is hyphenated into segments none
   20 characters long, but via a different string elsewhere in that same
   prompt, `ScheduledWorkflowSignalMissing`.

## The decision: stop calibrating, invert the design

The author tested the reviewer's own round-3 suggestion against the live
corpus before rejecting it, and found the fourth shape it couldn't reach
either:

> Three rounds, three genuinely different shapes, each found by attacking
> the previous fix, is the signature of an open-ended space, and the
> reviewer's own suggested fix for round 3 (a 20-character alphabetic
> floor) would have withheld `mp-ad05191fd122d49f` a second time — I tested
> it: 14 of 970 prompts carry an alphabetic run of 20+, every one a
> CamelCase identifier... Free text cannot be gated by shape.

`correct horse battery staple` — spaces, under 20 characters per word,
no keyword, no shape rule that doesn't also eat CamelCase code
identifiers — is the shape that closes the argument: not found on a fourth
attack, but named directly by the same reasoning as the first three.

So the artifact inverted rather than adding a fourth filter layer:

- **Public render:** ids, timestamps, recorded author, counts, weights,
  statuses, mechanical hints — no rule text, no cleaned text, no raw text.
  Safe by construction; needs no filter, because it carries nothing a
  filter would need to catch.
- **Private render (`--private`, terminal only):** everything — rule
  bodies, cleaned and raw text — where Jon's question actually gets
  answered, because he reads it locally.
- **The filter survives, demoted from gate to advisory.** A private-render
  row whose text looks like a credential or a personal arrangement is
  marked `DO NOT QUOTE PUBLICLY: <reason>` for a human copying a quote by
  hand. It no longer decides what gets published; nothing does, because
  the public path publishes no text at all.

Three rounds of attack strings were not wasted — they became the
advisory's rule set, verified again in round 4 against the reused corpus
(`prompts_flagged_do_not_quote: 120`, unchanged, since the inversion
changes what renders, not what the advisory flags).

## The measurement that justified it, re-derived independently

The "14 of 970" figure above was checked by the final reviewer against the
live corpus, not accepted from the PR body — a first attempt measured
*incremental* cost (new flags beyond the other three layers) and got 9,
which didn't match; reconciling to "the rule's own total match count,
independent of overlap" produced exactly 14, matching including the named
sub-claim that one of the 14 (`mp-ad05191fd122d49f`) was the same prompt
round 2's shape rule had already false-positived on.

## What this does not claim

The public render does not answer Jon's original question by itself — the
PR says so directly ("That is where the question gets answered, because
Jon reads it locally"), which the final review treated as the deciding
factor for why this is a disclosed trade, not a hidden one. The private
`--json` render does not carry `text_raw` (a structural gap — the struct
field is unexported, so `encoding/json` can't reach it regardless of
`--private`), which under-delivers relative to the PR's own description
but does not leak; noted for whoever next touches that flag.

## References

- agent-estate#1403 — the PR, four review rounds
  (`371bc24a`→REQUEST-CHANGES, `748929cd`→REQUEST-CHANGES,
  `4409a920`→REQUEST-CHANGES, `f9f1038d`→APPROVE)
- agent-estate#1394, agent-estate#1395 — the question this tool answers
