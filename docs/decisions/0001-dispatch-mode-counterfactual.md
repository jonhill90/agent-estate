# 0001 — The dispatch-mode counterfactual, measured

*Measured 2026-09-04 01:17:06Z–01:20:01Z on the operator's own Mac (18432MB
RAM, 11 cores, macOS 26.6.2, Claude Code 2.1.220). Raw evidence:
[`docs/evidence/1002-dispatchbench/`](../evidence/1002-dispatchbench/).
Reproduce with `go run ./cmd/dispatchbench -turns 10 -context-file
../../AGENTS.md` from `src/estate`.*

Status: **measured**. agent-estate#1002. Supersedes nothing; the first attempt
at this measurement produced no persistent-arm figures and was stopped by the
operator mid-run.

*This is the first file in `docs/decisions/` — the directory did not exist
before it. `AGENTS.md` cites a `docs/decisions/0001-sqlite-ledger.md` and says
plainly that it is **not in this tree**; that remains true, and this is a new
sequence rather than that document renumbered.*

## The question

`estate dispatch` runs every turn as a fresh `claude -p` subprocess that exits
when the turn ends. The alternative — one long-lived agent per lane, fed turn
after turn — was **asserted to be worse and never measured**. #1002 asked for
the counterfactual on three axes: dollars per turn, whether a persistent
lane's cache hit rate degrades as its context grows, and peak resident memory.

## What was run

**Workload.** 10 turns per arm. Turn *i* asks the agent to read the *i*-th of
10 generated 40-line text files in its working directory and answer two facts
about it on one line. Each arm gets its own scratch directory containing this
repository's own `AGENTS.md` as `CLAUDE.md`, so the cached prompt prefix is
production-shaped rather than an empty project's. Both arms ran the identical
prompts in the identical order, on the harness's own default model
(`claude-opus-5`, medium effort), through the same `--dangerously-skip-permissions`
flag.

**Sample size: 20 turns total, 10 per arm, one run.** That is small, and it is
stated rather than dressed up. It is enough to settle the cache question,
which moves by 49 percentage points, and enough to show the direction of the
memory question; it is not enough to put a confidence interval on anything.

**Arms.**

- *Stateless* — `claude -p --output-format json` per turn, run through
  `internal/harness` itself rather than a copy of it, so the thing measured is
  the production dispatch path.
- *Persistent* — one interactive `claude` in a tmux pane on a private socket,
  never restarted, fed each prompt with `send-keys`.

## The numbers

