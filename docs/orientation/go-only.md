# The implementation language is Go. This is checked, not trusted.

*Relocated verbatim from the repo root `AGENTS.md` by the progressive-disclosure split (only heading levels and relative link paths were adjusted for the new location). `AGENTS.md` is the index that routes here; this file is the detail.*

**The app is Go.** Shell and Python are not an implementation option here, at
any size, for any reason, including "just this one script" and "only for
delivery".

`reference/` holds the deleted shell and Python supervisor, kept so an agent can
read how a rule was once encoded. It is **reference material, not a codebase**:
nothing there is maintained, run, tested, or fixed. Recovering a rule from it
means reimplementing that rule in Go, not calling the script.

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
