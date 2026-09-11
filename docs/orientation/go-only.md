# The implementation language is Go. This is checked, not trusted.

*Relocated verbatim from the repo root `AGENTS.md` by the progressive-disclosure split (only heading levels and relative link paths were adjusted for the new location). `AGENTS.md` is the index that routes here; this file is the detail.*

**The app is Go.** Shell and Python are not an implementation option here, at
any size, for any reason, including "just this one script" and "only for
delivery".

`reference/` holds the deleted shell and Python supervisor, kept so an agent can
read how a rule was once encoded. Most of it is **reference material, not a
codebase**: 50 of the 57 `.py` files directly under
`reference/scripts/supervisor/` — the tmux/dispatch-orchestration supervisor
(`recycle.py`, `sensor.py`, `github_source.py`, `ci_gate.py`, the
`reconcile_*.py`/`cli*.py`/`transport*.py` family and the rest) — have had no
commit since the 2026-08-30 archive move, confirmed by `git log` on every one
of the 57 files individually, not assumed. Ten of those 50 are still part of
the live subset below, composed via mixin import rather than individually
edited since; the other 40 are neither touched nor reachable from anything
live. Recovering a rule from that fully-retired 40 means reimplementing it in
Go, not calling the script.

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
reimplemented rather than read.

This is guidance, not a gate. A CI blocker on new shell or Python was tried and
removed on 2026-09-02: it was an over-extreme reading of the operator's intent,
and a hard block can wedge an agent that legitimately needs a script for
tooling, a sandbox, or an experiment. The intent is narrow and stands — the
APP is not built out of shell and Python. Scripts elsewhere are unremarked.

**Why this is a guard and not a paragraph.** The directive that the supervisor
is Go was recorded on 2026-08-22. Its named target was later archived, the rule
was left pointing at nothing, and it silently stopped binding — a month of work
went into growing the layer it ruled out. A rule nothing checks is a preference.

**Before starting any task**, check it against the standing directives. If the
task extends something ruled out, stop and say so rather than doing it well.
Never open an issue against a layer scheduled for deletion. "Merged" is not
"delivered" — report what a human can now do that they could not before.
