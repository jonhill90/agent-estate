# The dispatch path

`2026-08-21`, first of three diagrams (loop-and-its-guards and memory are
next — jonhill90/agent-dotfiles#299).

Jon asked: *"Do we have any docs that tell me how these things work, with
charts or diagrams and such."* Measured answer: `docs/` in this repo and
`agent-dotfiles` together carry roughly 1,500 lines of prose about the
dispatch path and zero committed diagrams. This is the first one.

**Every diagram below is Mermaid, rendered from the code cited beside each
node — file and line number, checked against `origin/main` at commit
`14c2904` on 2026-08-21.** Where a path could not be traced to a specific
line, it says so instead of drawing a clean arrow. The happy path is five
boxes; the failure exits are the point, and they take up most of this
page on purpose.

## Where this lives, and why

This diagram lands in `agent-supervisor/docs/`, not `agent-dotfiles/docs/`,
for one reason: **every citation on this page points at a script in this
repo.** `docs/decisions/0003-independent-review-required.md` already
documents part of the mechanism diagram 5 draws (the merge-time
independent-review gate) — keeping this page beside it means both can be
reviewed and kept honest in the same PR whenever `dispatch.sh`,
`core.py`, or `verdict-independence.sh` change. `agent-dotfiles/docs/`
holds prose about the *estate* (harness config, skill roster, PRD/SPEC for
`agent-dotfiles` itself) — none of it currently cites a line number in
this repo, and adding a diagram that does would put the thing most likely
to go stale (line numbers in a file that changes constantly — 2,471 lines
in `dispatch.sh` alone, patched dozens of times in the last month) in a
different repository from the code it describes, with no shared CI to
catch drift. Docs-code proximity wins here even though it means Jon has
to know to look in two repos for "how do the lanes work" — the tradeoff
this page's own existence is arguing against on the *prose* side, but the
diagram's job is to stay true to the code, and that job is easier one
repo over.

---

## 1. The happy path, and where each guard sits

```mermaid
stateDiagram-v2
    classDef focal fill:#fceee6,stroke:#eb6c36,color:#2d3142,stroke-width:2px
    classDef normal fill:#f5f5f5,stroke:#2d3142,color:#2d3142
    classDef muted fill:#ececec,stroke:#4f5d75,color:#4f5d75

    [*] --> ClaimReserved: claim.sh take succeeds\ndispatch.sh:1485
    ClaimReserved --> WorktreeBuilt: worktree.sh new\ndispatch.sh:1523
    WorktreeBuilt --> CollisionChecked: collision-check.sh check\ndispatch.sh:1648
    CollisionChecked --> Refused: REFUSE overlap, no --force\ncollision-check.sh:376
    CollisionChecked --> ClaimLive: ALLOW (no-conflict / unknown / forced)\ncommit-lane-claim, dispatch.sh:2042\ncore.py:2864 status->delivered
    ClaimLive --> Delivered: brief lands in pane / claude-print\nmark_delivered, core.py:2460\ndelivered_at stamped
    Delivered --> Accepted: cli.py accept (no-pane transports only)\ncore.py:2510, accepted_at stamped
    Delivered --> Complete: lane calls complete() directly\ncore.py:3177, completed_at stamped
    Accepted --> Complete: lane calls complete() directly
    Complete --> ReviewDispatched: PR opened, --reviews-pr dispatch\ndispatch.sh:1229 contributor exclusion
    ReviewDispatched --> Merged: merge-pr.sh: ci_gate.py + verdict-independence.sh\ndocs/decisions/0003
    Refused --> [*]: claim + worktree unwound\ndispatch.sh:1655-1659

    class ClaimLive,Delivered focal
    class ClaimReserved,WorktreeBuilt,CollisionChecked,Accepted,Complete,ReviewDispatched,Merged normal
    class Refused muted
```

The ledger's own status enum (`core.py:24,28`):
`TERMINAL_STATUSES = ("complete", "failed", "cancelled")`,
`SOURCE_TASK_STATUSES = ("created", "delivered", "accepted", "running",
"complete", "failed", "cancelled")`. A claim placeholder reuses the same
column: `CLAIM_STATUS_RESERVED = "created"`,
`CLAIM_STATUS_LIVE = "delivered"` (`core.py:82-83`) — the two coral states
above are the same status value doing two different jobs, which is exactly
what diagram 3 goes wrong.

**Ordering note, confirmed by reading the script rather than assumed:**
the collision check runs *after* the worktree is built (`dispatch.sh:1648`,
step 3.2), not before the claim. It has to — `_files_changed_in_worktree`
needs a real `git diff` to read, and that diff does not exist until the
worktree does (`dispatch.sh:1634-1636`). A refusal at that point unwinds
both the claim and the worktree (`dispatch.sh:1655-1659`) — the estate is
left exactly as it found it, not holding a half-built lane.

**Caveat: that unwind is unconditional for the claim, not for the
worktree.** `abort_send` (`dispatch.sh:1562-1584`) always releases the
lane claim, but only tears down the worktree when `cleanup_worktree`
stays `1`. If the lane's own pane still appears to be inside `$WORKTREE`
and `dispatch_rehome_lane` cannot move it out, `cleanup_worktree` is set
to `0` and the worktree is deliberately left in place with a
`--rehome-lane` recovery hint printed to the operator (`dispatch.sh:1571-
1573`) — "the estate is left exactly as it found it" holds for the claim,
not for the worktree, in that one case.

---

## 2. #414 and #401: where `delivered_at` and `completed_at` diverge

```mermaid
flowchart TD
    classDef focal fill:#fceee6,stroke:#eb6c36,color:#2d3142,stroke-width:2px
    classDef normal fill:#f5f5f5,stroke:#2d3142,color:#2d3142
    classDef muted fill:#ececec,stroke:#4f5d75,color:#4f5d75
    classDef historical fill:#ececec,stroke:#4f5d75,color:#4f5d75,stroke-dasharray: 4 3

    Delivered["Delivered<br/>delivered_at set<br/>core.py:2460"]
    Accepted["Accepted<br/>accepted_at set, no-pane transports only<br/>core.py:2510"]
    StuckAccepted["#414: stuck at status=accepted<br/>completed_at NEVER set<br/>worker backgrounded a command, returned control<br/>reconcile_lane_completions.py:76-90"]
    OldSweep["414's own fix: list_delivered_open_tasks sweep<br/>never sees this row again once accepted<br/>(accept() moved it out of that query)"]
    Sweep3["3rd reconciler sweep<br/>_sweep_nonobservable(list_accepted_open_tasks())<br/>stale_after=3600s dwell<br/>reconcile_lane_completions.py:85-90"]
    Evidence{"_lane_log_pr_url:<br/>does lane-logs/&lt;task&gt;.log<br/>name a PR URL?<br/>reconcile_lane_completions.py:97-105,302"}
    CompleteEvidence["Complete (from evidence)<br/>_complete_from_evidence<br/>reconcile_lane_completions.py:105,319"]
    FailReconcile["status=failed<br/>note: 'no signal arrived' (not 'failed, not completed')<br/>written to &lt;task&gt;.reconcile.md, NOT &lt;task&gt;.md<br/>fail_stale_acceptance, reconcile_lane_completions.py:106-119"]
    Historical["#401 (fixed, PR agent-supervisor#424, merged 2026-08-20):<br/>before this evidence check existed, both fail paths<br/>stamped 'failed, not completed' straight to the<br/>canonical &lt;task&gt;.md with no PR check --<br/>133/817 result files carried it, 31 named a merged PR"]

    Delivered --> Accepted --> StuckAccepted
    StuckAccepted -.-> OldSweep
    StuckAccepted --> Sweep3 --> Evidence
    Evidence -->|"yes"| CompleteEvidence
    Evidence -->|"no"| FailReconcile
    FailReconcile -.->|superseded by| Historical

    class StuckAccepted,FailReconcile focal
    class Delivered,Accepted,Sweep3,Evidence,CompleteEvidence normal
    class OldSweep,Historical historical
```

**#414's exact shape** (`reconcile_lane_completions.py:76-90`): `accept()`
is the one place `status` ever becomes `'accepted'`
(`Ledger.list_accepted_open_tasks`'s own docstring). A `claude-print` or
`pi-rpc` worker that gets far enough to call `cli.py accept` moves its row
out of `list_delivered_open_tasks` — the query every earlier sweep reads —
and if that worker then backgrounds a long-running command and returns
control without ever calling `complete()`, `delivered_at` and
`accepted_at` are both stamped, `completed_at` never is, and nothing was
watching that row anymore. Measured live: five `claude-print` dispatches
sat at `status=accepted` for 2+ hours, zero commits, zero comments.

**#401 is fixed, not open** — worth stating plainly, because the issue
brief describing this diagram was written against the defect, not the
fix. `git log --oneline --all | grep 401` on this repo turns up five
commits and one PR (`#424`, merged `2026-08-20T23:53:24Z`) closing it. The
code today runs the evidence check shown above *before* either failure
path stamps anything (`reconcile_lane_completions.py:92-119`), and the
verdict — when there is genuinely no PR evidence — no longer claims the
work failed, only that no signal arrived, written to a sibling
`.reconcile.md` path so a late, genuine `complete()` from the lane itself
is never rejected by finding its canonical slot already taken
(`_write_result`'s `suffix` parameter, same file). Drawing the pre-#401
behavior as live would be exactly the failure this brief warns against —
disagreeing with the code — so it is shown dashed, as history the fix
replaced, not as a path #401-numbered issues can still take today.

---

## 3. A claim that outlives its lane

```mermaid
flowchart TD
    classDef focal fill:#fceee6,stroke:#eb6c36,color:#2d3142,stroke-width:2px
    classDef normal fill:#f5f5f5,stroke:#2d3142,color:#2d3142
    classDef muted fill:#ececec,stroke:#4f5d75,color:#4f5d75
    classDef unknown fill:#ffffff,stroke:#4f5d75,color:#4f5d75,stroke-dasharray: 4 3

    subgraph A["A: dead lane still holds the claim"]
      Reserved["claim placeholder, status=created<br/>CLAIM_STATUS_RESERVED, core.py:82<br/>inserted by claim_lane, core.py:2737"]
      Live["claim committed, status=delivered<br/>CLAIM_STATUS_LIVE, core.py:83<br/>commit_lane_claim, core.py:2864-2912"]
      DeadOwner1["dispatcher SIGKILLed / OOM-killed<br/>before brief sent"]
      DeadOwner2["lane's process dies<br/>after brief sent"]
      Reaped["reap_stale_lane_claims DELETEs it<br/>only rows still CLAIM_STATUS_RESERVED<br/>core.py:2961, 2992, 3019<br/>-> lane free again"]
      Stuck["reap_stale_lane_claims SKIPS this row<br/>LIVE claims are never reaped, by design<br/>core.py:2875-2877<br/>-> lane reads occupied forever"]
      NextDispatch["next dispatch attempt on this lane"]
      RefusedNoFree["dispatch.sh: no free lane<br/>refused until a human runs<br/>cli.py register (core.py:2875)"]

      Reserved --> DeadOwner1 --> Reaped
      Live --> DeadOwner2 --> Stuck --> NextDispatch --> RefusedNoFree
    end

    subgraph B["B: a cancelled/racing ledger row lets two lanes run one issue"]
      Free["lane_free() read by two dispatchers<br/>at nearly the same instant<br/>-- a QUERY, not a claim<br/>core.py:2740-2749"]
      InsertA["Dispatcher A: claim_lane() INSERT"]
      InsertB["Dispatcher B: claim_lane() INSERT"]
      Atomic["flock + BEGIN IMMEDIATE +<br/>one_open_task_per_lane unique index<br/>core.py:2754-2760<br/>only ONE INSERT can win"]
      Winner["winner proceeds"]
      LoserRefused["loser's INSERT raises IntegrityError<br/>-> claim_lane refuses, not merges"]
      HistoricalRace["historical shape closed by the guard above<br/>(agent-supervisor#183 round 3):<br/>record_dispatch used to CANCEL whatever task<br/>was outstanding instead of refusing --<br/>'the second writer always wins silently'<br/>core.py:2746-2749"]
      PRRace["PR-scoped variant: dispatch.sh step 0.6<br/>pr-lane check is a plain read (TOCTOU)<br/>dispatch.sh:903-934, pr-lane call at 938"]
      PRTrigger["real guarantee: ONE_OPEN_PULL_PER_SOURCE_REF<br/>BEFORE INSERT trigger at write time<br/>agent-supervisor#169, core.py:1038-1094<br/>reproduced by test_dispatch.sh's run_race case"]
      TodayUnknown["Jon measured this race TWICE on 2026-08-21<br/>(a refused dispatch on a dead lane's claim,<br/>and a cancelled row racing its own process).<br/>UNKNOWN: no ledger/log citation in the code itself<br/>ties those two specific live incidents to a line --<br/>the mechanisms above are what the code shows,<br/>not a trace of that day's two events."]

      Free --> InsertA & InsertB --> Atomic
      Atomic --> Winner
      Atomic --> LoserRefused
      Atomic -.-> HistoricalRace
      Free -.-> PRRace -.-> PRTrigger
    end

    TodayUnknown -.-> A
    TodayUnknown -.-> B

    class Live,Stuck,RefusedNoFree,HistoricalRace focal
    class Reserved,DeadOwner1,DeadOwner2,Reaped,NextDispatch,Free,InsertA,InsertB,Atomic,Winner,LoserRefused,PRRace,PRTrigger normal
    class TodayUnknown unknown
```

**Subcase A is a deliberate asymmetry, not an oversight.**
`reap_stale_lane_claims` (`core.py:2961`) only deletes rows still in
`CLAIM_STATUS_RESERVED` (`core.py:2992`, `2961-3019`) — a claim that
reached `CLAIM_STATUS_LIVE` via `commit_lane_claim` is never reaped even
when its owner process is provably dead, because "the owner is dead" does
not by itself distinguish "claim taken, nothing sent" from "claim taken, a
brief is live in the pane" (`core.py:2864-2880`, `commit_lane_claim`'s own
docstring). The cost of that asymmetry is exactly the shape Jon measured:
a dispatch refused because a dead lane's *live* claim cannot be
auto-cleared, and needs `cli.py register` run by a human.

**Subcase B, the mechanism is real and closed at two different layers,**
but I could not trace either of *today's* two specific incidents to a
log line or ledger row cited in the code — the code shows what the
guard does and what it replaced, not a record of a particular race on
2026-08-21. Marked unknown (dashed) rather than guessed. What the code
does show: the plain-lane race is closed at the write (`claim_lane`'s
atomic `INSERT` under `flock` + `BEGIN IMMEDIATE` +
`one_open_task_per_lane`, `core.py:2754-2760`) — the historical failure it
replaced is named in the same docstring: `record_dispatch` used to
*cancel* whatever task was already outstanding rather than refuse the
second writer, so "the second writer always wins silently" (measured,
`agent-supervisor#183` round 3, `core.py:2746-2749`). The PR-scoped
variant is a second, later fix: `dispatch.sh`'s own step 0.6 (`pr-lane`
check) is admitted in its own comments to be a plain read with a real,
multi-second TOCTOU window (`dispatch.sh:903-934`, the `pr-lane` call
itself at line 938) — what actually closes
that race is the `ONE_OPEN_PULL_PER_SOURCE_REF` trigger firing at INSERT
time (`agent-supervisor#169`, `core.py:1038-1094`), reproduced directly by
`test_dispatch.sh`'s own `run_race` case: two full dispatches, two lanes,
one PR, both `delivered`, and the second writer's `INSERT` now fails as a
unique-index violation instead of silently winning.

---

## 4. The collision check, and "ALLOW unknown"

```mermaid
flowchart TD
    classDef focal fill:#fceee6,stroke:#eb6c36,color:#2d3142,stroke-width:2px
    classDef normal fill:#f5f5f5,stroke:#2d3142,color:#2d3142
    classDef muted fill:#ececec,stroke:#4f5d75,color:#4f5d75

    Start["worktree already built for #ISSUE<br/>(collision-check.sh runs AFTER the worktree,<br/>dispatch.sh:1648 -- first point a real<br/>git diff can be read)"]
    Detect["detect_candidate_files:<br/>union of --<br/>1. backtick paths in issue/brief<br/>2. candidate's own branch content, if resumed<br/>3. gh pr diff --name-only, if --pr/--reviews-pr<br/>collision-check.sh:31-47"]
    Empty{"candidate file set empty?"}
    AllowUnknown["ALLOW unknown -- no file signal found ...<br/>allowing rather than blocking most dispatches<br/>on a signal most issues never give<br/>exit 0, collision-check.sh:222-223"]
    InFlight["compare against every IN-FLIGHT lane's<br/>ACTUAL worktree diff (git diff, not a guess)<br/>-- any task not complete/failed/cancelled<br/>with a recorded worktree path<br/>collision-check.sh:25-29"]
    Overlap{"same file touched<br/>by an in-flight lane?"}
    AllowClean["ALLOW no-conflict<br/>exit 0"]
    Forced{"--force passed?<br/>agent-supervisor#291<br/>dispatch.sh:141-148"}
    AllowForced["ALLOW forced -- &lt;lane&gt; &lt;file&gt; ...<br/>exit 0, collision-check.sh:69-72"]
    Refuse["REFUSE &lt;lane&gt; &lt;file&gt; ...<br/>exit 1, collision-check.sh:376<br/>dispatch unwinds: claim + worktree released"]

    Start --> Detect --> Empty
    Empty -->|yes| AllowUnknown
    Empty -->|no| InFlight --> Overlap
    Overlap -->|no| AllowClean
    Overlap -->|yes| Forced
    Forced -->|yes| AllowForced
    Forced -->|no| Refuse

    class Refuse focal
    class AllowUnknown,AllowClean,AllowForced normal
    class Start,Detect,Empty,InFlight,Overlap,Forced muted
```

**"ALLOW unknown" is not "safe."** It means none of the three
file-detection signals fired: no backtick-quoted path in the issue or
brief that resolves via `git ls-files`, no prior content on the
candidate's own branch, and no PR diff to read. `collision-check.sh`'s own
header states the reasoning plainly (`collision-check.sh:54-62`, quoting
the originating issue): *"If the file set cannot be determined: say
UNKNOWN, ALLOW the dispatch, and log it."* Most issues never name a file,
so refusing on unknown would refuse nearly every dispatch — the one place
this script's usual fail-closed posture deliberately inverts, and only for
the *candidate's* side; an in-flight lane whose own worktree cannot be
read is simply skipped for that one lane (`collision-check.sh:58-62`), not
treated as making the whole check unknown. What overlap actually means
here is whole-*file* overlap only — not line-range or hunk-level, a
predictive signal this estate rejected once already
(`collision-check.sh:12-23`).

---

## 5. Independent review — and why a fix lane is not one

```mermaid
flowchart TD
    classDef focal fill:#fceee6,stroke:#eb6c36,color:#2d3142,stroke-width:2px
    classDef normal fill:#f5f5f5,stroke:#2d3142,color:#2d3142
    classDef muted fill:#ececec,stroke:#4f5d75,color:#4f5d75

    PROpened["PR N opened by lane A<br/>source_kind='pull', source_ref=N<br/>recorded at dispatch time, core.py:1908"]
    ReviewReq["review requested: dispatch.sh --reviews-pr N<br/>(or a fix-pass, --pr N, after REQUEST CHANGES)"]
    Resolve["resolve AUTHOR_LANES = FULL contributor set for PR N<br/>-- every lane that EVER contributed, not just 'the' author<br/>resolve-pr-contributors.sh via dispatch.sh:765-826<br/>core.py:1902 'has anybody EVER contributed'"]
    Unresolvable{"contributor set<br/>resolvable?"}
    RefuseAuthorship["refuse the WHOLE dispatch<br/>fail closed -- unresolved authorship is<br/>never treated as 'no author, safe to admit'<br/>dispatch.sh:799-825"]
    IsContributor{"candidate lane IN AUTHOR_LANES?<br/>(includes a fix-pass lane dispatched<br/>with --pr N against the same PR --<br/>widened by #190, core.py:1902-1913)"}
    Excluded["candidate excluded:<br/>'a contributor does not review their own work'<br/>dispatch.sh:1229"]
    FreeLane["a lane with NO contribution to PR N<br/>is picked as the reviewer"]
    Verdict["review lane posts a verdict<br/>(APPROVE / REQUEST CHANGES)"]
    MergeAttempt["merge-pr.sh &lt;repo&gt; N<br/>-- the only path meant to merge a PR"]
    CiGate["ci_gate.py: PR must be green<br/>(gh pr merge alone does not check this)"]
    VerdictIndep["verdict-independence.sh:<br/>author_lane_for() ALSO widened to the FULL<br/>contributor set (#200) -- a fix-pass lane<br/>cannot approve/merge its own fix here either<br/>verdict-independence.sh:279-289"]
    WhyMergeGate["THIS is why merge-time enforcement exists<br/>separately from the dispatch-time exclusion above:<br/>free text typed into a lane's pane can reach<br/>'gh pr merge' directly, walking around every<br/>dispatch-time guard -- docs/decisions/0003"]
    Merged["merge proceeds"]
    RefuseMerge["merge refused --<br/>same lane, or unresolved / unreadable verdict,<br/>never treated as safe -- docs/decisions/0003"]

    PROpened --> ReviewReq --> Resolve --> Unresolvable
    Unresolvable -->|no| RefuseAuthorship
    Unresolvable -->|yes| IsContributor
    IsContributor -->|yes| Excluded
    IsContributor -->|no| FreeLane --> Verdict --> MergeAttempt --> CiGate --> VerdictIndep
    VerdictIndep -.-> WhyMergeGate
    VerdictIndep -->|independent, green| Merged
    VerdictIndep -->|same lane / unresolved| RefuseMerge

    class Excluded,RefuseAuthorship,RefuseMerge focal
    class PROpened,ReviewReq,Resolve,FreeLane,Verdict,MergeAttempt,CiGate,VerdictIndep,Merged normal
    class Unresolvable,IsContributor,WhyMergeGate muted
```

**A fix lane is not an independent review, and the code says so twice, at
two different layers, because one layer alone was proven not to hold.**
`dispatch.sh`'s `AUTHOR_LANES` set (built by resolving *every* lane that
ever contributed to PR N, not narrowed to a single "the author") already
excludes a fix-pass lane from being dispatched to review that same PR —
widened by `agent-supervisor#190` specifically because a narrower
single-author lookup missed exactly this case: *"a FIX-PASS task
dispatched against the same issue to address review findings is a second,
later contributor to the same PR"* (`core.py:1902-1913`). But dispatch-time
exclusion only governs who gets *assigned* a review — it does nothing
about a prompt typed directly into a lane's pane that reaches `gh pr
merge` without going through `dispatch.sh` at all
(`docs/decisions/0003-independent-review-required.md`). That is why
`merge-pr.sh`'s own `verdict-independence.sh` recomputes the same
contributor set independently at merge time (also widened to include a
fix-pass lane, by `agent-supervisor#200`,
`verdict-independence.sh:279-289`) rather than trusting that dispatch
already enforced it — `merge-pr.sh` is, in its own words, "the one place
in the system that cannot be skipped by habit."

