# Push 4.5 C2 — associative tagging pass (agent-estate#1279 unblocked it)

Lane: agent-estate:1 (lane-a). Single vault writer for this work per Director's
lock. Precondition confirmed: agent-estate#1279 merged as `dbc84bd` on
`origin/main`; `notemeta.Merge` preserves associative tags through
regeneration (independently verified in my own #1279 review, reproduced
again below on real notes).

## Verification of Director's numbers — DO NOT TRUST, VERIFY

Director's message said "VERIFY MY NUMBERS." Ran the same commands myself.

```
$ find "$AGENT_MEMORY_VAULT/01 - Notes/01p - Parameters" -name '*.md' | wc -l
    2638
$ find "$AGENT_MEMORY_VAULT/01 - Notes/01f - Facts" -name '*.md' | wc -l
     119
$ find "$AGENT_MEMORY_VAULT/01 - Notes" -name '*.md' | wc -l
    2757
```

2638 + 119 = 2757. **Confirmed exactly.**

Exclusivity snapshot (no other lane writing):
```
$ find "$AGENT_MEMORY_VAULT" -mmin -10 -name '*.md'
(no output)
```

Tag census, verbatim:
```
$ grep -rh '^tags:' "$AGENT_MEMORY_VAULT/01 - Notes" | sort | uniq -c | sort -rn
1969 tags: [note, 08-2026, standing-rule]
 443 tags: [note, 07-2026, standing-rule]
 145 tags: [note, 08-2026]
  63 tags: [note, 07-2026]
  17 tags: [note, 06-2026, standing-rule]
   1 tags: ["07-2026","azure","cloud","note","standing-rule"]
```
**Confirmed exactly** — 1969+443+145+63+17+1 = 2638, matching Director's numbers
digit for digit.

**Correction to the shape, not the numbers:** that census sums to exactly
2638 — the `01p - Parameters` count — because it is a `grep -rh '^tags:'`
over the whole `01 - Notes` tree, and **zero of the 119 Facts notes carry a
`tags:` line at all** (checked directly: `grep -rl '^tags:' ".../01f -
Facts" | wc -l` → `0`). This isn't a discrepancy in Director's arithmetic —
the arithmetic is exactly right — it's a gap in what the one-line census
shows: Facts notes aren't "topically bare like Parameters notes are,"
they have no tag mechanism engaged at all yet. Matches the plan doc's own
baseline citation ("0/119 facts tagged"). Both counts are consistent;
flagging so nobody reads "2638 = the whole census" and assumes Facts were
covered.

