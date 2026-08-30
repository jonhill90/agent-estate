import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import cli  # noqa: E402
from core import Ledger  # noqa: E402
from tests.supervisor.test_cli_helpers import FakeMetadataTransport  # noqa: E402


class LaneFreeTest(unittest.TestCase):
    """agent-dotfiles#174: the read side of the seam #140 opened.
    `dispatch.sh` calls `cli.py lane-free` once per idle-looking candidate
    instead of trusting the window name -- these exercise `lane_free` (the
    function `main` dispatches to) directly, without a real tmux."""

    def test_a_lane_the_ledger_already_knows_and_the_live_pane_agrees_reads_free(self):
        """agent-dotfiles#819: a known+free lane now costs one tmux read to
        cross-check its recorded harness against the live pane -- unlike the
        pre-#819 shape, which trusted the row outright and never touched
        tmux for this branch. A matching pane must still read free."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="t:3", pane_id="%3", nonce="n", harness="claude", repo="/repo",
                server_id="server", session_id="$0", command="claude.exe",
            )
            transport = FakeMetadataTransport(
                {"pane_id": "%3", "command": "claude.exe", "path": "/repo", "server_id": "server", "session_id": "$0"},
                options={cli.HARNESS_OPTION: "claude"},
            )

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="ad999-some-task")

            self.assertEqual(
                {"lane": "t:3", "known": True, "free": True, "backfilled": False, "harness": "claude"}, result
            )

    def test_a_known_lanes_stale_harness_row_refuses_instead_of_misrouting(self):
        """agent-dotfiles#819's own reproduction: `agent-dotfiles:2` was
        re-registered as a codex lane -- a live `codex` process running --
        but its `lanes.harness` row was still `claude`, left by the slot's
        previous claude occupant. MUTATION-CHECK, direction one: deliberately
        create that drift (register claude, then put codex live in the pane)
        and confirm this fires -- a stale row must never reach dispatch as a
        trustworthy `"harness"` value."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="t:2", pane_id="%2", nonce="n", harness="claude", repo="/repo",
                server_id="server", session_id="$0", command="claude.exe",
            )
            transport = FakeMetadataTransport(
                {"pane_id": "%2", "command": "codex", "path": "/repo", "server_id": "server", "session_id": "$0"},
                options={cli.HARNESS_OPTION: "codex"},
            )

            result = cli.lane_free(ledger, transport, lane="t:2", target="t:2", window_name="ad999-some-task")

            self.assertFalse(result["free"])
            self.assertFalse(result["known"])
            self.assertIn("claude", result.get("reason", ""))
            self.assertIn("codex", result.get("reason", ""))
            # No self-correction (agent-dotfiles#819's refuse-vs-self-correct
            # decision): the stale row is left exactly as it was, for a
            # human/`cli.py register` to repair deliberately.
            self.assertEqual("claude", ledger.get_lane("t:2")["harness"])

    def test_a_known_lanes_harness_row_matching_the_live_pane_passes(self):
        """MUTATION-CHECK, direction two: remove the drift (re-register the
        SAME lane under the harness the pane now actually runs) and confirm
        the check passes again."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="t:2", pane_id="%2", nonce="n", harness="codex", repo="/repo",
                server_id="server", session_id="$0", command="codex",
            )
            transport = FakeMetadataTransport(
                {"pane_id": "%2", "command": "codex", "path": "/repo", "server_id": "server", "session_id": "$0"},
                options={cli.HARNESS_OPTION: "codex"},
            )

            result = cli.lane_free(ledger, transport, lane="t:2", target="t:2", window_name="ad999-some-task")

            self.assertEqual(
                {"lane": "t:2", "known": True, "free": True, "backfilled": False, "harness": "codex"}, result
            )

    def test_an_occupied_lane_is_not_free_regardless_of_its_current_window_name(self):
        """THE INVERSION: a window renamed to `free-N` by hand must not
        change what the ledger already knows."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            ledger.register_lane(
                lane="t:3", pane_id="%3", nonce="n", harness="claude", repo="/repo",
                server_id="server", session_id="$0", command="claude.exe",
            )
            ledger.reconstruct_task(
                task_id="ad1-task", source_kind="issue",
                source_url="https://github.com/acme/repo/issues/1", source_ref="1",
                summary="in flight", source_state="OPEN", status="created", evidence=["seed"], status_marker=None,
            )
            ledger.assign(task_id="ad1-task", lane="t:3", pane_nonce="n", summary="in flight")
            transport = FakeMetadataTransport({})

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="free-3")

            self.assertFalse(result["free"])
            self.assertEqual([], transport.calls)

    def test_an_unknown_lane_named_free_n_is_backfilled_free_on_first_sight(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport(
                {"pane_id": "%3", "command": "claude.exe", "path": "/repo", "server_id": "server", "session_id": "$0"},
                options={cli.HARNESS_OPTION: "claude"},
            )

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="free-3")

            self.assertEqual(
                {"lane": "t:3", "known": True, "free": True, "backfilled": True, "harness": "claude"}, result
            )
            self.assertEqual(["t:3", ("t:3", cli.HARNESS_OPTION)], transport.calls)
            self.assertIsNotNone(ledger.get_lane("t:3"))
            self.assertTrue(ledger.lane_available("t:3"))

            # And the SECOND call for the same never-seen-again lane answers
            # from the ledger, not the name -- first sight only. It still
            # cross-checks the now-recorded harness against the live pane
            # (agent-dotfiles#819) -- one tmux read, same as the first call,
            # not a second registration.
            transport.calls.clear()
            second = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="free-3")
            self.assertEqual(
                {"lane": "t:3", "known": True, "free": True, "backfilled": False, "harness": "claude"}, second
            )
            self.assertEqual(["t:3", ("t:3", cli.HARNESS_OPTION)], transport.calls)

    def test_a_copilot_lane_is_backfilled_free_from_its_recorded_harness_option(self):
        """agent-dotfiles#216: the bug's own reproduction. `council-copilot`
        runs as `node` -- indistinguishable by process name from any other
        Node harness -- so this only works because the harness option is
        read as a recorded fact, not guessed from `command`."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport(
                {"pane_id": "%7", "command": "node", "path": "/repo", "server_id": "server", "session_id": "$0"},
                options={cli.HARNESS_OPTION: "copilot"},
            )

            result = cli.lane_free(ledger, transport, lane="t:7", target="t:7", window_name="free-7")

            self.assertEqual(
                {"lane": "t:7", "known": True, "free": True, "backfilled": True, "harness": "copilot"}, result
            )
            self.assertEqual("copilot", ledger.get_lane("t:7")["harness"])

    def test_a_codex_lane_running_a_binary_not_literally_named_codex_is_backfilled(self):
        """agent-dotfiles#216 acceptance: codex under a launcher (also `node`
        live, per the issue's window-8 measurement) must work too, once its
        harness is recorded."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport(
                {"pane_id": "%8", "command": "node", "path": "/repo", "server_id": "server", "session_id": "$0"},
                options={cli.HARNESS_OPTION: "codex"},
            )

            result = cli.lane_free(ledger, transport, lane="t:8", target="t:8", window_name="free-8")

            self.assertEqual(
                {"lane": "t:8", "known": True, "free": True, "backfilled": True, "harness": "codex"}, result
            )

    def test_an_unknown_lane_not_named_free_n_is_unknown_not_free(self):
        """Fail closed (agent-dotfiles#174): a lane this code cannot
        positively place is never offered -- the same posture `lanes.sh`'s
        own whitelist (#126) takes for pane state."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport({"command": "claude.exe"})

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="ad999-some-task")

            self.assertEqual({"lane": "t:3", "known": False, "free": False, "backfilled": False}, result)
            self.assertEqual([], transport.calls, "an ineligible name must not even probe tmux")
            self.assertIsNone(ledger.get_lane("t:3"))

    def test_an_unrecorded_harness_refuses_the_backfill_without_crashing(self):
        """The bug this exists for: #216's exact reproduction. A `node` pane
        with no `HARNESS_OPTION` set is unidentifiable and MUST stay refused
        -- this is the correct behaviour the issue itself names, not the
        defect. Only a positively recorded harness may pass."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport(
                {"pane_id": "%3", "command": "node", "path": "/repo", "server_id": "server", "session_id": "$0"}
            )

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="free-3")

            self.assertFalse(result["free"])
            self.assertIn("node", result.get("reason", ""))
            self.assertIsNone(ledger.get_lane("t:3"))

    def test_a_recorded_harness_the_live_pane_contradicts_refuses_the_backfill(self):
        """Mutation-check target (agent-dotfiles#216 acceptance): a pane
        recorded "claude" but actually running the process a copilot/codex
        lane runs (`node`) must NOT be trusted just because an option is
        present. If this refusal is ever weakened to trust any recorded
        value outright, this is the test that must go red."""
        with tempfile.TemporaryDirectory() as root:
            ledger = Ledger(Path(root))
            transport = FakeMetadataTransport(
                {"pane_id": "%3", "command": "node", "path": "/repo", "server_id": "server", "session_id": "$0"},
                options={cli.HARNESS_OPTION: "claude"},
            )

            result = cli.lane_free(ledger, transport, lane="t:3", target="t:3", window_name="free-3")

            self.assertFalse(result["free"])
            self.assertIn("claude", result.get("reason", ""))
            self.assertIn("node", result.get("reason", ""))
            self.assertIsNone(ledger.get_lane("t:3"))
