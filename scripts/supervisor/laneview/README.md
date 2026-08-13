# The laneview adapter contract

agent-dotfiles#178: Jon wants a tmux plugin that can be a meta-harness,
"working together and apart" with the headless supervisor. #197 established
that a tmux sidebar can render lane state, not just window names; #173
measured that on a real fixture and left a working bridge to one candidate
(OpenSessions). This directory is the seam #178 asked for: the interface
between "here is lane state" and "here is how a human sees it", with more
than one implementation behind it, so neither can become required by the
other.

## The interface

A renderer is a script `laneview/<name>.sh`, invoked by `../laneview.sh` as:

```
<name>.sh <session> <lanes.sh --json output>
```

It must:

1. **Read, never write.** The only state a renderer may treat as ground
   truth is the json it was handed — which is itself `lanes.sh --json`,
   the one reader of tmux's measurements and the ledger (`AGENTS.md`,
   "tmux is not a database"). A renderer must never decide "this lane is
   free" and act on that decision (dispatch, claim, rename a window) —
   that is `dispatch.sh` and `claim.sh`'s job, not a viewer's.
2. **Degrade to absence, not to staleness.** If a renderer's own backend
   (a daemon, an HTTP endpoint) is unreachable, it must say so and exit
   nonzero — never show the last state it had as if it were current.
   *Partly* unreachable counts: review of #231 measured `opensessions.sh`
   pushing two of three lanes, leaving the third's tile untouched, and
   still exiting 0 with a success line. A renderer that cannot complete a
   render must overwrite whatever it can reach with a marker saying so,
   and exit nonzero.
3. **Cost nothing when unused.** No renderer may be sourced by, or run
   as a dependency of, `dispatch.sh`, `watchdog.sh`, `notify.sh`, or any
   other headless supervisor script. `laneview.sh` is only ever invoked by
   a human or a human-facing process (a tmux plugin, an interactive
   shell).
4. **Name every state, and never let an unnamed one read as healthy.**
   `lanes.sh` ships eleven states today. A renderer that maps the ones it
   recognizes and defaults the rest is not neutral about the others —
   review of #231 measured `scrolled`, a lane `dispatch.sh` will refuse to
   use, drawn in the sidebar as a green idle tick because it fell through
   `*) echo idle`. `validate_laneview_state_maps` in
   `scripts/validate_repository.py` reads each renderer's map and **errors**
   if `lanes.sh` grows a state the map does not name. A renderer with no
   map that check can read gets a warning saying it is unchecked, so one
   that legitimately has none stays mergeable and stays visible.

That is the whole contract. It buys the two guarantees #178 asked to be
demonstrated:

