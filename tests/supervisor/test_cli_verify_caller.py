import contextlib
import io
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import cli  # noqa: E402
from core import Ledger  # noqa: E402
from tests.supervisor.test_cli_helpers import FakeMetadataTransport  # noqa: E402


class VerifyCallerCliTest(unittest.TestCase):
    """agent-supervisor#362: `accept`/`complete` both route through
    `_verify_caller`, and a claude-print lane has no tmux pane for the old
    `TMUX_PANE`-only check to authenticate against -- `TmuxAdapter.
    _verified_lane` raised "pane incarnation does not match registered lane"
    for every claude-print `accept`/`complete` unconditionally, because the
    caller was hardcoded to the bare `TmuxAdapter`, not `adapter_for_lane`.
    """

    def _register_claude_print_lane(self, ledger, *, lane, session_id, repo="/repo"):
        ledger.register_lane(
            lane=lane, pane_id=session_id, nonce=f"nonce-{lane}", harness="claude",
            repo=repo, server_id="claude-print", session_id=session_id, command="claude",
            transport="claude-print",
        )

    def _deliver_task(self, ledger, *, lane, task_id):
        ledger.reconstruct_task(
            task_id=task_id, source_kind="issue",
            source_url=f"https://github.com/acme/repo/issues/{task_id}",
            source_ref=task_id, summary="do it", source_state="OPEN", status="created",
            evidence=[], status_marker=None,
        )
        ledger.assign(task_id=task_id, lane=lane, pane_nonce=f"nonce-{lane}", summary="do it")
        ledger.mark_delivery_pending(task_id, pane_nonce=f"nonce-{lane}")
        ledger.mark_delivered(task_id, pane_nonce=f"nonce-{lane}")

    def test_a_claude_print_lane_completing_its_own_task_succeeds(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            self._register_claude_print_lane(ledger, lane="cp-worker", session_id="sess-own")
            self._deliver_task(ledger, lane="cp-worker", task_id="t1")

            with patch.dict(os.environ, {"CLAUDE_CODE_SESSION_ID": "sess-own"}, clear=False):
                output = io.StringIO()
                with contextlib.redirect_stdout(output):
                    self.assertEqual(
                        0, cli.main(["--state-dir", root, "accept", "--task", "t1"])
                    )
                self.assertIsNotNone(json.loads(output.getvalue())["accepted_at"])

                result_file = Path(root) / "result.md"
                result_file.write_text("done")
                output = io.StringIO()
                with contextlib.redirect_stdout(output):
                    self.assertEqual(
                        0,
                        cli.main([
                            "--state-dir", root, "complete", "--task", "t1",
                            "--result-file", str(result_file),
                        ]),
                    )
                value = json.loads(output.getvalue())
                self.assertEqual("complete", value["status"])
                self.assertIsNotNone(value["completed_at"])

    def test_a_claude_print_lane_completing_another_lanes_task_is_refused(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            self._register_claude_print_lane(ledger, lane="cp-owner", session_id="sess-owner")
            self._register_claude_print_lane(ledger, lane="cp-impostor", session_id="sess-impostor")
            self._deliver_task(ledger, lane="cp-owner", task_id="t-owner")

            with patch.dict(os.environ, {"CLAUDE_CODE_SESSION_ID": "sess-impostor"}, clear=False):
                with self.assertRaises(RuntimeError):
                    cli.main(["--state-dir", root, "accept", "--task", "t-owner"])
            ledger = Ledger(Path(root))
            self.assertEqual("delivered", ledger.get_task("t-owner")["status"])

    def test_a_tmux_lane_completing_another_lanes_task_is_still_refused(self):
        """The pane path, exactly as before this fix -- (2) on the tmux
        transport, so the fix did not weaken the existing send-keys guard
        while adding the claude-print one. `TmuxTransport` is faked so the
        pane's own metadata matches the ledger's registration exactly --
        `_verified_lane`'s identity check passes -- isolating the assertion
        to `_verify_caller`'s `TMUX_PANE` mismatch, the thing #362 touched."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="tmux-owner", pane_id="%1", nonce="nonce-tmux-owner", harness="claude",
                repo="/repo", server_id="server", session_id="$1", command="claude",
                transport="send-keys",
            )
            ledger.reconstruct_task(
                task_id="t-owner", source_kind="issue",
                source_url="https://github.com/acme/repo/issues/1", source_ref="1",
                summary="do it", source_state="OPEN", status="created",
                evidence=[], status_marker=None,
            )
            ledger.assign(task_id="t-owner", lane="tmux-owner", pane_nonce="nonce-tmux-owner", summary="do it")
            ledger.mark_delivery_pending("t-owner", pane_nonce="nonce-tmux-owner")
            ledger.mark_delivered("t-owner", pane_nonce="nonce-tmux-owner")

            fake_transport = FakeMetadataTransport(
                {
                    "pane_id": "%1", "command": "claude", "path": "/repo",
                    "server_id": "server", "session_id": "$1",
                },
                options={"@hill90_lane_nonce": "nonce-tmux-owner", "@hill90_lane": "tmux-owner"},
            )
            with patch.object(cli, "TmuxTransport", lambda *_a, **_k: fake_transport), \
                 patch.dict(os.environ, {"TMUX_PANE": "%2"}, clear=False):
                with self.assertRaises(RuntimeError):
                    cli.main(["--state-dir", root, "accept", "--task", "t-owner"])
            ledger = Ledger(Path(root))
            self.assertEqual("delivered", ledger.get_task("t-owner")["status"])