Also found during the pre-flight scan: **2 of the 119 Facts notes carry a
non-empty `candidate_id`** (reviewed publications with a persisted receipt)
— `TagNotes` refuses these by design (agent-estate#1279's own guard: "reviewed
publication requires receipt-aware tag update"). These 2 are excluded from
every batch below and remain untagged until that receipt-aware update is
built (out of C2's scope per the PR's own doc). Confirmed via
`grep -h '^candidate_id: [a-zA-Z0-9]'`.

## Vocabulary — extended first, in its own applied step, before any tagging

Ordering honored: vocabulary extended and confirmed on disk before batch 1's
first write.

**Derivation, not invention:** ran a frequency analysis over every note's
real body content (title + first paragraph, frontmatter and the mechanical
"Projection of corpus item..." boilerplate line stripped out first — that
boilerplate would otherwise swamp the signal with `corpus`/`item`/`prompt`).
Script and raw counts are in this report's evidence bundle
(`/tmp/c2-artifacts` — paths below are ephemeral scratch, not committed
anywhere; the only persistent artifact is this report plus the vault
itself). Top measured terms (word-boundary count across all 2757 note
bodies, stopwords removed):

```
299 fix        266 lane       194 estate     180 failure   175 repo
169 loop       156 supervisor 154 session    149 skills    143 deploy
138 claude     136 tmux       126 merge      122 review    102 memory
100 harness     96 api         95 vault       93 context    91 dotfiles
 89 knowledge   84 dispatch    84 branch      83 sweep      82 tui
 81 corpus      78 director    78 commit      73 github     73 token
 71 git         54 container   52 workflow    48 cron       48 auth
 47 docker      46 quota       45 gate        40 codex      39 reviewer
 36 cli         35 worktree    35 ledger      33 pipeline   32 security
 32 restore     32 reasoning   31 credential  31 identity   28 backup
 26 policy      25 candidate   24 testing     24 query      22 search
 21 secrets     21 spend       20 reference   20 index      19 batch
 16 provenance  16 daemon      16 python      15 budget     15 disclosure
 14 keychain    13 subagent     9 socket       7 validator    6 catalogue
  5 azure        5 pressure     4 obsidian     4 macos       4 kubernetes
  3 zsh          2 cloud        1 moc          1 tag         1 isolate
```

Chose **50 new values** from this list (kept SMALL, per instruction, despite
the frequency table running much longer): every value that is (a) a genuine,
distinct subject a person would associate a note with, not a generic verb/
function word (`fix`, `failure`, `loop`, `session`, `repo` excluded — `repo`
specifically to avoid colliding with the existing `kind/repo` axis value),
and (b) not redundant with another chosen value covering the same ground
(`query`/`search`/`index` collapsed toward `knowledge`; `policy`/`candidate`
dropped as too vague or already covered by lifecycle machinery; `daemon`
dropped as redundant with `supervisor`/`estate`; `python`/`macos`/
`kubernetes`/`zsh`/`socket`/`validator`/`catalogue` dropped as too rare —
under 10 raw hits across 2757 notes — to be worth a governed slot).
`obsidian` (4 hits) and `azure`/`cloud`/`pressure` (5, 2, 5 hits) are kept
despite low raw frequency because they name real, distinct, recurring
subsystems/tools this vault's own notes are about, not incidental word
coincidences, and Director named `obsidian` and `azure` explicitly as
expected. Final 50, applied via `estate candidates memory -action
tag-vocabulary`:

```
api auth backup branch budget codex container context corpus credential
cron deploy director disclosure dispatch docker dotfiles estate gate git
github harness identity keychain knowledge lane ledger memory merge
obsidian pipeline pressure provenance quota reasoning restore review
reviewer secrets security skills supervisor sweep testing tmux token tui
vault workflow worktree
```

Every value checked against the SAME regex `TagNotes`/`ExtendTags`
enforce (`^[a-z]+(?:-[a-z0-9]+)*$`, no slash) before submission — all 50
pass. `kind` was not touched; it stays a frontmatter-only value per
inmaps-spec §3, confirmed by reading `99 - Meta/tags.md`'s own `kind`
section, which already states the rule in its own words.

```
$ estate candidates memory -action tag-vocabulary -proposal vocab-extension.json -vault "$AGENT_MEMORY_VAULT"
50
$ estate candidates memory -action tag-vocabulary -proposal vocab-extension.json -vault "$AGENT_MEMORY_VAULT" -apply
50
```
Backed up `99 - Meta/tags.md` (sha256 recorded) before the apply. File now
carries 63 governed rows (13 original axis values + 2 from the #1279 proof
+ 50 new). Reviewed the diff directly — every new row landed as a
`| \`value\` | Association |` table row under the existing "Associative
vocabulary — Push 4.5" heading; nothing else in the file changed.

## Tagging methodology — associative matching, stated plainly

2-5 tags per note is Director's target. To hit that at 2757-note scale
without inventing associations, I built a reproducible, auditable
keyword-salience matcher rather than reading each note by hand: for every
note's title + body (boilerplate stripped, frontmatter stripped), count
whole-word-or-recognized-inflection hits (`deploy` also matches `deploys`/
`deployed`/`deploying`/`deployment` — a prefix-to-word-boundary match, so
"tenant deployment tooling" earns `deploy`) for each of the 52 governed
associative values (50 new + `azure`/`cloud`), then take the top 5 by
in-note frequency. This is a *literal* implementation of "what comes to
mind reading this" — if a note's own text names a system, that's the
association; nothing is assigned that the note doesn't itself mention.

**Honest result: this does not reach every note.** Of 2755 tag-eligible
notes (2757 minus the 2 receipt-guarded facts):

- **1423 notes** matched ≥1 governed value and are in the tagging plan.
- **1332 notes** matched **zero** governed values and are left untagged —
  not because of time, but because nothing in the governed vocabulary
  associates with their content (sampled several by hand to confirm this
  isn't a matcher bug: `"Cut #78."`, `"Stop dumping a full book of detail
  — answer plainly and briefly."` — genuinely topic-less one-line
  corrections/directives). Forcing a tag onto these would invent an
  association that isn't there, which the vault's own "missing metadata
  is never fabricated" rule forbids as much as fabricating a missing
  field would.

Tag-count distribution across the 1423 planned notes:
```
1 tag:  802 notes    2 tags: 345 notes    3 tags: 122 notes
4 tags:  51 notes    5 tags: 103 notes
```
Most land on 1-2 tags, which is honest for terse, single-directive
parameter notes — Jon's golden rule is "what comes to mind," and for a
one-sentence corpus item, that is often exactly one thing.

**Correction to Director's batch-count expectation:** "Batches of ~200...
before you run 13 more" assumed ~14 batches over all 2757 notes. Because
only 1423 notes have any planned write, the actual batch count is **8**
(seven of 200, one final batch of 23) — not 14. Flagging this now, per
"verify my numbers, do not trust them" cutting both ways: my own plan's
shape should be checked before I run the other 7.

## W1 batch discipline — batch 1 of 8

Validator baseline, run before any tagging write:
```
$ python3 tools/validate_index.py   # from 99 - Meta/
Checked 2757 fact file(s) against .../index.md
Warnings (14, non-blocking): [pre-existing missing description/title on
  8 recent Facts notes -- unrelated to tags, present before this pass]
Contract holds: no hard violations.
$ echo $?
0
```

**Preserve, don't replace — proven before running the batch at scale.**
Sample diff, a Parameters note with existing structural tags:
```
BEFORE: tags: [note, 06-2026, standing-rule]
AFTER:  tags: ["06-2026","deploy","note","standing-rule"]
```
`note`, `06-2026`, `standing-rule` all survived; `deploy` was added. This
is the additive proof Director asked for, run on the first real batch, not
a synthetic fixture.

**Batch 1 (200 notes) — full run:**
```
$ sha256 every target file -> backups/batch-01/manifest.before.txt (200 lines)
$ estate candidates memory -action tag -proposal batch-01.json -vault "$V"
200                                    # dry run: 200 notes would change
exit: 0
$ estate candidates memory -action tag -proposal batch-01.json -vault "$V" -apply
200                                    # apply: 200 notes changed
exit: 0
$ sha256 every target file -> backups/batch-01/manifest.after.txt
$ python3 tools/validate_index.py
Checked 2757 fact file(s) against .../index.md
Warnings (14, non-blocking) -- the SAME 14 pre-existing warnings, none new
Contract holds: no hard violations.
exit: 0
```
No failure occurred; no restore was needed. `find "$V/01 - Notes" -mmin -5
-name '*.md' | wc -l` → `200`, exactly the batch size — confirmed nothing
outside the batch's own 200 files was touched.

Post-batch-1 tag census (verbatim, top of the distribution):
```
1969 tags: [note, 08-2026, standing-rule]
 377 tags: [note, 07-2026, standing-rule]     (was 443 -- 66 of those notes
                                                now carry topical tags too
                                                and moved to their own
                                                distinct census lines)
 145 tags: [note, 08-2026]
  44 tags: [note, 07-2026]                     (was 63)
  14 tags: [note, 06-2026, standing-rule]      (was 17)
   6 tags: ["07-2026","note","review","standing-rule"]
   5 tags: ["07-2026","lane","note","standing-rule"]
   ... (170 distinct tag-set lines total, up from 6 before this pass)
```

## Director confirmation and two vocabulary merges (2026-09-07)

Director confirmed item 1 as proposed: 8 batches, the ~1330-odd zero-match
notes stay untagged, recorded below as a coverage number, not a failure.

Director confirmed item 2 with two merges, made at the cheapest possible
moment (only batch 1's 200 notes were tagged against the pre-merge
vocabulary, and I had its checksummed backup):

- **`reviewer` collapsed into `review`** — a note about reviewer selection
  is a note about review.
- **`secrets` collapsed into `credential`**.
- **`keychain` kept separate** — the macOS Keychain is a specific system
  under a standing hard rule (read-only, never probe); a tag that surfaces
  those notes distinctly earns its slot.

This takes the new associative set from 50 to **48 values** (52 associative
rows total with the pre-existing `azure`/`cloud`).

**Execution, in order:**
1. Restored batch 1's 200 notes from the checksummed backup taken before
   the first apply. Re-hashed and diffed against the pre-batch manifest:
   byte-identical. Validator re-run: exit 0.
2. `candidates memory`'s tools have no "remove a governed value" action —
   `ExtendTags` only appends. Collapsing an already-applied value is a
   correction to the vocabulary file itself, which the vault's own
   extension rule in `tags.md` explicitly permits as "a direct vault edit
   logged in `log.md`" as the alternative to a PR-driven change. Backed up
   `tags.md` (sha256 recorded) before editing; removed the `reviewer` and
   `secrets` rows, folding their meaning into the `review`/`credential`
   rows' own text; logged the edit in `99 - Meta/log.md` under a new
   `## 2026-09-07` heading, in that file's existing format. Validator
   after: exit 0. `tags.md` now carries 61 rows (was 63).
3. Rebuilt the tagging plan from scratch against the corrected 48-value
   vocabulary (regex/matcher unchanged otherwise — no widening, no loosened
   matching, per Director's explicit instruction not to touch that to close
   the coverage gap). New totals: **1414 notes planned** (was 1423 — a few
   notes whose only matches were `reviewer`/`secrets` now collapse into an
   already-present `review`/`credential` hit and lose a slot), **1341 no-
   match** (was 1332), same **2 receipt-guarded exclusions**. Still exactly
   **8 batches** (seven of 200, one of 14).
4. Ran batch 1 (fresh, against the merged vocabulary) through batch 8,
   full W1 discipline every time, no further check-ins, per instruction.

## Discrepancy resolved: 2, not 119

Director flagged that a summary line read as though all 119 Facts notes
were receipt-guarded. The measured number is **2**, not 119. Command and
full output, run again just now to state it plainly:

```
$ grep -l "^candidate_id: [a-zA-Z0-9]" "$AGENT_MEMORY_VAULT/01 - Notes/01f - Facts"/*.md
.../01 - Notes/01f - Facts/202608230005.md
.../01 - Notes/01f - Facts/202609060005.md
```
Two files. **117 of 119 Facts notes were taggable and got tagged** by this
pass (modulo how many of those 117 also had zero vocabulary-match content,
which is the separate 1341-note gap, not this guard). Only these exact two
are refused by `TagNotes`'s receipt guard, both of which were excluded from
every batch. Wherever a prior line of mine read as "119 refused," that
was imprecise phrasing, not the measured fact — the measured fact is the
list above.

## W1 batch discipline — batches 1 through 8, complete

Same discipline as batch 1's first run, every time: checksummed backup of
every target file before, dry run, apply, checksum after, validator,
restore-and-halt on any failure. **No failure occurred in any of the 8
batches; no restore was needed past the deliberate batch-1 restore for the
vocabulary fix above.**

| Batch | Notes | Dry run | Apply | Validator | Result |
|---|---|---|---|---|---|
| 1 | 200 | exit 0 | exit 0 | exit 0 | OK |
| 2 | 200 | exit 0 | exit 0 | exit 0 | OK |
| 3 | 200 | exit 0 | exit 0 | exit 0 | OK |
| 4 | 200 | exit 0 | exit 0 | exit 0 | OK |
| 5 | 200 | exit 0 | exit 0 | exit 0 | OK |
| 6 | 200 | exit 0 | exit 0 | exit 0 | OK |
| 7 | 200 | exit 0 | exit 0 | exit 0 | OK |
| 8 | 14  | exit 0 | exit 0 | exit 0 | OK |

Every validator run reported the SAME 14 pre-existing, non-blocking
warnings (missing `description`/`title` on 8 recent Facts notes, present
before this pass started) and no new ones, across all 8 runs.

**Post-hoc integrity check, run after all 8 batches, independent of the
per-batch script:**
```
$ # every planned tag is actually present on disk, for all 1414 notes
notes missing a tags: line entirely: 0
planned tags not found on disk: 0

$ # structural tags survived on every Parameters note tagged this pass
parameters notes checked for 'note' structural tag survival: 1302
missing: 0

$ # the 1341 no-match notes and 2 excluded notes were not touched
no-match notes count: 1341
excluded note .../202608230005.md candidate_id present: True
excluded note .../202609060005.md candidate_id present: True

$ python3 tools/validate_index.py   # from 99 - Meta/, final standalone run
Checked 2757 fact file(s) against .../index.md
Contract holds: no hard violations.
$ echo $?
0
```

**`reviewer`/`secrets` never landed anywhere:**
```
$ grep -rh '^tags:' "$AGENT_MEMORY_VAULT/01 - Notes" | grep -c '"reviewer"'
0
$ grep -rh '^tags:' "$AGENT_MEMORY_VAULT/01 - Notes" | grep -c '"secrets"'
0
```

## Final tag census

```
$ grep -rh '^tags:' "$AGENT_MEMORY_VAULT/01 - Notes" | sort -u | wc -l
592          # distinct tag-set combinations (was 6 before this pass)
```

Per-value usage across the vault, all 48 new associative values plus
`azure`/`cloud` (none at zero — every governed value earned real use):
```
174 lane        148 deploy      109 auth        101 review      101 estate
 90 merge        90 git          86 api          83 token        81 skills
 80 director     79 sweep        70 tmux         68 vault        68 supervisor
 61 workflow     61 harness      60 container    59 context      57 knowledge
 57 dispatch     53 github       52 memory       50 branch       49 dotfiles
 47 tui          44 cron         42 corpus       40 gate         39 docker
 34 credential   33 restore      31 quota        28 pipeline     27 codex
 23 reasoning    22 identity     21 worktree     19 testing      19 security
 19 backup       15 cloud        12 ledger       12 budget       11 provenance
  8 disclosure    4 azure         3 obsidian      3 keychain      1 pressure
```

## Coverage — an honest number, not a failure

- **2757** total notes under `01 - Notes` (2638 Parameters + 119 Facts).
- **2** excluded: receipt-guarded Facts notes (`TagNotes`'s own refusal;
  out of scope until a receipt-aware update exists).
- **1414** tagged this pass, associatively, through the tool, in 8
  checksummed/validated batches.
- **1341** left untagged: their own text contains no occurrence (or
  morphological variant) of any of the 48 governed associative values (plus
  `azure`/`cloud`). Per Director's confirmation, this stands as the honest
  measured coverage number, not a shortfall to close by widening the
  vocabulary or loosening the matcher — an invented association would
  violate the same "missing metadata is never fabricated" rule that governs
  every other field in this vault.

Total: 2 + 1414 + 1341 = 2757. Accounts for every note.

## Stated limitation — read into C4's gate, not fixed here

The matcher assigns a tag only when a note's own text already names the
governed term (or an inflection of it). That is deliberately non-inventive
— it never guesses an association the note doesn't itself state — but it
means the tags this pass produced **largely echo words BM25 already
indexes from the same body text**. A tag query against these 1414 notes
will frequently surface the same result set a plain full-text search for
the same word would have surfaced anyway.

The genuine value Jon's associative-tagging rule ("what comes to mind," not
"where it belongs") is chasing is the association that is NOT present in
the text — the connection a reader makes that the words alone don't state.
This mechanical pass, by construction, cannot produce those; that would
require actually reading and judging each note, which is out of this
pass's scope and budget.

