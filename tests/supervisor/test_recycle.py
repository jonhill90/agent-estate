import sys
import tempfile
import time
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from recycle import (  # noqa: E402
    ArmedChannel,
    ChannelsSectionMissing,
    ChannelsSectionUnparseable,
    EMPTY_CHANNELS_MARKER,
    decide_recycle,
    parse_armed_channels,
    respawn_supervisor,
)


# Mirrors the live brief's actual "## Live lanes and armed channels" format
# (a markdown table) rather than a shape invented to match the parser --
# see the format-mismatch defect this replaced.
CHANNELS_BLOCK = """
## Live lanes and armed channels

| Channel | Lane | Working from | Task |
|---|---|---|---|
| `infra-claude-tick` | `infra-claude` | `~/.local/state/agent-dotfiles-supervisor/brief.md` | implementing #47 recycle decision function |
| `skills-tick` | `skills` | `~/.local/state/agent-dotfiles-supervisor/brief.md` | rostering the loop-memory skill |

## Some other section

irrelevant body text
"""

# The real, deployed brief location this project reads from in production.
REAL_BRIEF_PATH = Path.home() / ".local" / "state" / "agent-dotfiles-supervisor" / "brief.md"


class FakeTransport:
    def __init__(self):
        self.calls = []

    def respawn_pane(self, target):
        self.calls.append(("respawn_pane", target))

    def send_literal(self, target, payload):
        self.calls.append(("send_literal", target, payload))


class DecideRecycleTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.brief_path = Path(self.tempdir.name) / "brief.md"

    def _write_brief(self, body):
        self.brief_path.write_text(body)

    def test_fresh_brief_and_old_enough_session_allows_recycle(self):
        self._write_brief("# Brief\n" + CHANNELS_BLOCK)
        now = 10_000.0
        decision = decide_recycle(
            brief_path=self.brief_path,
            session_started_at=now - 7200,
            now=now,
            max_session_age_seconds=3600,
            max_brief_staleness_seconds=1800,
        )
        self.assertTrue(decision.allowed)
        self.assertIn("recycle allowed", decision.reason)
        self.assertEqual(2, len(decision.channels))

    def test_stale_brief_is_refused_with_staleness_reason(self):
        self._write_brief("# Brief\n" + CHANNELS_BLOCK)
        old_time = time.time() - 7200
        import os

        os.utime(self.brief_path, (old_time, old_time))
        now = time.time()
        decision = decide_recycle(
            brief_path=self.brief_path,
            session_started_at=now - 7200,
            now=now,
            max_session_age_seconds=3600,
            max_brief_staleness_seconds=1800,
        )
        self.assertFalse(decision.allowed)
        self.assertIn("stale", decision.reason)
        self.assertIn(str(self.brief_path), decision.reason)

    def test_missing_brief_is_refused_with_absence_reason(self):
        missing_path = Path(self.tempdir.name) / "does-not-exist.md"
        decision = decide_recycle(
            brief_path=missing_path,
            session_started_at=0,
            now=10_000,
            max_session_age_seconds=3600,
            max_brief_staleness_seconds=1800,
        )
        self.assertFalse(decision.allowed)
        self.assertIn("missing", decision.reason)
        self.assertIn(str(missing_path), decision.reason)

    def test_young_session_is_not_recycled_with_age_reason(self):
        self._write_brief("# Brief\n" + CHANNELS_BLOCK)
        now = 10_000.0
        decision = decide_recycle(
            brief_path=self.brief_path,
            session_started_at=now - 60,
            now=now,
            max_session_age_seconds=3600,
            max_brief_staleness_seconds=1800,
        )
        self.assertFalse(decision.allowed)
        self.assertIn("too young", decision.reason)

    def test_missing_channels_section_refuses_rather_than_empty_list(self):
        self._write_brief("# Brief\n\nNo channels section here at all.\n")
        now = 10_000.0
        decision = decide_recycle(
            brief_path=self.brief_path,
            session_started_at=now - 7200,
            now=now,
            max_session_age_seconds=3600,
            max_brief_staleness_seconds=1800,
        )
        self.assertFalse(decision.allowed)
        self.assertIn("channels section missing", decision.reason)

    def test_unparseable_channels_section_refuses_rather_than_empty_list(self):
        # The heading exists -- ChannelsSectionMissing must not fire -- but
        # the body is neither a table nor the explicit empty marker. This is
        # the defect PR #48 was held for: a present-but-unreadable section
        # must not silently parse to "zero lanes running".
        self._write_brief(
            "# Brief\n\n## Live lanes and armed channels\n\n"
            "some free text that is not a table and not the empty marker\n\n"
            "## Next\n"
        )
        now = 10_000.0
        decision = decide_recycle(
            brief_path=self.brief_path,
            session_started_at=now - 7200,
            now=now,
            max_session_age_seconds=3600,
            max_brief_staleness_seconds=1800,
        )
        self.assertFalse(decision.allowed)
        self.assertIn("channels section unparseable", decision.reason)
        self.assertEqual((), decision.channels)

    def test_explicit_empty_marker_allows_recycle_with_zero_channels(self):
        self._write_brief(
            f"# Brief\n\n## Live lanes and armed channels\n\n{EMPTY_CHANNELS_MARKER}\n\n## Next\n"
        )
        now = 10_000.0
        decision = decide_recycle(
            brief_path=self.brief_path,
            session_started_at=now - 7200,
            now=now,
            max_session_age_seconds=3600,
            max_brief_staleness_seconds=1800,
        )
        self.assertTrue(decision.allowed)
        self.assertEqual((), decision.channels)


