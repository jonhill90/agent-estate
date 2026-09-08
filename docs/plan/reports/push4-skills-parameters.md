# Push 4 input — skills-repo reconciliation corpus sweep

Foundation document for Astra's 05:45 brief. Compiled against
`~/corpus/corpus.sqlite3` (renamed 2026-09-06; `ledger.sqlite3` compat
symlink resolves the same file), read-only (`sqlite3 -readonly`), never
writing to it. Query base: `items where weight='hard'` (never
`live_parameters`, which hides hard directives — agent-estate#1020) —
2638 hard items confirmed (1341 directive, 958 parameter, 173 correction,
138 question, 28 thought), matching the measured surface. 96 items match
`body like '%skill%'` on a crude match; that number was treated as a
floor, not the answer — the sweep below is semantic, built from a ~187-item
candidate pool (broad keyword net: skill, npx, plugin, harness-agnostic,
peter, eval, roster, discover, recommend, employer, marketplace, scoping,
install, upload, portable, credential) then individually read and judged
for actual relevance to skills-repo reconciliation. Several dozen
candidates were excluded as false positives (an unrelated `skills.ts`
source file in a different project, unrelated credential/security items
that only matched on `install`/`credential`).

Every quote below is `items.body` — this corpus's own already-distilled
statement of what was decided, never `prompts.text_raw`. Where a
supporting prompt quote was useful for dating or context, it is drawn
from `prompts.text_clean` only; none was needed verbatim in the entries
below beyond `body` itself, so no raw operator text appears anywhere in
this file.

**Compile-gap fix, Director review of `run/astra-push4-plan.md` (post the
first pass):** the plan cited four item ids not yet in this document —
`it-c10defd3a1e55022`, `it-ebe6f2eef9425eee`, `it-377e6dbc6abcd917`, and
`it-f61bb8e506b054b9` — needed as B2/B3/B4's retrievable basis. Queried
directly (`select id, kind, body from items where id like 'it-<prefix>%'`
— this table has no `text_clean` column; `body` is already clean prose in
all four cases, no additional cleaning was needed). **All four carry
`weight='preference'`, not `weight='hard'`** — outside this document's
own stated query base (`items where weight='hard'`, agent-estate#1020).
They are added below on the Director's explicit instruction, verified as
real corpus rows, and marked `weight: preference` inline at each entry so
a reader never mistakes them for the hard-item population the rest of
this document draws from. This also means the original semantic sweep
(keyword net over `weight='hard'` only) would **never surface a
preference-weight item**, by construction — checking whether any other
plan-cited id is missing (below) covers only ids the plan actually names,
not an exhaustive scan of `weight='preference'` rows for undisclosed
reliance, which is a real, stated limit of this document, not a claim of
total coverage.

Ids are given in full, 16-hex-character canonical form throughout (e.g.
`it-ea42cf0d6f8a53b7`) — never truncated — specifically so an exact-string
grep against this file cannot silently miss an entry the way a shortened
7-8 char prefix could collide or fail to match.

---

## 1. One home per skill — no duplication across repos

**Kind: PARAMETER.** `it-ea42cf0d6f8a53b7` (2026-07-29, acknowledged):
"I do not want a copy of every skill in every repo — that is dumb and
defeats the purpose." (`skill_duplication=forbidden`)

Supporting: `it-957502d135216f90` (2026-07-29, acknowledged, PARAMETER):
"Skills Jon installs from elsewhere — Gentiva, Azure, Microsoft, Matt
Pocock — must not end up committed in his own repo; the repo holds what
he authored, installed skills stay installed environment."
(`third_party_skills=installed_not_vendored`)

**Implication:** a skill has exactly one canonical repository home. A
skill authored by Jon (or an agent, see §8) lives in `skills` or
`skills-private`, never both, never copied between them, never vendored
from an upstream source into either. Anything pulled in from elsewhere
stays in its installed-environment form, not committed as a duplicate
copy. This is the load-bearing rule behind the whole reconciliation task
— "one home per skill" isn't a nice-to-have, it's explicitly named as
"the purpose" that duplication defeats.

---

## 2. The scoping axis

**Kind: PARAMETER.** `it-0a55635e229e1bfb` (open — never marked acted):
"Skills need a scoping axis: some public and shared across work and
personal, some project-scoped, some work-only, some personal-only."
(`skill_scope=public|project|work|personal`)

Supporting: `it-e547f3f6e6f2a386` (2026-07-29, acted, PARAMETER):
"agent-dotfiles must support both private and public skills, because it
runs on every machine."

**Implication:** the four-way axis (public / project / work / personal)
is a stated need, but **its own item's status is `open`** — it was never
marked `acted`. Astra should treat the axis itself as a real requirement
to design against, not as an already-implemented mechanism to verify.
`skills-private` (the private repo) and `skills` (the public repo) are
the two extremes of this axis that DID ship (§1, §10); project- and
work-scoping within those may still be open design, not built.

---

## 3. Progressive acquisition — on-demand install + discovery, not vendoring

**Kind: PARAMETER** (two corpus items, same underlying policy, not in
conflict despite different `resolved_to` keys — read together below).

`it-33e6ad272a1eccb8` (2026-07-30, acted): "Skills should be acquired
progressively — installed as the job needs them, not all up front."
(`skill_acquisition=progressive_on_demand`). The supporting prompt for
this item shows the actual mechanism in use:
```
npx skills add microsoft/azure-skills \
  --skill azure-prepare --skill azure-validate --skill azure-deploy \
  --agent '*' -y
```
— confirming "progressive npx install" as a real, exercised pattern
(the theme named in the brief), not aspirational.

`it-75b1f4af4cc2f19b` (2026-07-29, acknowledged): "Copying skills out of
official upstream repos into his own is dumb; discovery-by-recommendation
is the mechanism he wants." (`skill_acquisition=progressive_discovery`)

**Added — compile-gap fix, Director review of `run/astra-push4-plan.md`
(B2's cited install-mechanism basis, missing from the first pass):**
`it-c10defd3a1e55022` (PARAMETER, weight `preference` — not `hard`, see
the compile-gap note near the top of this document, acted): "Skills should be installed through a
tool like the npx skills command rather than being permanently baked into
the config." (`skill_install_method=npx_skills_cli`) — this is the direct
corpus basis for the `npx skills add ...` mechanism this section already
showed as a supporting prompt quote; the two now corroborate each other
rather than one standing alone on prompt-context evidence only.

**Implication:** these are two facets of one policy, not a contradiction
— flagging this explicitly because the two items carry different
`resolved_to` keys and could be misread as competing. "Progressive" means
both (a) install mechanically on-demand via `npx skills add ...`, not
pre-provisioning a whole set, and (b) discover candidates by
recommendation ("this came up three times, want a skill?") rather than
by copying an upstream repo's skill wholesale into his own. Astra should
design the skills-repo reconciliation around both halves together.

---

## 4. No plugins — but a named standard needs checking, not dismissing

**Kind: PARAMETER**, with a later **CORRECTION** that narrows scope,
not reverses it.

`it-bca91f79e5e9f184` (2026-07-29, acted): "We are not using any of the
plugins — everything runs off our own core skills." (`plugins=off`)

Supporting directives (2026-07-29/07-30, both DIRECTIVE, acted):
`it-dcf9a10a871cebff` — "Drop the ralph-loop and frontend-design
plugins, then fix measure_e15." `it-882f83ed208555fd` — "Jon uninstalled
the ralph loop plugin himself; that old noise is not wanted anymore."

**Later correction, 2026-08-11** (13 days after the above):
`it-059c6f16d69d9b1b` (CORRECTION, acted): "Agent Plugins is a new
standard published last week — go search for it instead of assuming it
does not exist."

**Implication — flagging the relationship explicitly per the brief's own
rule:** this is not a reversal of `plugins=off`. The July decision was
about the *harness-native plugin mechanism* (Claude Code marketplace
plugins like ralph-loop and frontend-design) being unwanted noise,
redundant with the skills the estate already builds itself. The August
correction is about a *different, newly-published named standard* called
"Agent Plugins" — the instruction is narrowly "don't assume it doesn't
exist, go check it," not "plugins are back on." Astra should verify
whether "Agent Plugins" (the spec) is relevant to the skills-repo
reconciliation before either adopting or dismissing it — the corpus
contains no later item resolving that check one way or the other;
**implication unclear** beyond "investigate before assuming irrelevant."

---

## 5. Harness-agnostic and web-uploadable

**Kind: PARAMETER.** `it-1fd291f8b912ffcd` (2026-07-?, acted): "Two
harnesses must be supported: Claude Code on the Mac, and Claude Code /
Claude Chat in the web UI, where skills are uploaded rather than read
from disk."

**Added — compile-gap fix, Director review (B3's cited basis, missing
from the first pass):** `it-ebe6f2eef9425eee` (PARAMETER, weight
`preference` — not `hard`, see the compile-gap note near the top of this document, acknowledged): "Skills do not
have to be CLI-first; they need to be harness-agnostic and work everywhere
including web harnesses like Claude Desktop, Codex and Copilot, and Pi has
an MCP server extension it can use." (`harness_targets=agnostic_including_
web`) — this is the broader, named statement of the requirement
`it-1fd291f8b912ffcd` gives as a two-harness instance; read together, the
rule is not "these two harnesses specifically" but "harness-agnostic,
including whatever web/MCP surface a given harness offers."

**Implication:** the skills repo's own portability requirement is not
abstract — it names a concrete second consumption mode (web upload, not
filesystem read) that a skill's own packaging must survive. Anything in
the reconciliation that assumes filesystem-only consumption (a symlink
farm, a path-relative reference) breaks this. This matches the public
`skills` repo's own README framing ("self-contained, model- and
harness-agnostic instructions... nothing here depends on private
tooling") already observed live in this run's own P1.4/P7 reviews.

---

## 6. Peter-shareable

**Kind: PARAMETER.** `it-9cc619d4bac7ca0d` (acted): "Peter may be given
work, so skills need to be shareable with him rather than personal-only."

**Implication:** at least one skill scope (§2's axis) must be shareable
with a second human collaborator, not just Jon. This is a concrete
instance of the "work" or "project" scope tier in §2's axis — Astra
should treat this as evidence the axis is not purely theoretical, since
it names a real person and a real need it must satisfy.

---

## 7. Evals — or a named gap

**Kind: PARAMETER** (where the content belongs) **+ DIRECTIVE** (that
it must happen).

`it-81eb7e9195e97b3c` (2026-08-?, acted, PARAMETER): "Content belongs in
the repo it is about — provenance-manifest and behavioral-evaluation
material must not sit in the skills repo, it goes in the evals repo."
(`eval_content_home=agent-evals`)

`it-350baeef5452a92c` (acknowledged, DIRECTIVE): "We still need to
evaluate our skills across harnesses and models; behavioural evals need a
lot of work and we should look at what others already do to make it
easier."

`it-cfd99c6670df0ae4` (acknowledged, DIRECTIVE): "At some point put all
the skills through the eval system we built, to harden them and prove
they behave consistently across models and harnesses."

**Added — compile-gap fix, Director review (B4's cited basis, missing
from the first pass, and this is the actual named rule the two directives
above only gesture toward):** `it-377e6dbc6abcd917` (PARAMETER, weight
`preference` — not `hard`, see the compile-gap note near the top of this document, acknowledged): "A published
skill is expected to carry evals; shipping one without them has to be
called out rather than glossed over." (`skill_release=evals_expected`)

**Implication:** the `agent-evals` repo split (already known from this
run's own in-scope-repo list) is not incidental — it's a stated
separation-of-concerns rule (§7's parameter), and the evals-over-every-
skill work is a real, acknowledged, **not-yet-closed** directive (both
supporting items are `acknowledged`, never `acted`/`resolved`). The newly
added item names the actual release gate directly: a skill without evals
is not silently acceptable — its absence must be stated, not omitted.
Astra should report the eval gap as a named, disclosed absence per
skill in the roster, not assume it is done because the repo split
happened.

---

## 8. Evidence-based roster — agent-authored is fine, same bar, capability first

**Kind: CORRECTION** (the operative one) **+ THOUGHT** (the rule it
produced) **+ PARAMETER** (the selection criterion). This cluster has a
real, resolved tension — reported explicitly per the brief's rule.

**2026-07-26**, `it-377c417f95c77338` (CORRECTION, acted): "The skill was
created during the evals without Jon asking for it — unrequested
artefacts appearing is itself the concern."

**2026-07-28, two days later**, `it-5bebe4a4e2bde3e5` (CORRECTION,
acted): "You are wrong that skills only get created when I ask — you
authored safe-deletion, dispatching-subagents, failing-test-first and
memory-conventions, not me." Same day, `it-7d2a37ef5302518c` (THOUGHT,
acted): "Agent-authored skills must clear the same acceptance bar as
Jon-authored ones — no shipping on credit." (`skill_bar=same_for_agent_
and_jon_authored`)

**Reconciliation, not a standing contradiction:** the 07-26 item is
narrower than it first reads — it flagged a *specific* surprise artefact
appearing mid-evals as a process concern (unexpected side effects during
an eval run), not a blanket rule against agent-initiated skill creation.
Two days later, Jon explicitly corrected the opposite misreading (an
assistant claiming skills "only get created when asked") and the
resulting THOUGHT states the actual standing rule: agent-authored skill
creation is normal and expected, gated by evidence (a recurring gap
noticed, not a request), but held to the identical acceptance bar as
anything Jon authors himself.

Supporting selection criterion, `it-ac90582e53f6ab2c` (2026-08-?,
acknowledged, CORRECTION): "Token count alone is not a sufficient reason
to reject a candidate skill." (`skill_selection=capability_first_tokens_
are_tiebreaker`)

**Added — compile-gap fix, Director review (B4's cited measured-roster
basis, missing from the first pass):** `it-f61bb8e506b054b9` (PARAMETER,
weight `preference` — not `hard`, see the compile-gap note near the top of this document, acted): "Skill roster
decisions should follow the measured evidence rather than anyone's
intuition, including Jon's." (`skill_roster_decisions=evidence_over_
intuition`) — this generalizes the token-count criterion above: roster
decisions as a whole (not just the reject case) are bound to measurement,
explicitly including Jon's own intuition as something the evidence can
override.

**Implication:** the skills-repo reconciliation should not require a
Jon-originated request as a precondition for a skill's existence — the
gate is evidence of a real, recurring need plus meeting the same
acceptance bar, with capability (not token cost) as the primary selection
axis and token cost only a tiebreaker between otherwise-equal candidates.
Roster decisions generally — not only rejections — are bound to measured
evidence over anyone's intuition, Jon's included; Astra's own roster
recommendations should cite measurement, not judgment calls presented as
self-evident.

---

## 9. Discovery-by-recommendation — mine, never wholesale-copy

**Kind: DIRECTIVE** (recurring pattern, six corpus items, same
direction, no conflict).

`it-5be8e30cebe58171`: "Mine microsoft/hve-core for logic-gems worth
adopting into the agent-dotfiles and skills repos." `it-8b64e2531657c0eb`:
research ScottRBK's harness-of-harnesses work and "find out who else has
similar or better work." `it-db169cf08a44bb69`: "Pull and review
addyosmani/agent-skills, ruvnet/ruflo, PrimeIntellect-ai/prime-agent and
paperclipai/paperclip for logic gems." `it-e06204afc670b76c`: "Mine
https://github.com/coleam00/skills for logic-gems." `it-866d4516c215c27c`:
review an old personal PRP framework "because it was noisy but had good
logic in it we never went back to review." `it-9019455b5bdda93b`: mine a
stale personal research repo "worth mining for ideas."

**Implication:** this is the same policy as §3/§9's "discovery-by-
recommendation," made concrete — external skill collections (public
repos, other people's work, Jon's own stale research) are a standing
input source, mined for ideas and logic, never pulled in wholesale (§1's
no-duplication rule still governs the output). Astra should read the
reconciliation as an ongoing external-scan practice, not a closed set of
skills to sort once.

---

## 10. Employer content stays out

**Kind: PARAMETER** (two items, same direction, one names the concrete
case).

`it-850afa9e43e114bf` (acted): "Employer-specific (Gentiva) skill content
must never live in agent-dotfiles; work-only skills belong in a separate
private overlay repo installed alongside it."
(`employer_content=excluded_from_agent_dotfiles`)

`it-9e769f3dafa0899e` (acknowledged): "We do not talk about work here —
this repo and this conversation stay clear of employer content; the
design is about public versus private skills generally."
(`work_content=off_limits_here`)

**Implication:** employer content is excluded even from the *design
conversation* about public/private skills, not just from the shipped
repos — a stricter boundary than "keep it in skills-private." Whatever
Astra produces from this sweep should not name or reference specific
employer content even in passing, consistent with this file's own
restriction against pasting raw prompt text.

---

## Beyond the twelve named themes

### 11. Repo naming — no compromise

**Kind: PARAMETER.** `it-d12dcba4279b3f1c` (acted): "The skills
repositories are to be named `skills` and `skills-private` — no name
compromise; work out the GitHub redirect problem yourself."
(`skills_repos=skills+skills-private`)

Note: `it-81a800246f94619f` (acted) records the earlier decision to
split into two repos at all (`skills_repos=skills_public+skills_private_
private` — an inconsistent `resolved_to` value, likely a stray
duplicated word in the extraction, not a second naming scheme; the
canonical names are the ones in `it-d12dcba4279b3f1c`). **Implication:**
the two-repo split and its exact names are both settled, non-negotiable
inputs — not open questions for the reconciliation.

### 12. Skill changes need a real reason

**Kind: PARAMETER.** `it-dd314fd532837b1f` (acted): "Changes to skills
need a justification tied to fixing the system; cosmetic churn is not
allowed." (`skill_changes=justified_only`)

**Implication:** any reconciliation-driven edit to an existing skill
(not just the repo-level moves) needs a stated reason tied to a real
defect or gap — Astra's brief should carry justifications per changed
skill, not a bare diff.

### 13. Spec conformance

**Kind: PARAMETER.** `it-fce82160a7c14b25` (acknowledged): "Skills
should follow the specification at https://agentskills.io/specification."
(`skill_spec=agentskills.io/specification`)

**Implication:** this is the external conformance target already visible
in the live `skills` repo's own README and this run's own P1.4 review of
Skills#302 — worth naming explicitly here since it's the one external
standard actually adopted (contrast with §4's "Agent Plugins" standard,
which is unresolved/unadopted).

### 14. Harness-native syntax stays out of skills

**Kind: PARAMETER**, inferred from a comparative research finding, not a
direct instruction — flagged as lower-confidence than the others above.
`it-957502d135216f90`'s own supporting prompt context (not its `body`,
which is the third-party-vendoring rule already used in §1) states "zero
harness-native syntax in our skills" as a verified, current fact
distinguishing this repo's own skills from a comparison set
(`team-skills`) that embeds harness-specific injection syntax directly.

**Implication:** harness-agnosticism (§5) extends to the skill's own
authoring style, not just its distribution mechanism — no `!`-injection,
argument-hints, or other harness-specific syntax inside a `SKILL.md`
itself. **Implication for enforcement unclear** — no corpus item states
this as a rule to check going forward, only as an observed-true fact at
the time; Astra should treat it as a design constraint worth stating
explicitly rather than assuming it's self-enforcing.

---

## Summary for Astra

- **Settled, non-negotiable:** one home per skill (§1), the two-repo
  split and exact names (§11), no plugins as harness-native mechanism
  (§4), employer content excluded even from design talk (§10), spec
  conformance to agentskills.io (§13).
- **Real, open gaps — not done, do not assume closed:** the full
  public/project/work/personal scoping axis (§2, item status `open`);
  cross-harness/cross-model eval coverage for every skill (§7, both
  supporting items `acknowledged`, never closed); whether the "Agent
  Plugins" named standard is relevant (§4, no resolving item exists).
- **One resolved tension worth restating up front, since it reads as a
  contradiction out of context:** agent-authored skill creation is
  correctly gated by evidence, not by Jon asking first (§8) — an earlier
  item that reads like the opposite was about a narrower process concern
  (a surprise artefact during an eval run), not a standing rule against
  agent authorship.

**Item count: 35 distinct corpus items cited** (verified by extracting
every full-form `it-` id actually appearing in this file, de-duplicating,
and re-querying the database for both kind and weight — not an estimate;
counts below are query output, not hand-counted), across 14 themed
entries: 10 directly matching the brief's named themes (one theme, "no
plugins," folds the "Agent Plugins" standard nuance into itself rather
than a separate 11th entry), 4 found beyond that list. Kind breakdown:
20 PARAMETER, 10 DIRECTIVE, 4 CORRECTION, 1 THOUGHT. Weight breakdown:
**31 `hard`, 4 `preference`** — the 4 preference-weight items are the
ones added in the compile-gap fix (Director review of
`run/astra-push4-plan.md`; see this document's own note near the top),
each marked `weight: preference` inline at its own entry so this is
never silently conflated with the hard-item population the rest of the
document draws from.

**Third check, per the Director's request:** every distinct item-id
prefix appearing in `run/astra-push4-plan.md` was extracted and checked
against this document — 11 distinct prefixes cited, 7 already present
before this fix, 4 were the compile gap just closed. Zero additional
missing citations found. This check covers only ids the plan actually
names, not an exhaustive re-scan of `weight='preference'` rows generally
for undisclosed reliance — stated as a real limit above, not implied
total coverage.
