# 2026-09-08 — 233 of 352 vault facts excluded from `index.md`: decision (b), and what was actually missing

**Status:** decided and applied to the live vault (`$AGENT_MEMORY_VAULT`).
agent-estate#1314.

## The state that opened this

    $ ls "$AGENT_MEMORY_VAULT/01 - Notes/01f - Facts"/*.md | wc -l
    352
    $ grep -c '^- \[' "$AGENT_MEMORY_VAULT/index.md"
    119
    $ echo $((352 - 119))
    233

233 is the distilled-rules layer (`distilledRuleWeight = 1.6`, #1290)
outgrowing `index.md`'s 160-entry cap. All 233 are on disk and retrievable
by `estate knowledge query`; none appeared on the page headed `# Facts`
every entry point routes to.

## The decision: (b), established against the contract's own text

`99 - Meta/index-contract.md` caps `index.md` at 160 entries (measured 119
at A2-COMPLETION's write time) for progressive disclosure — it is the
`okf_version` carrier loaded at session start (OKF §12), not a browsing
index. Its own "What this does not do" section states the cap is
deliberate: *it does not require every fact in `01 - Notes/` to be indexed
— a fact can exist, be queryable, and simply not have earned index space
yet.* That is decision **(b)** stated in the contract's own words, not
argued around it. (a) — raising or removing the cap — would contradict
this text directly and was rejected.

## The defect (b) actually names, re-checked rather than assumed

The issue's own second half of (b) claims *"the excluded 233 have no
reachable hub."* Before building anything, this was checked against the
live vault rather than taken on the issue's word:

    $ comm -23 <(ls .../01f\ -\ Facts | sed 's/\.md$//' | sort) \
               <(grep -oE '[0-9]{12,14}' index.md | sort -u) \
      > excluded.txt   # 233 ids
    $ grep -ohE '\[\[[0-9]{12,14}\]\]|01f%20-%20Facts/[0-9]{12,14}\.md' \
      "02 - MOCs"/*.md | grep -oE '[0-9]{12,14}' | sort -u > moc-referenced.txt
    $ comm -23 excluded.txt moc-referenced.txt | wc -l
    0

**All 233 excluded facts are already referenced from at least one existing
topic hub under `02 - MOCs/`** — spot-checked as genuine markdown links
inside machine-generated `<!-- rules:start -->` sections (not a digit
coincidence), e.g. `02 - MOCs/review.md` links
`01 - Notes/01f - Facts/20260614222858.md`. `02 - MOCs/README.md` itself
still claimed *"None has been born yet as of P10 — no topic has crossed
the note-count threshold"* — also false today: 53 files under `02 - MOCs`
carry a `<!-- rules:start -->` section, 45 with at least one linked fact
(measured 2026-09-08; re-count before citing further).

So the issue's literal premise — no reachable hub exists — was false. The
real defect was narrower than the issue assumed: the mechanism already
existed and already covered all 233; `index.md` and two of the vault's own
navigation docs just didn't say so, and one of those docs actively said
the opposite.

## What changed (three files, all prose, no new subdirectory, no hub built)

1. **`index.md`** — added a paragraph after the existing intro: names the
   352/119 gap, cites `index-contract.md`'s own "What this does not do",
   and points to the two real discovery paths (`estate knowledge query`;
   topic hubs under `02 - MOCs`, e.g. `review.md`, `gate.md`, `memory.md`)
   plus `Start Here.md`. Bullet count unchanged (still 119) — this is a
   documentation addition, not a re-generation.
2. **`02 - MOCs/Notes.md`** — corrected the stale "119 durable facts...
   each is still listed individually in `index.md`" claim to 352, and
   restated the Facts bullet in the same "too many to cap-fit, bulk-
   reachable via the hub layer" shape the file already used, correctly,
   for Parameters.
3. **`02 - MOCs/README.md`** — corrected the stale "None has been born
   yet" claim about topic-hub emergence (true as of P10, false since) with
   the measured 53/45 counts above, and pointed out these are the
   mechanism `index.md` now names.

Not done, per the brief's constraints: the 160-entry cap was not raised or
`index-contract.md` changed (decision (b) says it shouldn't be); the whole
index was not regenerated; no new subdirectory was invented under
`01 - Notes/`; no new `02 - MOCs/Facts.md` hub was built, because the
233/233 measurement above showed one was not needed — building a
redundant fourth discovery path (index.md, `estate knowledge query`, topic
hubs, plus a new bulk hub) on top of infrastructure that already covers
the gap would have been the over-engineered fix, not the cheap one.

## Reachability proof — bounded hops from the entry point

Picked one of the 233 excluded facts (not in `index.md`'s 119):

    Start Here.md (line 43: "Open the subject hub in `02 - MOCs`")
      -> 02 - MOCs/review.md (line 25: links the fact)
      -> 01 - Notes/01f - Facts/20260614222858.md
         "The agent must screenshot and inspect its own work
          before asking Jon to review it"

Two hops from the documented entry point; one hop from any topic hub
directly. Reproducible for any of the 233 by the same `comm`/`grep`
sequence above.

## Vault safety

- Backed up `index.md`, `02 - MOCs/Notes.md`, `02 - MOCs/README.md`
  outside the vault (`/tmp/index-coverage-1314-vault-backup`) before
  editing — an earlier attempt to back them up inside the vault polluted
  the validator (it treats any file literally named `index.md` anywhere
  under the tree as a second contract carrier), corrected before any real
  edit was made.
- Standing-law `HashPrefix` (`26d4a45ea2a7`, #1286), via
  `corpus.StandingLaw($AGENT_MEMORY_VAULT)`:
  - Before: resolved cleanly, `HashPrefix 26d4a45ea2a7` matched.
  - After: resolved cleanly, `HashPrefix 26d4a45ea2a7` matched (unchanged
    — no file under `01 - Notes/` was touched).
- Note counts, before and after (unaffected by design — only prose in
  `index.md` and two `02 - MOCs` files changed):
  - 352 facts, 3,217 parameters, `## Context` on 2,915 Parameters — both
    measurements identical.
  - `index.md` bullet count: 119, unchanged.
- Validator (`python3 scripts/knowledge/vault/validate_index.py
  "$AGENT_MEMORY_VAULT"`), run from this checkout:
  - Before: `Contract holds: no hard violations.` (14 pre-existing,
    unrelated frontmatter warnings)
  - After: `Contract holds: no hard violations.` (same 14 warnings, no
    new ones — the new links in `index.md` resolve).

## Verify

    go build ./src/estate/...   # exit 0
    go test ./src/estate/...    # all packages pass except:

`internal/pressure`'s `TestBelowCapAllowsOrRefusesForTheRightReason` fails
environmentally tonight: `Check() refused below the cap on a measurable
host: [weekly budget 9% remaining, at or below the 10% stop threshold]` —
the real weekly token quota, not a regression; pre-briefed and confirmed
against unmodified `origin/main` by three lanes before this task started.

## References

- agent-estate#1314 — the issue this decision closes
- `99 - Meta/index-contract.md` — the contract decision (b) is drawn from
- `02 - MOCs/Notes.md`, `02 - MOCs/README.md`, `Start Here.md` — the
  vault's own navigation docs, two of which were stale and corrected here
- `run/inmaps-spec.md` §4 — topic-hub mechanized emergence, the mechanism
  this decision relies on and found already working
