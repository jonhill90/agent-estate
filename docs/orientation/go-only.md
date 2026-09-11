# The implementation language is Go. This is checked, not trusted.

*Relocated verbatim from the repo root `AGENTS.md` by the progressive-disclosure split (only heading levels and relative link paths were adjusted for the new location). `AGENTS.md` is the index that routes here; this file is the detail.*

**The app is Go.** Shell and Python are not an implementation option for
`src/estate` or `src/tui`, at any size, for any reason, including "just this
one script" and "only for delivery".

**2026-09-11 — the rule was narrowed to the app, on Jon's own words
(agent-estate#1412).** He was asked whether `reference/tests-supervisor/`
(the retired shell supervisor's test tree) should be repaired, kept frozen,
or dropped. His answer narrowed the rule itself, not just that tree —
described here rather than quoted, since `text_clean` is NULL for the
source row (`hp-41cc3914d6d18816`); see
[the ADR](../decisions/2026-09-11-go-only-rule-narrowed-to-the-app.md) for
why publishing the exact wording matters more there, and what that costs:

He said either moving the retired test material elsewhere or dropping the
reference folder outright would be fine, and he wasn't certain which was
better; that the one thing that actually mattered was never writing the app
itself in shell and Python again, which he named as the original reason the
rule existed; that he now trusts the team enough to let scripts be written
and used as needed; and that whatever isn't needed should be deleted, since
old commits already carry the history.

Two things follow from that, and they are different in kind:

1. **The rule binds the app only.** Never writing the app itself in shell or
   Python again is, by his own account, the whole reason the rule existed in
   the first place. Helper scripts elsewhere in the repo — tooling, one-off checks, a
   sandbox, an experiment — may be written and used as needed. This was
   already true in practice (see "guidance, not a gate" below); it is now
   also true by direct instruction, not just by omission.
2. **This is not "anything goes."** The app itself — `src/estate`, `src/tui`
   — stays Go, with no exception. Do not read the relaxation as covering the
   app; it covers scripts *outside* it. If a task extends the app itself
   using shell or Python, that is still the exact failure this rule exists to
   catch.

The reasoning is recorded in full, including the deletion this same decision
authorised, in
[`docs/decisions/2026-09-11-go-only-rule-narrowed-to-the-app.md`](../decisions/2026-09-11-go-only-rule-narrowed-to-the-app.md).

## `reference/scripts/` is live infrastructure, not reference material

This was true before 2026-09-11 and the date above does not change it — it is
restated here because the directory's own name ("reference") suggests
otherwise, and that has misled agents before (agent-estate#1380).

`src/estate/internal/isolate` names `reference/scripts/supervisor/` by path
(its `__pycache__` allowlist entry and the dirty-worktree tests built around
it). `.claude/settings.json` registers
`reference/scripts/supervisor/prompt_capture_hook.py` as a live
`UserPromptSubmit` hook — it runs on every prompt, not just importable.

**One 17-file subset under `reference/scripts/supervisor/` is not inert
(agent-estate#1380).** `core.py`'s `Ledger` class and the 11 `core_ledger_*.py`
files it composes via mixin, plus `itemize_prompts.py`, `mine_prompts.py`,
`prompt_capture_hook.py`, `migrate_dead_capture_1357.py` and
`migrate_dead_items_1362.py` (five entry points that each construct a real
`Ledger(...)`), were touched by real, reviewed, tested PRs as recently as
2026-09-10, and most carry a dedicated file under
`reference/scripts/supervisor/tests/` (agent-estate#1412: moved there from
`reference/tests-supervisor/`, which no longer exists, on 2026-09-11).
`prompt_capture_hook.py` is genuinely **run**, not just importable: it is
registered as a live Claude Code hook in `.claude/settings.json`. This subset
is still outside the Go-only rule's scope above — the app is `src/estate`,
and this is corpus/capture tooling, not the app — but it is maintained,
tested, and fixed, so treat it accordingly rather than as dead material to be
reimplemented rather than read. Everything else under
`reference/scripts/supervisor/` — the tmux/dispatch-orchestration supervisor
proper (`recycle.py`, `sensor.py`, `github_source.py`, `mcp_server.py`,
`cli.py`, `adapter.py`, `verdict.py`, the `reconcile_*.py`/`lane_*_reap.py`
family and the rest, plus all `*.sh`) is retired: no commit since the
2026-08-30 archive move for most of it, and — confirmed directly in Go
source, `src/tui/internal/rail/model.go`'s own comment on `mcp_server.py` —
"no longer exists in this repository (only `reference/` — never maintained,
run, tested, or fixed — has it)". Recovering a rule from that retired
majority means reimplementing it in Go, not calling the script.

## `reference/tests-supervisor/` — disposition as of 2026-09-11

The retired-shell test tree that used to sit here is gone. What each file
tested, the per-file evidence for what moved versus what was deleted, and
why, is recorded once, in
[`docs/decisions/2026-09-11-go-only-rule-narrowed-to-the-app.md`](../decisions/2026-09-11-go-only-rule-narrowed-to-the-app.md#what-was-verified-before-executing-this-not-assumed-from-the-quote-alone) —
not repeated here. Short version: the suites that covered the live 17-file
subset moved to `reference/scripts/supervisor/tests/`, next to the code they
test; run them with `python3 -m pytest reference/scripts/supervisor/tests/
-q`. Everything else tested a retired subject and was deleted, on the same
"old commits are the record" basis the decision itself rests on.

## Guidance, not a gate

A CI blocker on new shell or Python was tried and removed on 2026-09-02: it
was an over-extreme reading of the operator's intent, and a hard block can
wedge an agent that legitimately needs a script for tooling, a sandbox, or an
experiment. The 2026-09-11 decision above made that reading's opposite
explicit rather than merely un-blocked: scripts elsewhere are not just
tolerated, they are Jon's stated preference for how supporting tooling gets
built — see the same source row above (`hp-41cc3914d6d18816`) for the source,
described rather than quoted. The intent
that is *not* relaxed is equally narrow and equally explicit: the APP is not
built out of shell and Python.

**Why this is a guard and not a paragraph.** The directive that the
supervisor is Go was recorded on 2026-08-22. Its named target was later
archived, the rule was left pointing at nothing, and it silently stopped
binding — a month of work went into growing the layer it ruled out. A rule
nothing checks is a preference.

**Before starting any task**, check it against the standing directives. If
the task extends something ruled out — writing or growing `src/estate` or
`src/tui` in shell or Python — stop and say so rather than doing it well.
Never open an issue against a layer scheduled for deletion. "Merged" is not
"delivered" — report what a human can now do that they could not before.