**This is not something to fix in C2.** It is a fact to carry into C4:
the gate question ("show me the azure parameters") must be judged on
whether the tag surfaces something BM25 alone would have missed — a note
where `azure` is the association but the word "azure" itself doesn't
appear in the body. If every gate-passing result is also a body-text hit,
the gate has passed on a tautology (tag matches word, word matches word),
not demonstrated the associative layer's actual value. C4 should test with
that distinction in mind, not just confirm the query returns non-zero
results.

## What remains after C2 (updated by the C4 section below)

C1 (retrieval wiring, #1279) and this C2 tagging pass are complete. C3
(relation proposer) is separate and still not started. C4 (MOC
follow-through and the retrieval gate) follows in its own section below.

---

# C4 — MOC follow-through and the retrieval gate

Director's framing, taken at face value rather than defended against: C2's
matcher assigns a tag only when the note's own text already contains the
term, so the tags may just echo what BM25 already indexes from the body.
This section measures that, not argues it. Two parts: MOC proposals
(threshold firing), then the gate — three topics, tag-query vs. plain-text
query, set differences reported both ways.

## Part 1 — MOC proposals

Verb confirmed from `main.go`'s own switch, not the plan doc's prose:
`estate candidates memory -action moc-propose` (`MOCProposals`),
`-action moc-accept`/`-action moc-reject -id <name> -reviewer <actor>`
(`ReviewMOC`), `-action refresh` (`RefreshMOCs`) — `internal/candidates/moc.go`.

**Three real defects found running the existing, merged proposer against
the live, freshly-tagged vault** — not introduced by C2, all pre-existing,
all blocking, all fixed and shipped as agent-estate PR #1282
(`fix/moc-proposer-recursive-glob`, three commits, not merged — Director
merges):

1. **Non-recursive note discovery.** `MOCProposals`/`RefreshMOCs` used
   `filepath.Glob(vault + "/01 - Notes/*.md")` — flat, matches nothing one
   directory deeper. Every real note in this vault lives under
   `01p - Parameters/` or `01f - Facts/` (agent-estate#942's own
   note-subdirs registry). Measured: `moc-propose` printed `null` against
   a vault with tags counted in the dozens, well past the >=8 threshold.
   Fixed with a `filepath.WalkDir`-based `walkNotes`, matching the
   traversal `internal/knowledge/vault.go` already uses correctly for the
   same tree.
2. **Hard-abort on the first ungoverned tag.** Every real note also carries
   structural/time tags (`note`, `MM-YYYY`, `standing-rule`) that are not
   in `99 - Meta/tags.md`'s governed vocabulary (they're generated
   bookkeeping, never MOC-eligible) and that clear the threshold on nearly
   any vault. `MOCProposals` treated `validateINMAPS`'s rejection of one of
   these as fatal and returned immediately — aborting the whole batch
   before a single real, governed tag was ever reached. Measured, after
   fix 1 alone: `moc-propose` failed outright with `tag outside
   vocabulary: 07-2026`, zero proposals for azure/deploy/estate/etc.
   Fixed: skip an ungoverned tag rather than abort (every other
   `validateINMAPS` failure this call could produce is impossible here,
   since `MOCProposals` builds every field except `Tags` itself).