| | stateless | persistent |
|---|---|---|
| turns answered | 10 / 10 | 10 / 10 |
| total cost, first-party | **$3.4136** (sum of 10 `-p` envelopes) | **$1.56** (the lane's own `/cost`) |
| cost per turn | **$0.341** | **$0.156** |
| mean wall-clock per turn | **8.7s** | **4.3s** |
| prompt tokens served from cache, turn 1 | 65.9% | 49.2% |
| …turn 10 | 69.1% | **98.7%** |
| peak worker-tree RSS, turn 1 | 643MB | 694MB |
| …turn 10 | 642MB | **763MB** |
| memory returned when the turn ends | **yes** | no |

Per-turn detail is in
[`summary.md`](../evidence/1002-dispatchbench/summary.md); every figure below
is derived from [`results.json`](../evidence/1002-dispatchbench/results.json).

### The cache hypothesis is refuted, and in the opposite direction

#1002 asked whether a persistent lane's cache hit rate **degrades** as its
context grows, and said that degradation, if real, would be the finding.

It does not degrade. It improves, sharply, and then holds:

    persistent, cached share of prompt tokens by turn
    49.2  98.3  98.4  98.4  98.5  98.4  98.6  98.6  98.6  98.7

Turn 1 pays to create the cache (51,453 cache-creation tokens). From turn 2
on, cache creation collapses to a near-constant ~1,768 tokens per turn while
cache reads grow linearly at about +3,550 per turn — the conversation getting
longer, all of it cached. The stateless arm sits flat at 69–79%, because each
fresh process re-establishes only the prefix and never accumulates a
conversation to cache.

**Within a 10-turn conversation, growing context makes a persistent lane
cheaper per turn, not more expensive.** What this run does *not* establish is
where that stops: a lane approaching the model's context window, or one whose
cache TTL lapses between turns, is outside what 10 back-to-back turns can see.

### Memory is the real cost, and it is monotone

    persistent worker-tree peak RSS by turn (MB)
    694  711  733  750  751  756  759  760  762  763

    stateless worker-tree peak RSS by turn (MB)
    643  646  645  641  643  646  642  637  637  642

The stateless arm is flat and each turn's memory is returned at exit. The
persistent arm grows monotonically — **+69MB across 10 turns**, decelerating
but never falling — and returns nothing until the lane is killed.

Ten turns cannot establish where that curve settles, and this record does not
extrapolate it. What it does establish is the shape: a persistent lane's
footprint is a floor that only rises, and the in-flight cap of 6 would be
sizing something that grows rather than something that resets.

### Host-wide, and the part of it that is contaminated

The lane was dispatched at `inflight = 0` specifically so host-wide peak
resident memory would be measurable rather than contaminated. **It was clean
for the first two thirds of the run and then was not**: the ledger records a
second dispatch, `1012-1788484764546279000-39569-1`, at 01:19:24Z, inside the
window.

So the host-wide claim is split at that timestamp rather than averaged across
it:

- **Genuinely alone** (all 10 stateless turns, persistent turns 1–3):
  host-wide sum-of-RSS peaked at **14221MB**; minimum free memory **6543MB**,
  against a floor of 2048MB; **zero swapouts in every 2s sample**.
- **After 01:19:24Z** (persistent turns 4–10): host-wide sum-of-RSS peaked at
  14490MB and minimum free memory was 6912MB. Those two figures include
  another lane's work and are not attributed to this benchmark.

Per-worker figures are unaffected throughout: the worker gauge sums a named
process tree, not the host.

Sum-of-RSS double-counts shared pages and is reported as a relative
indicator, not as bytes of RAM consumed. Free memory, read through the
estate's own `pressure.Host`, is the host-wide figure to trust.

## What was not measured, and is not guessed

- **A per-turn dollar figure for the persistent arm.** An interactive session
  emits no cost envelope. `$1.56` is the whole session, from the CLI's own
  `/cost`; dividing by 10 gives a mean, not ten measurements.
- **Two first-party cost sources that do not fully reconcile, reported as
  such.** For the persistent arm, `/cost` and the session transcript agree on
  cache-creation tokens (67.7k against 67,494) and disagree on cache reads
  (1.6m against 1,107,339) and input (4.6k against 39). The transcript reader
  is the validated one — `TestTranscriptTotalsMatchTheEnvelopeForTheSameTurn`
  pins it against a real `claude -p` envelope for the same turn, to the token
  — but that validation covers a *stateless* turn, and what composes `/cost`'s
  aggregate is not visible from outside. **The conclusion does not depend on
  which is right:** `/cost`'s $1.56 is the larger of the two candidate figures
  for this arm and it is still less than half the stateless arm's $3.4136.
  Reconciling them is open work, not a number to invent.
- **No dollar figure was computed from tokens.** Multiplying token counts by a
  price table is the estimating `internal/harness`'s `Spend` refuses for
  codex, for the reason #975 gives, and it is refused here too. Where a
  harness states no dollar figure, this record prints none.
- **Anything but claude.** Both arms are Claude Code. There is still no codex,
  Pi or Copilot spend data anywhere in the estate, so this says nothing about
  routing across harnesses.
- **Long lanes.** Ten turns. A lane that lives for a hundred is the case that
  would decide the memory question, and it is not this run.
- **Production-sized turns.** These turns are deliberately trivial, so what is
  priced is the dispatch mode rather than the work. The absolute dollar
  figures are floors, not typical turns: the estate's own ledger records
  $82.97 across 43 real turns, roughly $1.93 each, against $0.341 here.

### Against the production baseline, which is a different instrument

Live lanes were measured at **430–500MB per stateless worker** by reading
individual `claude` processes. This benchmark reads a **process tree** and got
637–646MB for the same arm. The gap is method, not regression — do not read
the two as one series. Within this run, both arms are measured the same way,
which is what makes the arms comparable to each other.

## The decision this supports

**Stateless subprocess dispatch stays the default, on memory grounds alone.**
Not on cost, and not on speed — the persistent arm won both, by 2.2× and 2.0×
respectively on this workload. It stays because a stateless worker's memory is
returned at exit and a persistent lane's is not, and because this host has
already been taken down twice by exhausting exactly that resource. A cap of 6
against a flat ~640MB is a bounded 3.8GB; the same cap against a footprint
that rises every turn is not a bound at all.

**The cost argument for stateless dispatch should stop being made.** It is
about 2.2× more expensive per turn than the alternative on this workload, and
the reason usually given for preferring it — that a persistent lane's cache
decays — is measurably false over ten turns.

That leaves persistence as a real option that has to buy its memory back:
bounded lane lifetimes, a lane budget separate from the in-flight cap, or
`claude -p --resume` (a fresh process per turn that reloads the conversation)
as a third mode nobody has measured. Those are open, and none of them is
decided here.

## The harness, and why it is safe to run

`src/estate/cmd/dispatchbench`. It is a measuring instrument, not part of the
daemon; `estate` does not call it, and must not — it spends real money and
loads the host, so a person runs it deliberately.

The first attempt at this measurement was told in prose to run one worker at a
time and to stop below a memory floor. It did neither: two benchmark binaries
ran concurrently, the larger reaching 1753MB, and the host's swap file grew
from 1024MB to 29696MB before the operator stopped it by hand. So both rules
are mechanisms here, and each one has a test that fails when it is removed:

| rule | mechanism | test that dies without it |
|---|---|---|
| one benchmark process per host | `runLock`, an `O_EXCL` pid file, stale only when `kill(pid, 0)` says the holder is gone | `TestSecondBenchmarkProcessCannotStart` — two real processes, not two goroutines |
| one turn at a time inside it | `serial`, which *refuses* a second concurrent caller rather than queueing it | `TestSerialAdmitsExactlyOneTurnAtATime`, with `TestSerialWithoutItsGateWouldOverlap` proving the goroutines genuinely race |
| the floor is checked by the harness, never the worker | `monitor` samples `pressure.Host` on its own goroutine and holds the run's `context.CancelFunc` | `TestPreflightRefusesBelowAnImpossibleFloor` |
| a worker is a tree, not a process | `subtreeRSSMB` | `TestSubtreeRSSSumsTheWholeTree` |
| tmux never reaches the operator's server | `assertIsolated` + a six-verb allowlist | `TestLaneRefusesToAddressAnUnisolatedSocket`, `TestLaneRefusesVerbsOutsideItsAllowlist` |

All five mutants were run and all five failed their tests before the real run;
the restored tree is green.

**Thresholds used, stricter than the dispatch gate's on purpose** — the gate's
job is to refuse the last turn that would hurt, this benchmark's is to stop
long before that: abort below **2048MB** free (the gate's floor is 512MB), at
or above **1 swapout per 2s sample**, or above **2000MB** in any one worker
tree. None fired. The run's worst readings were 6543MB free and zero swapouts.

`pressure.Host` was added to `internal/pressure` for this: it applies the
host's own three limits and none of the estate's, so a caller that is already
the in-flight turn is not refused for existing and does not go and build a
second reader of `vm_stat`. `Check` now calls the same code, so the gate and
this harness cannot drift into two readings of one host.

### One instrument defect found and fixed, which nearly became the headline

The session transcript writes **one record per content block of the same API
response**, each repeating the whole response's usage. Summed naively, the
first cross-check read 127,667 cache-read tokens for a turn whose own
`claude -p` envelope said 80,497 — a 59% overstatement that would have gone
straight into this document. Responses are now deduplicated on `message.id`,
and the deduplicated reader matches that envelope exactly on all four token
counts. That is what licenses using the transcript for the persistent arm,
where no envelope exists to check it against.

## Cleanup

Every scratch directory, tmux socket and session transcript this run created
was removed; `~/.claude/projects` carries no `dispatchbench-*` directory and
`/tmp` no `dbench-*` socket dir. The harness removes them itself unless
`-keep` is passed, which it was for this run precisely so the transcript could
be reconciled against `/cost` afterwards.
