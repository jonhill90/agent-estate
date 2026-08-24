---
type: Decision
description: agent-supervisor and agent-tui merge into one repo, direction matches the measured dependency, sequenced with the go-public flip -- not before it.
generated:
  at: 2026-08-23T18:00:00-04:00
---

# 0006 — agent-tui merges into agent-supervisor, in one step with going public

`2026-08-23`. Decided by the director loop, not asked to Jon as a question
— he stated the intent (`estate-loop/director-one-repo.md`) and invited the
opposite case rather than agreement. This document is that case, answered,
plus a hardened restatement of the intent and a new measurement that arrived
mid-decision (`estate-loop/director-one-repo-addendum.md`), both weighed
below rather than reopening the question from scratch.

## Decision

**Merge.** `agent-tui` folds into `agent-supervisor`, direction matching the
measured dependency (below) — `agent-tui`'s code moves into
`agent-supervisor` as a subtree (e.g. `tui/`), history preserved via a
history-preserving import (`git subtree add` or `git filter-repo`, not a
squashed copy), not the reverse and not a fresh third repo built from
scratch. Rename to `agent-estate` is a separate, optional, lower-cost
follow-on — it does not gate this decision either way (see "What a rename
buys, separately" below).

**This merge is inseparable from a publish.** `agent-supervisor` is public
today (`gh repo view jonhill90/agent-supervisor --json visibility` →
`PUBLIC`, measured 2026-08-23); `agent-tui` is private
(`PRIVATE`, same command). Moving `agent-tui`'s source into `agent-supervisor`
publishes it, full stop — there is no version of this merge that doesn't.
That is not a side effect to let happen quietly; it is the actual content of
the decision Jon reserved in `agent-tui#117` ("Publishing is the owner's
call and must not happen as a side effect of a cleanup PR"). **This document
does not execute the merge.** It settles the direction and the sequencing;
the merge PR itself must carry an explicit, named line — "this publishes
agent-tui's source" — that Jon signs off on before it lands, not a rename
commit that happens to have that effect.

## Why the split's strongest case doesn't survive the hardened intent

The council's split-seat (below) built a real case: visibility divergence,
the N-repo dispatch pattern, disjoint toolchains, asymmetric blast radius.
Two addendum facts arrived after that case was built and neither one was
available to it — weighed here rather than left standing unanswered.

**"Neither component has an external consumer."** True, and it changes what
the visibility argument is actually for. `agent-supervisor` has zero forks,
zero stars, zero non-Jon issue or PR authors — measured directly (`gh api`
via the split-seat's own dispatch, `forkCount=0 stargazerCount=0`,
corroborated independently below). Going public was never about serving
outside consumers of either repo; it's Jon's own stated value — "I dont
mind showing my work" (`CLAUDE.local.md`) — not a product-market argument.
That survives the merge fine: a merged repo can be shown just as easily as
two. What does NOT survive is the split-seat's use of "different
audiences" as a reason to keep them apart — there is no second audience.
The visibility question turns out to be about *when* to show the work, not
*whether the two repos need different visibility postures forever*.

**The asymmetric dependency count** (addendum, verified independently
below: 59/255 of `agent-tui`'s tracked `.go` files, `origin/main`,
mention `agent-supervisor`; 2/30 of `agent-supervisor`'s `.go` files
mention `agent-tui`) is not evidence of "two peers sharing one system,"
which is what the merge-seat argued from PR cross-references and
mirrored constants. It is evidence of a client and a server: `agent-tui`
is, structurally, a viewer built to read `agent-supervisor`'s ledger and
lane state (its own README: "reads agent-supervisor's lane and session
state over MCP") — it does not run, and provides no value, without a
live `agent-supervisor` checkout or daemon nearby. `agent-supervisor` runs
fully headless; it ships **four other lane viewers of its own**
(`scripts/supervisor/laneview/{text,tui,opensessions,dock}.sh`, per this
repo's own `CLAUDE.md`) and does not need `agent-tui` for anything. That
asymmetry is the finding the addendum asked to be read plainly rather than
smoothed into "they're two halves of one thing": they are not two halves —
one is the system, the other is a (better, more ambitious) rendering of it.

Confirmed independently, not taken on the council's word: `git grep -l -i
agent-supervisor origin/main -- '*.go'` inside `agent-tui` returns 59 files;
`git grep -l -i agent-tui HEAD -- '*.go'` inside `agent-supervisor` returns
2 (`daemon/cmd/supervisord/main.go`, `daemon/internal/sendmsg/sendmsg.go`,
both comments about matching `agent-tui`'s resume convention, not code that
calls into it). Zero of the 59 are a literal Go import
(`git grep -n '"github.com/jonhill90/agent-supervisor' origin/main -- '*.go'`
→ 0 hits) — every one is a string literal (CLI help text, test fixtures,
default-repo labels), a comment, or a call to `exec.Command` against
`agent-supervisor`'s scripts/CLI. That distinction matters for what a merge
actually fixes (see below) but not for the shape of the dependency: it is
real, it is load-bearing for `agent-tui`, and it runs one way.

The split-seat's remaining arguments — disjoint CI toolchains, blast-radius
asymmetry, the N-repo dispatch pattern — are true and are named in "What a
merge does not fix" below as real, unresolved costs of merging. None of them
answers "does either side have an outside consumer," which was the specific
challenge put to this case, so none of them is grounds to keep the repos
apart on their own.

## Why this doesn't repeat PR #135's rename

`agent-tui`'s module path was renamed today (PR #135,
`github.com/jonhill90/keelson` → `github.com/jonhill90/agent-tui`,
merged 2026-08-23T17:43:05Z) specifically because a public module path can
only be safely renamed once — "renaming after the flip would break any
consumer import," in that PR's own words. A merge renames the module path
again. That is only free because the repo remains private today and has
never had a real consumer of that path — confirmed above, zero forks/stars/
external contributors, and a private repo cannot have been `go get`-able by
anyone outside regardless. **The path spent by PR #135 has had zero real
consumers for its entire life so far.** Doing the merge-and-publish now,
before that path is ever advertised externally, costs nothing beyond what
PR #135 already spent. Doing it later — after `agent-tui` goes public on
its own, accumulates even one real external `go get`, and *then* merges —
recreates exactly the doubled-rename cost PR #135 was written to avoid, this
time for real. If the merge is going to happen at all, this is the cheapest
point in this repo's history it will ever be at.

## Sequencing — the part most likely to be got wrong

1. **Jon signs off on the merge-equals-publish framing explicitly**, as its
   own named decision, not implied by a restructuring PR. This is the one
   step this document does not take on its own — everything else here is
   the director's call to make, per the brief; this one is Jon's, per
   `agent-tui#117`'s own standing rule, and per the global guardrail that
   outward-facing, hard-to-reverse actions get confirmed even when
   authorized to proceed on everything else.
2. **The history scan required by `agent-tui#117` is already done** —
   closed 2026-08-23, completed, scanned full history for credentials/
   absolute paths/personal data, found nothing to stop on. That gate is
   clear; it does not need repeating before the merge.
3. **Merge via history-preserving import**, `agent-tui` → `agent-supervisor:
   tui/`, one step, module path renamed once as part of the same PR (not
   staged — matching Jon's own stated preference for one-pass renames,
   PR #135's own approach: "Full rename in one PR, not staged — his
   preference").
4. **Rename to `agent-estate` is separate and optional**, sequenced after
   or alongside the merge PR, not a precondition for it (see below).

No date is named for step 1 in this document, on purpose — a target date
is Jon's to set, not the director's to invent. What this document commits
to instead: the merge should not be scheduled or attempted without step 1
having happened first, and step 1 should not be left indefinite — the
devils-advocate pass below flags exactly this failure mode (a sequencing
rule with no trigger is indistinguishable from permanent deferral) and it
applies here as much as it did to the "keep split" framing it was raised
against.

## What a merge does not fix — named, not glossed over

- **CI does not get simpler, it gets co-located.** `agent-supervisor`'s
  suite (801 Python tests + 89 bash integration suites, custom 5-way shard
  planner) and `agent-tui`'s (`go build && go vet && go test ./...`, one
  job) are different tools for different languages; a merge puts both
  workflow files in one repo, it does not unify them into one pipeline.
  The one thing it does remove: `agent-tui`'s CI currently checks out
  `agent-supervisor` as a *second, separate* repo via `actions/checkout`
  just to run one cross-check test
  (`TestAllStatesCoversLanesShStates`) — that becomes a same-repo file
  read after the merge, which is a real, if small, simplification.
- **The issue/PR numbering ambiguity gets fully fixed, not partially.**
  `agent-tui/internal/reposcan` — 199 lines plus a maintained manifest —
  exists solely to catch bare `#N` references resolving to the wrong
  repo's issue tracker, and has already caught three real instances of
  exactly that bug. One shared numbering space after the merge removes the
  entire failure class `reposcan` exists to guard against — this is the
  cleanest, most complete win a merge buys.
- **The hand-mirrored constants (`SOURCE_URL_RE`, `DEFAULT_REPOSITORIES`,
  `AllStates`/`lanes.sh`'s `state=` literals, `quotaLineRE`) do not
  automatically become one source of truth.** A merge makes it *possible*
  to import the real Python/Go values directly instead of hand-copying
  them, but doing so is follow-on engineering work this document does not
  scope or schedule — without it, the same five-plus mirrored constants
  keep drifting inside one repo instead of across two.
- **Docs do not merge themselves.** `agent-supervisor/docs/product/{PRD,
  SPEC}.md` and `agent-tui/docs/{PRD,SPEC}.md` describe two different
  products today; a merge puts both directories in one tree, it does not
  reconcile their content into one narrative. `agent-tui`'s freshly-piloted
  `docs/index.md` (OKF-style, `agent-tui#136`, 2026-08-23) and this repo's
  new one (below) would need combining by hand.
- **The blast-radius asymmetry the split-seat named is real and does not
  reverse.** Today, nothing in `agent-tui`'s CI can break
  `agent-supervisor`'s required checks. Folding `agent-tui` in as `tui/`
  and wiring its CI as a required check on the same PRs would, at minimum,
  put a Go regression on the path to blocking a Python/bash change that
  has nothing to do with the TUI, unless the merge is deliberately scoped
  to keep the CI jobs independent (separate required-status entries, not
  one combined gate). Naming this so it is a decision made in the merge
  PR, not an accident of copying `.github/workflows/ci.yml` in unchanged.

## What a rename to `agent-estate` buys, separately

Nothing this decision depends on. The product identity work is already
done and already converged on "the Estate" independent of the repo
question — `agent-tui` PR #135 today: "the product is the Estate, binary
`estate`," `keelson` and `steading` both retired as prior names, and the
lane-trailer convention (`Author-Lane: estate:N`) already uses `estate` as
the umbrella name for the shared dispatch system, not either repo's name
individually. Renaming the merged repo to `agent-estate` would match that
convention but is not required for the merge to be coherent — the merged
repo could stay `agent-supervisor` (the name with the actual users: Jon,
directly, today) without contradiction. Treat the repo rename as Jon's
naming call to make whenever he wants it, on the same "his to name, not
ours to assume" basis this repo's own `AGENTS.md` already applies to
product naming.

## Evidence

- `gh repo view jonhill90/agent-supervisor --json visibility` → `PUBLIC`;
  same for `jonhill90/agent-tui` → `PRIVATE`. Measured 2026-08-23.
- `agent-tui#117` ("Prepare agent-tui to go public"), closed 2026-08-23,
  `stateReason: COMPLETED`. Its own body: "Out of scope for this issue: Do
  not flip visibility... must not happen as a side effect of a cleanup
  PR." No successor issue exists naming an owner or date for the flip
  itself — checked via `gh issue list --repo jonhill90/agent-tui --search
  "public visibility"`, one hit, `#117` itself.
- `agent-tui` PR #135 (`rename(keelson): the product is the Estate, binary
  estate, module path agent-tui`), merged 2026-08-23T17:43:05Z. Module:
  `github.com/jonhill90/keelson` → `github.com/jonhill90/agent-tui`.
  Binary: `cmd/keelson` → `cmd/estate`. Local `agent-tui` checkout was
  found ~15-19 commits stale against `origin/main` mid-investigation
  (dirty working tree, uncommitted local edit) — all claims in this
  document about `agent-tui`'s current state are against `origin/main`,
  fetched and read via `git show`, not the stale local working tree.
- Dependency asymmetry: `git grep -l -i agent-supervisor origin/main --
  '*.go'` inside `agent-tui` → 59 of 255 tracked `.go` files;
  `git grep -l -i agent-tui HEAD -- '*.go'` inside `agent-supervisor` → 2
  of 30. Zero literal Go imports either direction
  (`git grep -n '"github.com/jonhill90/agent-supervisor' -- '*.go'` → 0).
  `agent-supervisor` ships 4 of its own lane viewers
  (`scripts/supervisor/laneview/{text,tui,opensessions,dock}.sh`, per this
  repo's own `CLAUDE.md`) independent of `agent-tui`.
- No external consumers: `gh api repos/jonhill90/agent-supervisor
  --jq '.forks_count, .stargazers_count'` → `0, 0`; all issue/PR authors,
  all-time, all states, are `jonhill90`.
- Cross-repo PR reference rate, last 30 merged PRs each repo, measured
  directly (not the council's first, higher estimate — corrected once
  checked): `agent-supervisor` 12/30 mention `agent-tui`/`keelson`/
  `estate`; `agent-tui` 15/30 mention `agent-supervisor`.
- Two independently shipped drift bugs caused by the repo boundary:
  `agent-tui#19` (`AllStates`'s guard passed while blind on dynamic
  assignments -- found during independent review of `agent-supervisor#149`,
  which had just added the `never-busy` lane state `AllStates` didn't know
  about), `agent-supervisor#490`
  (cross-repo PR authorship unresolvable when a lane's issue and PR live in
  different repos, `#472`/`#473`).
- `agent-tui/internal/reposcan` (`agent-tui#131`/`#132`): a 199-line
  package plus a 131-entry manifest that exists solely to catch bare `#N`
  issue references resolving to the wrong repo; caught 3 real instances,
  including one a prior manual sweep had already missed.
- `agent-supervisor#304` ("agent-supervisor is dispatching into agent-tui's
  and skills' lanes, and the ledger cannot detect it"), filed 2026-08-17,
  **still open**. Named here as a live, unresolved coupling risk a merge
  does not by itself close — it is a routing/audit gap, not a repo-boundary
  bug, so folding the repos together would not automatically fix it either.

## Process note

A two-seat council (split-is-load-bearing vs. merge-is-right) was convened
first, each seat required to ground its argument in this repo's own
measured evidence rather than general merge/split reasoning; a
devils-advocate pass then attacked the split-seat's conclusion directly and
found it survived on three of four fronts, with the fourth (no trigger
condition on "wait for the go-public decision") accepted as a real gap. A
follow-up measurement (the asymmetric `.go`-file dependency count, verified
independently above) and a hardened restatement of intent ("neither
component is usable outside this meta-harness," explicit preference against
carrying two repos) arrived after that pass and were not available to it;
both are weighed directly in this document rather than triggering a second
full council run, since neither contradicts anything the first pass
measured — they extend the record, they don't dispute it.