3. **Broken generated links.** `mocLinks` built every wikilink from
   `filepath.Base(p)` alone — `../01%20-%20Notes/<id>.md` — correct only
   if notes sit directly in `01 - Notes`. Measured, after fixes 1+2: the
   first real `moc-estate.md` draft's very first link,
   `../01%20-%20Notes/202608030161.md`, pointed at a file that does not
   exist; the real file is at `01 - Notes/01p - Parameters/202608030161.md`.
   Every entry in every one of the (then) 42 hubs would have shown
   Obsidian a broken link. Fixed: `mocLinks` now takes `vault` and computes
   each note's path relative to `01 - Notes` via `filepath.Rel`, preserving
   the subdir.

All three: new regression test, mutation-tested (reverted the fix,
confirmed the specific test fails for the specific stated reason, reverted
the mutation, confirmed the whole module green again), full
`go build ./... && go vet ./... && go test ./...` green. Ran from a
binary built off this branch (not off `origin/main`, since the fix is
unmerged) — the CODE is in PR #1282 for independent review; the VAULT
writes it produced are directly inspectable in the vault right now, same
as every other operation in this report.

**Threshold firing, actual measured counts** (from the ground-truth tagging
plan, filtered to `status: stable` — the exact filter `MOCProposals`
itself applies):

