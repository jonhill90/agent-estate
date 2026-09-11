# laneview tmux plugin

The "tmux plugin" half of jonhill90/agent-supervisor#7 (agent-dotfiles#178):
a key, bound in tmux, that opens a popup showing live lane state -- Jon's
own, not a third-party program (that is `laneview/opensessions.sh`).

It is a **launcher**, not a second implementation of the contract in
`../laneview/README.md`. All it does is `tmux bind-key` a popup that runs
`laneview.sh <impl> <session>` -- the same command typed by hand gets. The
actual rendering is whichever `laneview/<impl>.sh` script that invokes
(`tui.sh` by default).

## Install

Never edits `~/.tmux.conf` (standing constraint). Add this line yourself,
pointing at wherever this repo is checked out:

```tmux
run-shell '/path/to/agent-supervisor/scripts/supervisor/laneview-plugin-tmux/laneview.tmux'
```

If installing through TPM instead, point `@plugin` at this repo and this
directory serves as the plugin root (`laneview.tmux` is the file TPM looks
for).

Reload tmux config (`prefix + :` `source-file ~/.tmux.conf`, or restart the
server) after adding the line.

## Use

`prefix + L` opens a popup rendering the current session's lanes. `q`
closes it. Arrow keys / `j`/`k` move the selection, `Enter` jumps to that
lane (`select-window` + `switch-client` -- navigation, never a decision:
this plugin and the renderer it launches never claim a lane, dispatch to
one, or rename a window).

## Configure

Two `tmux` options, both optional:

- `@laneview-key` — the key to bind (default `L`).
- `@laneview-impl` — which `laneview/<impl>.sh` to run (default `tui`).
  Set to `text` to use the plain-stdout renderer instead, or to any other
  implementation added under `../laneview/`.

```tmux
set -g @laneview-key 'v'
set -g @laneview-impl 'text'
```

## Together and apart

- **Apart:** `rm -rf` this directory. `grep -rl laneview-plugin-tmux` from
  the repo root returns 3 hits, not 1 (corrected 2026-09-11,
  agent-estate#1397) -- this file, `reference/tests-supervisor/
  test_laneview_tmux_plugin.sh`, and `../laneview/README.md`'s own
  cross-reference to this directory. Neither of the other two is a
  dependency: the test's own first check is `[ ! -d ... ] && SKIP exit 0`
  -- it degrades, not fails, when this directory is gone, the same
  pattern `../laneview/README.md` documents for its own suite -- and the
  `laneview/README.md` mention is prose, not code that reads this
  directory. So the practical claim still holds -- no key is bound, no
  popup exists, and the headless supervisor (`dispatch.sh`, `watchdog.sh`,
  `notify.sh`) never had a dependency on it to lose -- only the specific
  "returns only this directory's own files" sentence was wrong, and is
  removed rather than repeated.
- **Together:** the popup renders live lane state without the supervisor
  ever running, because the renderer it launches reads only `lanes.sh
  --json`, which reads tmux and the ledger directly.

## Cost when unused

Nothing. `laneview.tmux` runs once, at tmux start (or whenever the config
line is sourced), and only calls `tmux bind-key`. No daemon, no timer, no
pane injected into any window -- the fixed-width-sidebar cost `#173`
measured against OpenSessions does not apply here, because this plugin
never modifies the tmux layout. The popup exists only between the keypress
and `q`.