- **Apart:** delete every file under `laneview/` and `laneview.sh` itself
  (and `laneview-plugin-tmux/`, if that's gone too). Nothing else in
  `scripts/supervisor/` references this directory (verify with `grep -rl
  laneview scripts/supervisor --include='*.sh' | grep -v
  ^scripts/supervisor/laneview`) — dispatch, merge, and notify are
  unaffected. The verification surface holds too: `tests/supervisor/
  test_laneview.sh`, `test_laneview_tmux_plugin.sh`, and
  `test_laneview_tui_interactive.sh` each check for their subject before
  doing anything else and print a `SKIP` line and exit 0 when it is
  missing, rather than failing on a bare "No such file or directory" —
  so `tests/supervisor/test_shell_suites.py`'s repo-wide discovery stays
  green through the deletion instead of needing to be told which test
  files to remove.
- **Together:** stop every supervisor process (`dispatch.sh`,
  `watchdog.sh`, the cron/launchd driver) and run `laneview.sh <impl>`
  directly. It still renders, because it reads `lanes.sh --json`, which
  reads tmux and the ledger directly — none of it is supervisor state.

## Implementations shipped here

| file | renders as | cost | source of #178's "apart" vs "together" evidence |
|---|---|---|---|
| `text.sh` | one line per lane to stdout | none — no daemon, no plugin | proves "apart": works in a bare shell, in cron, over SSH, with the supervisor never running |
| `opensessions.sh` | a tmux sidebar pane, via OpenSessions' `/api/agent-event` + `/set-status` HTTP API | a Rust daemon + sidebar client per tmux client (TPM-installed plugin) | proves "together": the tmux-plugin path #173 measured live, unchanged in mechanism from `lanebridge.sh` |
| `tui.sh` | a curses screen: a `digest.sh --json` header line, one line per lane below it, selectable, `enter` jumps to it | none when not running — no daemon, plain Python stdlib (`curses`); one extra subprocess (`digest.sh --json`) per refresh tick | jonhill90/agent-supervisor#7's "a TUI he owns" — no third-party program, unlike `opensessions.sh`; the header is agent-dotfiles#67 |

### Where the two deliberately disagree: the supervisor row

`text.sh` prints it (`. arch  supervisor`); `opensessions.sh` drops it
(`if r["state"] == "supervisor": continue`). #4 asked for a choice --
document the difference or make them agree -- and the choice is **keep them
different**, because the difference is the renderers' vocabularies, not an
oversight:

- `text.sh` prints `lanes.sh`'s state name verbatim beside every glyph, so
  `supervisor` is a thing it can say. A human reading that line is told
  exactly what the window is, and the "never a dispatch target" fact is
  carried by the word itself. Dropping the row would hide a window the
  reader can see in tmux.
- `opensessions.sh` renders into OpenSessions' fixed `AgentStatus`
  vocabulary -- idle/running/stale/waiting/error -- which has no value
  meaning "not a lane". Every value it *could* pick is a claim about an
  agent, and the honest-looking one, `idle`, means "waiting for work" in a
  sidebar whose purpose is showing which agents are free. The supervisor
  pane is the one window a dispatch must never reach, so drawing it as
  available is rule 4's defect with a different cause. Omitting the row is
  rule 2 applied to a single tile: absence rather than a wrong claim.

The rule that generalizes: a renderer whose vocabulary can *name* a state
shows it; a renderer that would have to translate it into a claim it does
not mean omits it and says why here.

Making them agree would have retired one oddity -- `opensessions.sh`'s
`supervisor) echo idle` arm is unreachable while the filter stands. That is
a real argument and it loses to the above: the filter and the map arm guard
different things (the filter is upstream of the map; if it is ever removed
the arm is the only thing keeping `supervisor` off the `*)` stale path), so
the arm stays and is now commented as deliberate. `tests/supervisor/
test_laneview.sh` asserts the drop, so the divergence is checked and not
merely described.

Removing any is a deletion of its one file *under `scripts/`*. No
implementation imports from another, and `laneview.sh` does not
special-case any name — it re-enumerates this directory, so deleting
`text.sh` leaves `opensessions` and `tui` resolving and needs no edit in
`scripts/`.

`tui.sh` needs a real tty to draw a curses screen, which no headless caller
(cron, this repo's own tests, `laneview.sh tui session > file`) has. Rather
than fail there, it renders one static frame — the same shape `text.sh`
uses — and exits, so rule 2 (never crash into a traceback, never show
staleness) holds headlessly too, and the renderer stays testable the same
way `text.sh` is: call it directly with canned json and no tty.

The interactive path (selection, jump-to-lane) is a different code path
from that static frame and needs its own coverage — `tests/supervisor/
test_laneview_tui_interactive.sh` drives it inside a real isolated tmux
pane rather than skip it. It exists because review caught a real bug this
way that a static-frame test alone could not: an earlier version fed the
Python source to `python3 -` over a heredoc, which consumes stdin, so
curses had no terminal left to read keys from — it rendered correctly
(rendering only needs stdout) and silently dropped every keystroke. The
fix writes the source to a temp file first so stdin stays the tty.

### The tmux plugin

`../laneview-plugin-tmux/` is jonhill90/agent-supervisor#7's other ask, "a
tmux plugin". It is not a fourth entry in the table above: it is a
*launcher* that binds a tmux key to a popup running one of the
implementations above (`tui.sh` by default, configurable). See its own
README for install, keys, and the together/apart evidence for the plugin
itself — deleting it is independent of deleting any renderer here, and
deleting `tui.sh` degrades the plugin to `laneview.sh`'s own "no renderer"
error rather than breaking it silently.

One caveat, measured in review of #231 rather than assumed: the earlier
form of this claim was verified with a grep scoped to `scripts/supervisor`
and then stated as if it covered the tree. It does not.
`tests/supervisor/test_laneview.sh` names `text.sh` directly — deleting
`text.sh` fails **five** of its nineteen cases, measured on this change:

```
$ rm scripts/supervisor/laneview/text.sh && bash tests/supervisor/test_laneview.sh
  ... 14 passed, 5 failed
```

That is deliberate and is not coupling between implementations: the "apart"
guarantee is a property of `text.sh` specifically (it renders with no tmux
binary and no daemon reachable), so the test proving it has to name it.
Deleting a renderer means deleting its file and the cases asserting its own
behaviour. No other renderer, no supervisor script, and no check changes:
`validate_laneview_state_maps` returns nothing when this directory is
absent.

**This figure said "four" from the pre-split port (`4239313`) until #4
corrected it, and re-measure it when you touch this suite.** Four was right
when it was written; PR #12 (the two shipped viewers) and PR #69 (the
`digest.sh --json` header) each added cases exercising the full
`laneview.sh text` path without anyone re-running the deletion to check
whether the count still held. It read as measured and had quietly stopped
being one — the same drift `agent-dotfiles#246` found in this same
paragraph before the split.

## What #173 already measured about the tmux-sidebar path specifically

Carried here so it is not re-derived: OpenSessions genuinely renders lane
state (not just window names) through its extension API, at the cost of a
fixed-width pane injected into *every* tmux window — which, in the
measured run, narrowed unviewed lane panes enough to flip two lanes'
`lanes.sh` classification from `free`/`menu-blocked` to `unknown`. That is
a real cost of the *tmux-plugin* implementation, not of this adapter: the
`text.sh` implementation has no pane-width effect at all, because it does
not touch tmux's layout. See #173's second and third comments for the full
measurement, including the `window-size manual` mitigation.