```
$ estate candidates memory -action moc-propose -vault "$AGENT_MEMORY_VAULT"
[42 proposal paths]
```
45 of the 48 new associative tags cleared the raw >=8-note threshold;
**42 of those also had >=8 STABLE notes** (the filter `MOCProposals`
applies, correctly — I checked directly and confirms it, not a bug):
`budget` (6 stable of 12), `ledger` (2 of 12), `provenance` (7 of 11), and
`disclosure` (5 of 8) fell under 8 once non-stable notes were excluded and
correctly did not propose.

**Accept decision.** All 42 proposals' stable-note density, sorted:
```
14 cloud        14 security     15 worktree     16 backup       16 codex
18 identity     18 testing      19 corpus       21 reasoning    23 pipeline
24 quota        28 credential   30 tui          31 gate         31 restore
35 dotfiles     36 cron         37 dispatch     37 memory       37 supervisor
39 branch       39 docker       40 context      40 github       44 tmux
45 knowledge    46 harness      56 container    56 workflow     58 director
60 vault        62 git          62 skills       64 estate       64 merge
68 token        71 review       72 sweep        77 auth         80 api
127 lane        136 deploy
```
No existing hub covered any of them (`02 - MOCs` held only the 7 structural
area hubs — Agents/Inbox/Meta/Notes/Parameters/Projects/Sources — before
this pass). The lowest density here is `cloud`/`security` at 14 — 75% over
the threshold, not a borderline call. **None of the 42 looked marginal by
any reading; all 42 were accepted**, none merely listed. Had any come in
at, say, 8-10, I would have listed rather than accepted it — none did.

**Applied through the tool**, batched as one reviewed set of 42 (not 200 —
this is a review decision on 42 items, not a bulk mechanical operation):
```
$ estate candidates memory -action moc-propose -vault "$V" -apply
42 drafts written to 00 - Inbox
$ for f in 00 - Inbox/moc-*.md; do
    estate candidates memory -action moc-accept -id "$(basename "$f")" \
      -reviewer "process:agent-estate-lane-a" -vault "$V" -apply
  done
# all 42 succeeded, 0 failures
$ python3 tools/validate_index.py   # from 99 - Meta/
Contract holds: no hard violations.
$ echo $?
0
```
Post-accept state: 42 new `status: stable` hub files in `02 - MOCs`; the
42 Inbox drafts remain (per `ReviewMOC`'s own design — an accepted draft
is marked `deprecated` in place, never deleted) now `status: deprecated`.

**Link-integrity check, run independently of the tool, over every accepted
hub:**
```
hubs with generated links checked: 42
total links: 1906
broken: 0
```
Backed up before any write (`00 - Inbox`, `02 - MOCs` full copy +
checksums); validator green before and after every step; no restore was
needed.

### Part 1, re-run for real after #1282 merged (`8ba75ac`)

All four review-found defects are merged to `main` (`8ba75ac4e6`,
approved by lane-b independently re-running the CLI against the live
vault and the mutation test). Built off `main` at `8ba75ac` directly (no
rebase needed -- nothing of mine was pending on top of it), confirmed
vault exclusivity (`find ... -mmin -10` empty) and a clean validator
baseline, then ran the real, merged proposer:

```
$ estate candidates memory -action moc-propose -vault "$AGENT_MEMORY_VAULT"
estate: 4 tag(s) past the threshold were not proposed (not in the governed
vocabulary): 07-2026 (233 notes), 08-2026 (972 notes), note (1208 notes),
standing-rule (1208 notes)
null
```
Ran `-apply` too, for completeness, not because a different result was
expected: checksummed every file in `00 - Inbox` and `02 - MOCs` before
and after -- **byte-identical**, confirming zero writes even in apply
mode, not merely a dry-run artifact.

