#!/usr/bin/env python3
"""Is a lane's REGISTRATION still true of the live tmux server it names?

agent-supervisor#520 (and the false-pass half of #513).

WHAT THE INDEPENDENCE GATE ACTUALLY TRUSTS, and why that was thin.
`verdict-independence.sh` decides "was this PR reviewed by someone other than
its author" by comparing `lanes.pane_id` for the two lane ids -- the author's
against the one hand-stamped in the PR comment's `Review-Lane:` trailer (see
`resolve_lane_relation` / `core.lane_relation_from_rows`). Two rows with
different `pane_id` read as positively DIFFERENT lanes, and that is the only
thing standing between a real independent review and a self-merge.

Nothing on that path ever asked whether either row is still TRUE. `pane_id`,
`server_id` and `session_id` are written once at registration and never
reconciled against tmux again, so:

  * a row registered against a tmux server that has since died names a pane
    that no longer exists, and still reads as a perfectly good identity;
  * a row registered by hand with values nobody measured (the shape
    agent-supervisor#520's own brief warns about, at registration time
    rather than lookup time) is indistinguishable from a measured one.

Measured on this estate on 2026-08-23, which is why this module exists: all
four of `estate:2`..`estate:5` carried `lanes` rows whose `pane_id` (`%38`,
`%39`, `%51`, `%52`) named no pane on the running tmux server (whose panes
were `%1`..`%5`), whose `repo` named a different checkout than the panes'
actual cwd, and whose `server_id` named a session created the previous day.
Both `post-verdict.sh` (which only asks `get_lane(...) is not None`) and
`merge-pr.sh` accepted every one of them as a valid reviewer identity.

An instrument that cannot see a thing looks exactly like the thing being
absent -- this repo's own most-produced failure mode. A registration nobody
re-measures looks exactly like a registration that is correct.

THREE ANSWERS, never two:

  verified      the ledger's row is corroborated by the live server it names:
                that server is reachable, the recorded pane exists on it, that
                pane's own `<session>:<index>` is this lane id, and the pane
                belongs to the same server incarnation the row recorded.
  contradicted  the server the row names IS reachable and disagrees with the
                row -- the pane is gone, or it is a different lane now, or the
                session is a later incarnation than the one registered. This
                is a positive finding of staleness/fabrication, and callers
                must treat it as a refusal.
  unverifiable  nothing could be checked: no row, an off-pane transport
                (`claude-print`/`pi-rpc`/`acp` have no tmux pane by
                construction), no tmux binary, or a socket that is not there
                at all. NOT a pass and NOT a failure -- the honest third
                answer. Each caller decides; see `verdict-independence.sh`
                for the choice made there and why.

THE LIMIT, stated rather than left implicit. This proves a lane id names a
live pane the ledger really registered. It does NOT prove the agent that
POSTED a verdict is the agent in that pane -- under this estate's single
shared GitHub account, a `Review-Lane:` trailer is an unattested claim, and
no check reachable from the merge path can tell an honest stamp from a lane
typing a sibling's id. `register-lane-self.sh` narrows that by making the
registration itself observation-only; it does not close it.

READ-ONLY, always. Every tmux verb here is `list-panes` / `display-message`;
nothing is created, killed or respawned, so invariant 4's `assert_isolated_
tmux` requirement (destructive verbs and session creation) does not apply
and no test of this module needs an isolated socket to be SAFE -- though the
tests still use one, because they need a server whose contents they control.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from core import Ledger  # noqa: E402

# Transports with no tmux pane by construction -- `adapter.ACPAdapter`,
# `PiRPCAdapter` and `ClaudePrintAdapter` all synthesise a `pane_id` from a
# session id (`claude-print:<lane>`, an ACP/pi session id). There is nothing
# on a tmux server to look for, so the answer is `unverifiable`, never
# `contradicted`.
PANE_TRANSPORTS = ("send-keys",)

TMUX_TIMEOUT_SECONDS = 10


def _socket_path(server_id):
    """The socket half of `transport.TmuxTransport.metadata`'s `server_id`,
    which is `f"{socket_path}:{session_created}"`. Split on the LAST colon:
    a socket path may legitimately contain one, a `session_created` epoch
    never does."""
    if not isinstance(server_id, str) or ":" not in server_id:
        return None
    socket_path, _, created = server_id.rpartition(":")
    if not socket_path or not created:
        return None
    return socket_path


def _list_panes(tmux_bin, socket_path, runner):
    fmt = "#{pane_id}\t#{session_name}:#{window_index}\t#{socket_path}:#{session_created}"
    return runner([tmux_bin, "-S", socket_path, "list-panes", "-a", "-F", fmt])


def _subprocess_runner(command):
    return subprocess.run(
        command, check=True, capture_output=True, text=True, timeout=TMUX_TIMEOUT_SECONDS
    ).stdout


def check(lane, *, ledger, tmux_bin="tmux", runner=None):
    """`{"lane", "status", "detail", "pane_id"}` -- see the module docstring
    for what each status means. Never raises for an ordinary failure: an
    unreachable server, a missing binary and a malformed row are all
    `unverifiable` with a reason, because a caller that gates a merge on this
    must never be handed an exception it could accidentally treat as a pass."""
    runner = runner or _subprocess_runner

    def out(status, detail, pane_id=""):
        return {"lane": lane, "status": status, "detail": detail, "pane_id": pane_id}

    try:
        row = ledger.get_lane(lane)
    except Exception as error:  # a wedged/absent ledger is not a pass
        return out("unverifiable", f"could not read the ledger: {error}")
    if row is None:
        return out("unverifiable", f"lane {lane} is not registered in this ledger")

    transport = (row.get("transport") or "").strip()
    pane_id = (row.get("pane_id") or "").strip()
    if transport not in PANE_TRANSPORTS:
        return out(
            "unverifiable",
            f"lane {lane} is registered transport {transport or 'unset'!r}, which has no tmux pane to check",
            pane_id,
        )
    if not pane_id:
        return out("unverifiable", f"lane {lane} has no pane_id recorded", pane_id)

    server_id = (row.get("server_id") or "").strip()
    socket_path = _socket_path(server_id)
    if socket_path is None:
        return out(
            "unverifiable",
            f"lane {lane} records server_id {server_id!r}, which is not <socket>:<session_created>",
            pane_id,
        )
    if not Path(socket_path).exists():
        return out(
            "unverifiable",
            f"the tmux socket lane {lane} was registered against ({socket_path}) does not exist",
            pane_id,
        )

    try:
        listing = _list_panes(tmux_bin, socket_path, runner)
    except Exception as error:
        return out("unverifiable", f"could not list panes on {socket_path}: {error}", pane_id)

    panes = {}
    for line in listing.splitlines():
        parts = line.split("\t")
        if len(parts) != 3:
            continue
        panes[parts[0]] = (parts[1], parts[2])
    if not panes:
        return out("unverifiable", f"tmux on {socket_path} reported no panes at all", pane_id)

    if pane_id not in panes:
        return out(
            "contradicted",
            (
                f"lane {lane} is registered on pane {pane_id}, but no such pane exists on the "
                f"tmux server it names ({socket_path}) -- the registration describes a dead or "
                "never-observed incarnation"
            ),
            pane_id,
        )

    live_lane, live_server = panes[pane_id]
    if live_lane != lane:
        return out(
            "contradicted",
            (
                f"pane {pane_id} is live but is lane {live_lane} now, not {lane} -- a window "
                "renumber or a reused pane id means this lane id no longer names this pane"
            ),
            pane_id,
        )
    if live_server != server_id:
        return out(
            "contradicted",
            (
                f"pane {pane_id} is lane {lane} on a DIFFERENT server incarnation "
                f"({live_server}) than the one registered ({server_id}) -- the ids line up by "
                "coincidence, not because this is the process that was registered"
            ),
            pane_id,
        )
    return out("verified", f"lane {lane} is pane {pane_id}, live on {server_id}", pane_id)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--lane", required=True)
    parser.add_argument("--state-dir", required=True)
    parser.add_argument("--tmux-bin", default="tmux")
    args = parser.parse_args(argv)
    try:
        ledger = Ledger(args.state_dir)
    except Exception as error:
        print(json.dumps({"lane": args.lane, "status": "unverifiable", "detail": str(error), "pane_id": ""}))
        return 2
    result = check(args.lane, ledger=ledger, tmux_bin=args.tmux_bin)
    print(json.dumps(result))
    # Exit codes so a shell caller can branch without parsing: 0 verified,
    # 1 contradicted (a positive refusal), 2 unverifiable (nothing checked).
    return {"verified": 0, "contradicted": 1}.get(result["status"], 2)


if __name__ == "__main__":
    sys.exit(main())
