# Do the reviewers earn their tokens?

**Yes, on the sample this estate can currently produce — with real costs and
real gaps in what the sample can prove.** Every one of the six PRs that
merged out of the reviewed batch converged to a unanimous APPROVE only after
review forced a fix for a defect the author's own passing test suite had not
caught. No investigated REQUEST CHANGES finding in this sample was later
shown to be wrong. But review is not free: it consumed more agent turns and
more agent time than authoring did in the same window, and one PR ran
thirteen review rounds and still never merged.

This answers Jon's 2026-08-12 question with numbers from one session, not
with a belief about review being good practice in general.

## Sample and window

Source: `~/.local/state/estate/ledger.jsonl`, the append-only record of every
dispatched turn on this machine. **The file holds 162 records total, and
they span exactly one continuous session: 2026-09-02 23:36 UTC through
2026-09-03 06:29 UTC — about 7 hours.** There is no older data; the estate's
Go rewrite (`src/estate`, the tool that writes this ledger) is new enough
that this is the entire recorded history of dispatch-and-review under it.
The repository's other ~290 PRs (created 2026-08-18 through 2026-08-30, under
the retired shell/Python supervisor) used a different review mechanism that
this ledger does not record and this analysis does not cover.

Within that one session, 51 of the ledger's `complete` records carry a
`VERDICT:` line — a review turn — against 9 pull requests (#913, #914, #915,
#916, #917, #918, #922, #924, #926), plus one earlier combined review
(`review-907-910`, a single dispatch that reviewed four PRs before this
numbering scheme started). 21 further `complete` records are non-review
turns: authoring and fix-pass work. That is the whole population; nothing
here is a sample of a larger pool, so no percentage below needs a confidence
interval, but every one is still a percentage of 51 or fewer events and
should be read that way — this is one night's evidence, not a policy proof.

**Verdict split, all 51 review turns:** 35 REQUEST CHANGES, 15 APPROVE, 1
COULD NOT DETERMINE. That 69% REQUEST CHANGES rate does **not** mean two of
three reviews were independently wasted — most of these are sequential
rounds against an evolving head (round *N* finds something, the author fixes
it, round *N+1* re-reviews the fix). It measures how much iteration a PR
needed to reach clean, not how often an independent reviewer was overruled.

## What review caught that the author's own tests had already passed

Every example below is cited to the PR and, where it exists, the ledger
record or PR comment that shows the failure reproduced, not just asserted.

- **[#909 → fixed before merge, confirmed by #917] Worktree teardown deleted
  real work twice, in two different ways.** The first combined review
  (`review-907-910`, 2026-09-02T23:43Z) found that `Dirty()` used plain `git
  status --porcelain`, which omits gitignored paths by git's own default —
  so an agent's real, uncommitted, gitignored output looked like an empty
  worktree and was deleted on teardown. Fixed with `--ignored`. Three review
  rounds later, on the *next* PR in the same area (#917), a **codex** review
  seat found the mirror-image bug: a turn that **committed** its output left
  a clean `git status`, so `Remove()` ran `git branch -D` and destroyed the
  only ref to those commits — an agent that did the tidy thing lost more for
  it. Both defects were data loss; both were caught before any real dispatch
  output was lost, because review ran before merge.

- **[#914, five rounds] The stop-condition guard was defeated five times in
  a row, and the author's own words on round four: "Every one was found by
  an independent reviewer. None was found by the loop inspecting itself."**
  `hasArtifact()` (the check meant to catch a Director loop reporting fake
  progress) was bypassed by: any non-empty string (`"null"`) → a placeholder
  word-list (`"working on it"`) → a looks-like-a-path regex
  (`"still going, read/write path unclear"`) → pointing at something real
  but pre-existing (`touch go.work`) → writing directly to the log instead
  of through the CLI the checks were bolted onto. Each bypass was found by a
  fresh review round, not by re-running the existing test suite, which
  stayed green throughout — the tests encoded the same blind spot the guard
  had.

- **[#916, thirteen rounds, never merged] A cascading self-merge/authorship
  hole that review kept reopening faster than it could be closed, and it is
  the one PR in this sample where review's answer was "do not ship this."**
  In order: `estate authored` let any caller forge authorship
  (council round 1, 2026-09-03T02:33Z, three seats, unanimous REQUEST
  CHANGES) → the exemption meant to protect real reviewers (`RoleReview`)
  was dead code nothing in production ever wrote (same round) → once
  written, it was never *required*, so an unverified reviewer-lane string
  still passed (round 2) → the staleness check that was meant to bind a
  review to a PR head trusted `git`'s attacker-settable committer-date field
  (round 4) → the verdict parser matched the literal string `"VERDICT:
  APPROVE"` **anywhere in the report**, including inside a REQUEST CHANGES
  review's own seat table quoting a prior round (round 4 — reproduced
  against the council's own comments on this very PR) → the `<issue>`
  argument to `estate merge` was never checked against the PR being merged,
  so an author could name an unrelated clean issue and merge anything (round
  5). Each was a distinct, reproduced defect, not a restatement. The PR
  stayed open, unmerged, thirteen rounds in — its own gate, once fixed,
  refused to let its own author merge it, and the work was redone from
  scratch as #934 rather than force through.

- **[#924, closed rather than merged] Review found a worse bug than the one
  the PR was fixing.** The PR fixed the TUI reporting "Director not running"
  when launched outside the repo root. The reviewing seat built the binary
  outside any repository, ran it inside an *unrelated* git repo that
  happened to contain its own `docs/tick-log.jsonl`, and got that repo's
  contents rendered as this estate's Director status — silent fabrication
  with a timestamp on it, not merely an absence. The PR's own doc comment
  had claimed "nothing is invented." Because this PR was also hand-authored
  and therefore ineligible to merge under this repo's own rule, it was
  closed once the underlying bug was fixed differently (and more safely) on
  `main` by #929 — a lane-authored, independently reviewed PR that never
  consulted the working directory at all.

- **[#918] A "calm failure" — the exact category this repo's own docs warn
  about.** `estatus.readLines` mapped every missing-ledger case to `Absent`,
  including a *misconfigured* path (parent directory missing), which the
  TUI then rendered as the reassuring "no turn has ever been dispatched"
  instead of an error. Found and reproduced across three review rounds
  before the distinction (missing-in-an-existing-directory vs.
  missing-parent-directory) was actually implemented.

- **[#913, three rounds] The author's own fix repeatedly cited files and
  commands that did not exist on the branch it was fixing — each fix
  introducing a fresh instance of the exact defect class the PR existed to
  remove.** Round 1: a workflow file was still described as enforcing a rule
  it no longer enforced. Round 2's fix cited `frame_capture_test.go`, real
  only on an unmerged branch. Round 3's fix cited `estate tick check` and
  `estate authored`, real only in the author's uncommitted working tree.
  Three reviewers, three independent catches, and the fix that finally
  stuck wasn't a fourth manual correction — it was `agents_md_test.go`, a
  CI check that verifies every subcommand `AGENTS.md` names is real in
  `main.go`, added directly in response to the pattern review kept finding.

- **[#926, first genuine 3-way parallel council] A fix that looked verified
  by its own evidence was not.** All three concurrently dispatched review
  seats independently found that a dispatch-id collision fix did nothing:
  the deduplicating counter was a package-level variable that reset to zero
  every time a new OS process started, and `estate dispatch` is one process
  per call — so the counter never separated anything, and uniqueness had
  rested entirely on luck (`time.Now()` granularity on that machine). The
  author's own prior "proof" (three ids, each ending in `-1`) was, on
  re-reading after the review, proof the fix had failed, not succeeded.

## Reviewer disagreement, and what it resolved to

Two councils split in this sample, both 2-REQUEST-CHANGES / 1-APPROVE on the
same head:

- **#917, round 1** — codex (isolation/failure-mode lens) and one claude seat
  found the committed-work-deletion bug above; the third claude seat, reading
  for untrue claims rather than for failure modes, approved because nothing
  it checked was false. The minority's findings were real, were fixed, and
  round 2 was a unanimous 3/3 APPROVE.
- **#926, round 1** — same shape: two REQUEST CHANGES caught the inert
  counter, one APPROVE seat missed it. The two refusals were later shown
  correct; round 2 was unanimous 3/3 APPROVE once the pid-based fix landed.

In both cases in this sample, when a council split, the REQUEST CHANGES side
was the one later confirmed right. That is a real signal, not merely
"reviewers found something to say" — but it is **n=2**, both from the same
short session, and should be read as exactly that: a direction, not a
settled ratio.

No case in this sample shows the opposite — a REQUEST CHANGES verdict, once
investigated, turning out to be false. The nearest thing to a dispute is
`#914`'s third finding (three genuinely-blocked ticks would render the same
"STALLED" status as an actual spinning loop): the author declined to add a
sixth heuristic for it, and the *next* review round explicitly ruled that
decision correct rather than treating it as an unresolved disagreement.

## What it cost

Pairing each review's `dispatched`→`complete` timestamps: **51 review turns
totaled ~11,006 seconds (≈183 agent-minutes), averaging 216 seconds each.**
The 21 non-review (authoring / fix-pass) turns in the same window totaled
~7,242 seconds (≈121 agent-minutes), averaging 345 seconds each — individual
author turns ran longer, but there were more than twice as many review turns,
so **review consumed about 52% more total agent-minutes than authoring did**,
and outnumbered authoring turns 2.4:1. This does not separate cost by model
tier (this estate deliberately dispatches cheaper tiers for review and
reserves the expensive tier for judgment calls — a real mitigation this
number doesn't show) and it does not include the token cost of the fix
passes review's findings then required, which is folded into the 21 author
turns above, not billed separately to review.

`#916` is the concentrated cost case: 13 review rounds, real distinct
defects in essentially every round, and the PR still never merged — it was
abandoned and its problem re-dispatched as a fresh PR (#934). Whether that
is "review doing its job thoroughly" or "review costing more than the
change was worth" is a judgment call the record states outright rather than
hides: the parking comment on #916 (2026-09-03T03:37Z) says exactly that —
four rounds in, real findings every round, but "four rounds of my time
against a gate whose stated limit is that it cannot prove anything anyway."

Of the 9 reviewed PRs: 6 merged (#913, #914, #915, #917, #918, #922), 1
closed as superseded by a safer fix elsewhere (#924), 2 stayed open,
superseded by fresh dispatches (#916, #926). **None of the 6 merged PRs
merged with an outstanding REQUEST CHANGES** — every one reached a clean,
unanimous APPROVE (or, for the single-seat PRs, an unrejected APPROVE) before
merge.

## The verdict must land in two places, not one

If you are the reviewer, this is the part that costs a whole extra turn when
missed. `estate merge`'s gate (`internal/gate`) requires your `Verdict:` line
in **two independent places**, not one:

1. the PR comment you post — `Verdict:`, `Review-Lane:`, `Reviewed-SHA:`, the
   convention AGENTS.md's "Merging PRs you did not author" states; and
2. your own review turn's **final returned text**, verbatim-repeating the same
   `Verdict:`/`Review-Lane:` block. That returned text is written by the
   dispatch process into that lane's ledger `Result` field the moment your
   turn exits — `internal/gate`'s `resolveResultVerdict` (`gate.go`,
   `verdict.go`) parses `Verdict:` out of the ledger `Result` the same way it
   parses the PR comment, and cross-checks the two against each other before
   allowing a merge.

Miss the second one and the gate refuses correctly, even though your PR
comment was fine:

```
reviewer <lane>'s ledger record carries no parsable Verdict: line in its own
Result -- cannot cross-check the PR comment against it; the reviewing turn's
own final returned text must repeat the same Verdict:/Review-Lane: block it
posted as a PR comment, refusing
```

This happened for real on PR #1219 (agent-estate#1220): a reviewer posted a
correct `Verdict: APPROVE` comment, then summarised its findings in its own
returned text instead of repeating the verdict block — a perfectly reasonable
thing to do for a thorough review, and unmergeable anyway. A third review
turn had to be dispatched purely to re-record a verdict nobody disputed.

**This is not redundant, and must not be "simplified" back to one place.**
Every lane in this repo pushes through the same shared GitHub login, so a PR
comment alone is not proof of who actually wrote it — anyone with that login
can post a comment claiming `Review-Lane: <any lane>` and `Verdict: APPROVE`.
`gate_test.go`'s `TestBypass_ForgedVerdictCommentImpersonatesReviewer`
(agent-estate#934) is the attack this closes: a forged approval comment
overriding a lane's real `REQUEST CHANGES`. The ledger `Result` is written
locally by the dispatch process from the reviewer subprocess's own output,
never from anything a GitHub comment asserts about itself — it is the one
record a forged comment cannot also forge. So: after posting the PR comment,
make the `Verdict:`/`Review-Lane:`/`Reviewed-SHA:` block the last thing your
own turn returns, not a prose summary of what you found.

## What this data cannot settle

- **The corpus this estate is told to query first is empty in this
  environment.** `~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3`,
  opened read-only as instructed, has 0 rows in `open_questions`,
  `live_parameters`, `pr_verdicts`, `tasks`, and `lanes`, and only 93 rows in
  `prompts` — all from this same 7-hour window. Jon's original 2026-08-12
  question is known here only through this task's own paraphrase of it, not
  as a queryable primary-source quote. A run of this analysis against the
  live corpus, not this sandboxed copy, could add his exact words and any
  answer he's already given himself.
- **Every confirmation in this document is self-reported inside the same
  estate.** The Director dispatches the reviewer, narrates whether the
  finding was real, and dispatches the fix — there is no outside party
  checking whether "confirmed real" was actually true. The mitigating
  evidence is that most confirmations cite a reproduced command and its exit
  code, not just a claim, which is stronger than a bare assertion but is
  still not independent verification.
- **This covers one session on the new Go estate, not the repository's
  longer history.** The other ~290 PRs opened between 2026-08-18 and
  2026-08-30 under the retired shell/Python supervisor are not represented
  here; that era's review discipline (`Review-Lane:`/`Verdict:` PR comments)
  existed but is not captured in `ledger.jsonl`, which this estate only
  started writing recently. A fuller answer would mine `gh pr list
  --state all` comment threads across that whole window the same way this
  document mined nine PRs from one night.
- **Disagreement sample is two data points.** Both split councils in this
  window resolved in the same direction (minority REQUEST CHANGES was
  right). That is suggestive, not a rate — a larger sample could easily show
  councils splitting the other way.

## Bottom line

On the only evidence this estate can currently produce — one 7-hour session,
51 review turns against 9 PRs — review is not decoration. It caught data-loss
bugs before they shipped, a five-round cascade of stop-condition bypasses the
author's own tests never touched, a fabricated-repo-data bug worse than the
one under review, and a self-merge authorization hole so persistent it
justified abandoning and redispatching the PR rather than continuing to
patch it. No confirmed finding in this sample was wrong. Set against that:
review outnumbered authoring 2.4:1 in turns and cost roughly 52% more total
agent time in this window, and thoroughness has a visible ceiling — #916's
thirteen rounds bought real defects caught, and also a PR that still isn't
merged. The honest answer is not "review always pays for itself" — it is
"in this sample, review paid for itself on every PR that shipped, and the
one PR where it didn't converge was the one the estate chose not to ship."