**Reported as the actual result, exactly as instructed, not treated as
something to route around: no new MOCs were proposed.** The threshold now
correctly finds every tag past >=8 -- 46 of them, matching lane-b's own
measured count exactly (4 skipped ungoverned + the 42 already accepted and
covered in this pass's earlier run = 46) -- and correctly proposes nothing
new, because every governed topic already has a hub from the run
completed before #1282's merge. **"The threshold now works and correctly
proposes nothing new" is the finding; it is what the evidence supports,
not a null result to be worked around.**

No accept/reject decisions were needed this round -- there was nothing to
decide on. Vault state unchanged: `02 - MOCs` still holds exactly 50 files
(8 structural area hubs + the 42 topic hubs from Part 1's first pass);
validator green before and after (`Contract holds: no hard violations.`,
exit 0 both times).

## Part 2 — the gate

### Criteria, stated before running anything

Written here BEFORE the discriminating test below is run, per instruction.

- **The tags earn their place** if, for at least one of the three test
  topics, the tag-query result set (`#topic`) includes one or more notes
  that a plain search of the note's own original body/title text for the
  same word (and its ordinary inflections) would NOT have found — i.e., a
  genuine association the matcher could not have produced by construction,
  or the retrieval tool's own text-matching pipeline missing something the
  exact-tag filter still catches (e.g., a stemming gap).
- **The result is the tautology outcome** if, for all three topics, the
  tag-query set is empty, or a subset of, or near-identical to the
  plain-text-query set — i.e., every note the tag surfaces is also found
  by searching for the bare word, so the tag added no reachable note the
  word alone would not have surfaced.
- Given C2's own matcher design (assign the tag ONLY when the note's text
  already contains the term or an inflection of it, capped at the top 5
  matches per note), the tag-found/text-missed direction is expected to be
  small-to-empty **by construction** — this is stated here, before
  running anything, as a predicted, not discovered, result; the
  measurement is run in full regardless, and any exception to this
  prediction is exactly the interesting finding.
- The text-found/tag-missed direction (a note whose body says the word but
  didn't get the tag) is expected to be non-trivial and NOT evidence
  against the tags — it is the known, already-disclosed effect of the
  5-tag-per-note cap (a note mentioning six systems only keeps the top 5
  by in-note frequency) and is reported as that, not conflated with the
  tautology question.
- Three topics: `deploy` (highest-frequency, 148 tagged notes / 136
  stable), `estate` (mid, 101 tagged / 64 stable), `azure` (the plan's
  named case, low-frequency, 4 tagged notes — below the MOC threshold but
  still a real governed tag from the #1279 proof).

### Method

Two independent private indexes, neither ever touching
`~/.local/state/agent-estate/knowledge/index.json`:

- **Index A (tagged, current)**: `ESTATE_KNOWLEDGE_INDEX` pointed at a
  scratch path, `AGENT_MEMORY_VAULT` the real vault, built via bare
  `estate knowledge` — this is the sanctioned private-index path used
  throughout. Query with `#topic` for the tag-side set (exact `SynapticTags`
  match, agent-estate#1279's own mechanism).
- **Index B (pre-tagging reconstruction)**: a scratch copy of the ENTIRE
  real vault, with the 1414 notes C2 touched restored to their exact
  pre-batch content from this pass's own checksummed batch backups
  (verified byte-identical against the original `manifest.before.txt` for
  a sample note before use). This index has ZERO topical tags anywhere —
  it is what the vault looked like before this push started. Query it with
  the BARE word (no `#`) for the text-side set. This avoids the
  contamination a same-index comparison would have: `bm25.go` folds
  `SynapticTags` into the same searchable text tier1/tier2 already
  occupies, so a plain-word query against the CURRENT (tagged) index would
  partly match VIA the tag itself, not proving anything about the body
  text alone. Querying a genuinely tag-free reconstruction is the only
  clean way to isolate "what would full-text search alone have found."
- Index B's reconstruction: a full scratch copy of the real vault, with the
  1414 notes this pass tagged restored to their exact pre-batch content
  from this pass's own checksummed batch backups. Spot-checked one restored
  note against `manifest.before.txt` before use: byte-identical.
- `estate knowledge query`'s own CLI has no `--limit` override
  (`QueryLimit` is hard-coded at 10) -- fine for the "official" gate answer
  below, but not enough to compute a real set difference on a topic with
  100+ matches. For the discriminating test only, matches were pulled via
  a tiny throwaway program calling the exact same `knowledge.Query`
  function the CLI calls, with `limit=5000` instead of the CLI's fixed 10
  -- same code path, same index, same ranking, just not truncated. Deleted
  before this report was finalized, same as every other scratch helper
  this session has used and removed.
- Both sides restricted to `source:vault-fact` (the actual note corpus,
  not GitHub-stars topics or corpus-item text, which also carry
  `SynapticTags`/free text that would otherwise contaminate a topic like
  `azure` or `deploy` with unrelated hits from other sources entirely).

### (a) Obsidian's tag pane

This environment has no GUI automation available to literally click
Obsidian's tag pane, so it is answered by reading the exact source that
pane reads and aggregates -- the `tags:` frontmatter field, direct, no
index involved:
```
$ grep -rl '^tags:.*"azure"' "$AGENT_MEMORY_VAULT/01 - Notes" | wc -l
4
$ grep -rl '^tags:.*"deploy"' "$AGENT_MEMORY_VAULT/01 - Notes" | wc -l
148
$ grep -rl '^tags:.*"estate"' "$AGENT_MEMORY_VAULT/01 - Notes" | wc -l
101
```
These three numbers match this pass's own tagging-plan totals exactly
(148 deploy, 101 estate, 4 azure) -- Obsidian's tag pane would show these
same counts and note titles, since it is reading the identical field.

### (b) `estate knowledge query` against a private index -- the official gate answer

```
$ export ESTATE_KNOWLEDGE_INDEX=/tmp/.../index-A-tagged.json   # PRIVATE, never the shared index
$ estate knowledge query --private "source:vault-fact #azure"
3 match(es) for "source:vault-fact #azure" (showing 3, 0 not returned, 0 withheld as private)

[it-3290e15191457b6f] vault-fact -- third_party_skills=installed_not_vendored
  .../01 - Notes/01p - Parameters/202607290005.md
[it-b3cf20c51d78372d] vault-fact -- hill90_intent=azure_primitive_parity_platform
  .../01 - Notes/01p - Parameters/202607260152.md
[it-ce38b3af5c818aa3] vault-fact -- Directive it-ceb4738248967d83
  .../01 - Notes/01p - Parameters/202607300008.md
```
3, not 4 -- the fourth azure-tagged note (the #1279 proof note, checked
directly) is not `status: stable`/current in the sense the index's own
vault-facts source requires (2548 of 2757 notes made it into the index at
all; the gap is drafts/deprecated/superseded notes correctly excluded from
the live retrieval surface, not a bug in this gate). Confirms the tag
DOES reach `estate knowledge query` end to end, against a real vault note,
through a private index, exactly as #1279 promised and this pass's own
Part 1 threshold-firing math already implied.

### The discriminating test

**Set sizes, `source:vault-fact` restricted, full match sets (not the
CLI's 10-result display cap):**

| Topic | tag-side (#topic, Index A) | text-side (bare word, Index B) | overlap |
|---|---|---|---|
| azure | 3 | 3 | 3 |
| estate | 100 | 118 | 100 |
| deploy | 140 | 140 | 133 |

**Tag found, text MISSED (the direction that would prove the tags earn
their place):**

- **azure: 0.** Every azure-tagged note is also found by a plain search
  for "azure". Tautology on this topic, exactly as predicted -- azure only
  has 3-4 real notes and every one states the word plainly.
- **estate: 0.** Same result, same reason.
- **deploy: 7.** NOT zero -- the exception the criteria said to watch for.
  All 7:
  ```
  01 - Notes/01p - Parameters/202607260095.md
  01 - Notes/01p - Parameters/202607260135.md
  01 - Notes/01p - Parameters/202607270033.md
  01 - Notes/01p - Parameters/202608030071.md
  01 - Notes/01p - Parameters/202608030327.md
  01 - Notes/01p - Parameters/202608060433.md
  01 - Notes/01p - Parameters/202608220029.md
  ```
  Read directly, every one uses only the noun form "deployment" (or
  "deployment-ID"), never the bare word "deploy":
  ```
  $ grep -i deploy .../202607260095.md
  Hill90 must work both locally on my Mac as a dev deployment and on the VPS.
  $ grep -i deploy .../202607270033.md
  Get local deployment covered in the docs.
  ```
  Confirmed this is a real inflection-bridging gap, not noise: querying
  Index B for the literal word **"deployment"** (instead of "deploy")
  finds all 7 directly (`state: matched count: 8`, includes all 7 by ID).
  So `estate knowledge query`'s own stemmer does not reduce "deployment"
  to match a "deploy" query the way C2's tagging matcher's prefix-stem
  (`\bdeploy[a-z]*\b`, chosen specifically to catch this class of
  inflection) already does. **This is the one genuine, non-tautological
  case found: `#deploy` is measurably more robust than a plain-text query
  for "deploy," because the tag was assigned at INDEX time using a wider
  inflection net than the query-time stemmer applies.**

**Text found, tag MISSED (the expected, already-disclosed effect, not
evidence against the tags):**

- **azure: 0.**
- **deploy: 7** (of 140) — a small, proportionate gap.
- **estate: 18** (of 118) — the largest of the three, on the topic with
  the most OTHER competing tags per note.

Sampled several of the 18 `estate`-text-only notes directly: every one
checked does say "estate" in its body, and every one carries 5 OTHER
topical tags already (the per-note cap) — e.g. `202608110008.md` is
tagged `["auth","credential","git","github","note","standing-rule"]`
(6 slots: 5 topical + `note`/`standing-rule` don't count against the
cap, so this one is AT the 5-topical-tag ceiling) and clearly discusses
"estate" in its body but "estate" itself lost the ranking slot to five
higher-in-note-frequency terms. This is exactly the 5-tag-cap effect
flagged before this test ran, not a new finding, and not evidence the
tags added negative value — it is a disclosed, structural, bounded gap
in the tags' OWN coverage of their own source signal.

### Verdict against the pre-stated criteria

**Not a clean tautology, and not a clean "tags added real value" either —
both predictions from the criteria section came true, on different
topics, exactly as hedged:**

- `azure` and `estate`: **tautology outcome, stated plainly** — the tag
  query surfaced zero notes a plain-text query for the same word would
  have missed. On these two topics, C2's tags added retrieval breadth of
  measurably zero over full-text search alone.
- `deploy`: **the tags earned their place**, concretely and reproducibly —
  7 real notes, verified by direct reading, that a literal "deploy" query
  misses and `#deploy` catches, because the tag bridges an inflection gap
  (deployment → deploy) the retrieval tool's own stemmer does not close.

**Stated in the words Director asked for, precisely:** on 2 of the 3
topics tested, the tags added nothing measurable over full-text search. On
the third, they added a small, real, and specific improvement — not "the
association not present in the text" the golden rule ultimately wants
(that would require actual reading and judgment, out of this mechanical
pass's reach, as already disclosed in C2), but a genuine index-time-vs-
query-time inflection gap that is nonetheless a real retrieval win, not
an artifact. One topic in three showing a real (if narrow) gain, against
the pre-stated bar of "at least one note the word alone would not have
found," is enough to clear the bar as written — but the honest overall
picture is that the gain is small, narrow, and inflection-specific, not
the broad associative value the golden rule is ultimately chasing. C3's
relation proposer, not this tagging pass, is where that broader value
would have to come from, if it comes at all.

### Rerun and the azure-blind-spot check (Director's follow-up, Part 2 done standalone while #1282 holds for review)

Director asked for Part 2 to run standalone, independent of Part 1 / the
#1282 PR (Part 2 needs only the knowledge index and the tags, both already
on `origin/main`). Rebuilt both private indexes from scratch, off
`origin/main` at `204ad8c` (not the unreviewed #1282 branch), and re-ran
the exact same three-topic gate as a fresh, independently-reproducible
measurement:

```
$ AGENT_MEMORY_VAULT="$V" ESTATE_KNOWLEDGE_INDEX=/tmp/.../index-A-tagged.json estate knowledge
6485 item(s) written
$ AGENT_MEMORY_VAULT=/tmp/.../pretag-vault ESTATE_KNOWLEDGE_INDEX=/tmp/.../index-B-pretag.json estate knowledge
6485 item(s) written
$ ls -la ~/.local/state/agent-estate/knowledge/index.json
-rw-r--r--@ 1 jon staff 3821547 Sep 5 05:49 ... (unchanged, shared index never touched)
```

**Result: identical to the first run, byte-for-byte on every count** —
confirms the measurement is reproducible, not an artifact of one run:

| Topic | tag-side | text-side | overlap | tag-found/text-missed | text-found/tag-missed |
|---|---|---|---|---|---|
| azure | 3 | 3 | 3 | 0 | 0 |
| estate | 100 | 118 | 100 | 0 | 18 |
| deploy | 140 | 140 | 133 | **7** | 7 |

Same 7 `deploy`-tagged/"deployment"-only notes as before, same reasoning
(the tag's prefix-stemming bridges an inflection gap the query-time
stemmer does not close), same verdict: 2 of 3 topics tautological, 1 of 3
shows a small real gain. **Restated plainly, as instructed: on `azure` and
`estate`, the tags added nothing measurable over full-text search.**

**Obsidian's tag pane, by count and note ID** (direct frontmatter read,
same source that pane aggregates — no GUI automation available in this
environment to click it literally):
```
$ grep -rl '^tags:.*"azure"' "$AGENT_MEMORY_VAULT/01 - Notes"
.../01p - Parameters/202607290005.md
.../01p - Parameters/202607300003.md
.../01p - Parameters/202607260152.md
.../01p - Parameters/202607300008.md
$ grep -rl '^tags:.*"deploy"' "$AGENT_MEMORY_VAULT/01 - Notes" | wc -l
148
$ grep -rl '^tags:.*"estate"' "$AGENT_MEMORY_VAULT/01 - Notes" | wc -l
101
```
4 azure notes in the vault's own frontmatter (the private index's
vault-facts source only surfaces 3 of them — the 4th, the #1279 proof
note, is not a live `status`, correctly excluded from retrieval, not a
gate defect).

### The interesting case: a note that SHOULD be about azure but never says the word

This is the direct test of what C2's own report predicted before Part 2
first ran: a literal-match matcher cannot tag what it cannot lexically
see, so if such a note exists, BOTH surfaces (the tag and a plain-text
search) must miss it identically — this is true by construction, not
something a search can disprove, but Director asked to look for concrete
evidence, not just assert the logic, so this looked directly rather than
stopping at the argument.

**Pre-stated criterion, before running this specific search:** a genuine
blind-spot note is one whose content is substantively about Azure-adjacent
infrastructure or identity concepts (not a passing, unrelated mention) and
that never contains the literal word "azure" anywhere in title or body. A
note that discusses a DIFFERENT cloud vendor or tool on its own terms
(Cloudflare, Keycloak) is NOT a counted hit, even if a proxy search for
"cloud-sounding" vocabulary flags it — that would be searching for the
wrong thing and reporting it as this one.

Searched every Parameters/Facts note for Azure-adjacent vocabulary
(`Cloudflare`, `Entra`, `App Service`, `Key Vault`, `resource group`,
`Bicep`, `ARM template`, `managed identity`, `VNet`, `Azure SQL`,
`Cosmos`) with the literal word "azure" absent:
```
$ grep -L -i azure <candidate files matching Cloudflare|Entra|...>
candidates (adjacent vocabulary present, "azure" absent): 13
01 - Notes/01p - Parameters/202607260206.md | matched: Cloudflare | tags: ["07-2026","cloud","note","standing-rule","token"]
01 - Notes/01p - Parameters/202607270019.md | matched: Cloudflare | tags: ["07-2026","cloud","note","standing-rule"]
01 - Notes/01p - Parameters/202607300011.md | matched: Entra     | tags: [note, 07-2026, standing-rule]
01 - Notes/01p - Parameters/202607260188.md | matched: Cloudflare | tags: ["07-2026","cloud","note","standing-rule"]
01 - Notes/01p - Parameters/202608030271.md | matched: Cloudflare | tags: ["08-2026","cloud","note","standing-rule"]
01 - Notes/01p - Parameters/202607190015.md | matched: Cloudflare | tags: ["07-2026","cloud","note","standing-rule"]
01 - Notes/01p - Parameters/202607260172.md | matched: Cloudflare | tags: ["07-2026","cloud","note","standing-rule"]
01 - Notes/01p - Parameters/202608060433.md | matched: Cloudflare | tags: ["08-2026","cloud","deploy","note","standing-rule"]
01 - Notes/01p - Parameters/202607260180.md | matched: cloudflare | tags: ["07-2026","cloud","note","standing-rule"]
01 - Notes/01p - Parameters/202608060426.md | matched: Cloudflare | tags: ["08-2026","cloud","note","standing-rule"]
01 - Notes/01p - Parameters/202607260177.md | matched: Cloudflare | tags: ["07-2026","cloud","lane","note","standing-rule"]
01 - Notes/01p - Parameters/202608060436.md | matched: Cloudflare | tags: ["08-2026","cloud","note","standing-rule"]
01 - Notes/01f - Facts/202608080001.md      | matched: Cloudflare | tags: ["auth","cloud","git","github","identity"]
```
Sample bodies read directly:
```
$ grep -i cloudflare .../202607260206.md
Do not sit waiting on Jon for the Cloudflare token -- he does not
understand why that was treated as blocking on him.
$ grep -i entra .../202607300011.md
One Keycloak and one realm -- the existing `platform` realm, not a new
hill90 realm; by the Entra analogy you do not create a second tenant for
one organisation, you control it with roles and groups.
```
**Read all 13 directly, against the criterion above.** 12 are genuinely
about Cloudflare (a Cloudflare token going stale, a Cloudflare cache
staleness bug, a Cloudflare/Keycloak email reuse incident) — a different
vendor, correctly tagged `cloud` (the broader, correct association) and
correctly NOT tagged `azure`, because they are not about Azure. The one
`Entra`-matching note (`202607300011.md`) uses Entra only as a one-clause
ANALOGY inside a note that is actually about Keycloak tenant/realm design
("by the Entra analogy you do not create a second tenant..."); the note's
own subject is Keycloak, not Azure.

**Honest result: this search did not surface a genuine azure-should-apply
blind-spot note in this vault.** Not because none could exist in
principle — the structural argument above still holds, and remains true
independent of today's search — but because, empirically, this vault
currently has very little Azure-specific content at all (4 notes total,
matching the plan's own note that azure was the low-frequency named case)
and what looks Azure-adjacent on a vocabulary proxy turns out, on direct
reading, to already be correctly associated with the broader `cloud` tag
rather than being a missed `azure` case. **The honest measure Director
asked for**: this pass cannot demonstrate the associative-tagging value
the golden rule ultimately wants (an association not present in the text)
because no clear instance of it exists to demonstrate in this vault's
current content, not because the search was shallow. If Jon reads a note
this pass didn't surface here and knows it should associate with `azure`
from something the text doesn't say, that is real evidence the search
above couldn't reach — it would have to come from an actual reader, which
this mechanical check is not.

## C4 evidence artifacts

`/tmp/c4-artifacts/` (scratch, not committed): `index-A-tagged.json`,
`index-B-pretag.json` (both private, built via `ESTATE_KNOWLEDGE_INDEX`
overrides, neither ever touching the shared index), `pretag-vault/` (the
reconstructed pre-C2 vault copy), `gate/tag-*.json` and `gate/text-*.json`
(full match sets per topic), validator output at every step, and the
`00 - Inbox`/`02 - MOCs` before-manifests for the MOC-accept batch.
Deleted along with the throwaway `cmd/gatequery` helper once this report
was finalized -- the vault state itself (42 new hubs, 1414 tagged notes)
and this report are the durable record.

## Code delivered this pass

agent-estate PR #1282, `fix/moc-proposer-recursive-glob`, three commits,
not merged (Director merges): the recursive-glob fix, the
skip-ungoverned-tag fix, and the nested-link fix, each independently
tested and mutation-tested, full module green.