class ParseArmedChannelsTest(unittest.TestCase):
    def test_parses_each_row_into_an_armed_channel(self):
        channels = parse_armed_channels("# Brief\n" + CHANNELS_BLOCK)
        self.assertEqual(
            [
                ArmedChannel(
                    channel="infra-claude-tick",
                    lane="infra-claude",
                    brief="~/.local/state/agent-dotfiles-supervisor/brief.md",
                    description="implementing #47 recycle decision function",
                ),
                ArmedChannel(
                    channel="skills-tick",
                    lane="skills",
                    brief="~/.local/state/agent-dotfiles-supervisor/brief.md",
                    description="rostering the loop-memory skill",
                ),
            ],
            channels,
        )

    def test_explicit_empty_marker_is_a_legitimate_empty_list(self):
        text = (
            f"# Brief\n\n## Live lanes and armed channels\n\n{EMPTY_CHANNELS_MARKER}\n\n## Next\n"
        )
        self.assertEqual([], parse_armed_channels(text))

    def test_missing_heading_raises_channels_section_missing(self):
        with self.assertRaises(ChannelsSectionMissing):
            parse_armed_channels("# Brief\n\nNothing relevant here.\n")

    def test_heading_with_neither_table_nor_marker_raises_unparseable(self):
        text = (
            "# Brief\n\n## Live lanes and armed channels\n\n"
            "none in flight\n\n## Next\n"
        )
        with self.assertRaises(ChannelsSectionUnparseable):
            parse_armed_channels(text)

    def test_table_with_header_but_zero_data_rows_is_a_legitimate_empty_list(self):
        text = (
            "# Brief\n\n## Live lanes and armed channels\n\n"
            "| Channel | Lane | Working from | Task |\n"
            "|---|---|---|---|\n"
            "\n## Next\n"
        )
        self.assertEqual([], parse_armed_channels(text))

    @unittest.skipUnless(
        REAL_BRIEF_PATH.exists(), f"real brief not present at {REAL_BRIEF_PATH}"
    )
    def test_parses_the_real_deployed_brief(self):
        # Read-only: this is the one test in the suite that is not confined
        # to tempfile.mkdtemp(), by design -- it is the regression test for
        # "the parser cannot read the real brief". Skips cleanly on a fresh
        # clone or any host where the file does not exist.
        channels = parse_armed_channels(REAL_BRIEF_PATH.read_text())
        self.assertTrue(
            channels, "real brief's armed-channels section parsed to an empty list"
        )


class RespawnSupervisorTest(unittest.TestCase):
    def test_respawns_pane_then_seeds_the_tick_prompt(self):
        transport = FakeTransport()
        respawn_supervisor(transport, target="%19", tick_prompt="tick")
        self.assertEqual(
            [("respawn_pane", "%19"), ("send_literal", "%19", "tick")],
            transport.calls,
        )


if __name__ == "__main__":
    unittest.main()
