import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class HarnessIdentityTest(LedgerTestBase):
    def test_codex_and_claude_lanes_share_schema_but_keep_adapter_identity(self):
        self.ledger.register_lane(
            lane="infra-claude",
            pane_id="%8",
            nonce="nonce-8-a",
            harness="claude",
            repo="/repo/hill90",
            server_id="server-a",
            session_id="$4",
            command="claude.exe",
        )
        codex_lane = self.ledger.get_lane("app-review")
        claude_lane = self.ledger.get_lane("infra-claude")
        self.assertEqual(set(codex_lane), set(claude_lane))
        self.assertEqual("codex", codex_lane["harness"])
        self.assertEqual("claude", claude_lane["harness"])
        self.assertNotEqual(codex_lane["nonce"], claude_lane["nonce"])

    def test_copilot_acp_is_a_registerable_harness(self):
        """Red: register_lane's Python-level check and the lanes table's CHECK
        constraint both still hard-code ('codex', 'claude') -- a copilot-acp
        lane cannot be registered at all, so ACPTransport has no lane to
        dispatch through."""
        lane = self.ledger.register_lane(
            lane="copilot-worker",
            pane_id="session-1",
            nonce="nonce-acp",
            harness="copilot-acp",
            repo="/repo/app",
            server_id="acp",
            session_id="session-1",
            command="copilot",
        )
        self.assertEqual("copilot-acp", lane["harness"])
        self.assertEqual("copilot-acp", self.ledger.get_lane("copilot-worker")["harness"])