---

## Sources

Every citation above is checked against `jonhill90/agent-supervisor`
`origin/main` at commit `14c2904` (2026-08-21):
`scripts/supervisor/dispatch.sh`, `claim.sh`, `collision-check.sh`,
`core.py`, `reconcile_lane_completions.py`, `merge-pr.sh`,
`verdict-independence.sh`, and `docs/decisions/0003-independent-review-required.md`.

`lane-done.sh` is a *third* path into `Complete`, not drawn as a separate
node above to stay inside the diagram's own budget: it runs in the
supervisor's pane (not the worker's) when a `wait-for` channel signals
that a still-correctly-named window finished, and calls `cli.py
record-completion` rather than `cli.py complete` — no `TMUX_PANE`
ownership check, no `--result-file`, authenticated instead by the task's
own recorded `pane_nonce` (`cli.py:1067-1112`, `lane-done.sh:180-192`).
`record_completion`'s own docstring is explicit that this still resolves
to `Ledger.complete` (`core.py:3177`, the same terminal transition cited
in diagram 1) — but it also states one thing this path does NOT do that
the worker-driven path does: no `completion:<task>` notification event
reaches the supervisor lane through this route (`cli.py:1079-1083`). That
is a real, cited gap this page is marking rather than smoothing over: a
completion recorded this way is durable in the ledger but silent to
whatever reads that notification channel.
