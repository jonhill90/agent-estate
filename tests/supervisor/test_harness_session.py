"""harness_session.py's codex resolver -- agent-supervisor#(codex adapter
gap, 2026-08-23).

`resolve()` used to refuse for every harness but claude, unconditionally --
not a theoretical gap, a LIVE one: every codex dispatch hit it, every time,
and left that lane permanently UNRECOVERABLE to restore.sh. This suite
proves the codex resolver added to close that reads codex's real on-disk
rollout shape correctly, and stays refusal-first everywhere the same three
tests (began during this dispatch, matches the marker, agrees with its own
filename) fail -- the identical discipline the claude resolver already has,
applied to codex's differently-shaped files.

The fixture shape (`session_meta` as line 1, `payload.{session_id,cwd,
timestamp}`, filename `rollout-<ts>-<uuid>.jsonl` under
`.codex/sessions/<Y>/<M>/<D>/`) is not invented here: it is what a REAL
`codex -a never -s danger-full-access` session wrote to
`~/.codex/sessions/...` on this machine (codex-cli 0.149.0, isolated tmux
socket, private TMUX_TMPDIR, 2026-08-23), copied into these fixtures rather
than paraphrased.
"""

import json
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import harness_session as hs  # noqa: E402

SID = "01a02f3f-4f15-78c0-9086-442a4741037c"
MARKER = "/private/tmp/ad-worktree-example"
# Real payload shape, trimmed to the fields this module reads -- copied from
# a real rollout file's own first line, not paraphrased.
BEGIN_EPOCH = 1787499073.303  # 2026-08-23T15:31:13.303Z, the fixture's own timestamp


def _write_rollout(root, *, session_id=SID, cwd=MARKER, timestamp="2026-08-23T15:31:13.303Z",
                    record_type="session_meta", filename_id=None, day="23"):
    sess_dir = Path(root) / ".codex" / "sessions" / "2026" / "08" / day
    sess_dir.mkdir(parents=True, exist_ok=True)
    stem_id = filename_id if filename_id is not None else session_id
    path = sess_dir / f"rollout-2026-08-23T11-31-13-{stem_id}.jsonl"
    payload = {}
    if session_id is not None:
        payload["session_id"] = session_id
    if cwd is not None:
        payload["cwd"] = cwd
    if timestamp is not None:
        payload["timestamp"] = timestamp
    record = {"timestamp": timestamp, "ordinal": 0, "type": record_type, "payload": payload}
    with path.open("w", encoding="utf-8") as handle:
        handle.write(json.dumps(record) + "\n")
        handle.write(json.dumps({"type": "turn.completed"}) + "\n")
    return path


class CandidatesCodexTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.home = self.tmp.name

    def tearDown(self):
        self.tmp.cleanup()

    def test_finds_a_real_shaped_rollout(self):
        _write_rollout(self.home)
        found = hs.candidates_codex(home=self.home, marker=MARKER, since=0)
        self.assertEqual(found, [SID])

    def test_no_sessions_directory_is_empty_not_an_error(self):
        found = hs.candidates_codex(home=self.home, marker=MARKER, since=0)
        self.assertEqual(found, [])

    def test_cwd_must_match_exactly_not_substring(self):
        _write_rollout(self.home, cwd=MARKER + "/subdir")
        found = hs.candidates_codex(home=self.home, marker=MARKER, since=0)
        self.assertEqual(found, [], "a cwd that merely CONTAINS the marker must not match")

    def test_different_cwd_never_matches(self):
        _write_rollout(self.home, cwd="/private/tmp/someone-elses-worktree")
        found = hs.candidates_codex(home=self.home, marker=MARKER, since=0)
        self.assertEqual(found, [])

    def test_a_transcript_from_before_since_is_excluded(self):
        _write_rollout(self.home)
        found = hs.candidates_codex(home=self.home, marker=MARKER, since=BEGIN_EPOCH + 3600)
        self.assertEqual(found, [], "a rollout that predates this dispatch's `since` must not resolve")

    def test_within_began_slack_still_matches(self):
        _write_rollout(self.home)
        # since is a few seconds AFTER the file's own timestamp -- inside
        # BEGAN_SLACK_SECONDS, the same clock-skew allowance the claude
        # resolver gives itself.
        found = hs.candidates_codex(home=self.home, marker=MARKER, since=BEGIN_EPOCH + 2)
        self.assertEqual(found, [SID])

    def test_filename_disagreeing_with_payload_id_is_refused(self):
        # The file's own trailing uuid (its real identity to `codex resume`)
        # disagrees with what payload.session_id claims -- a hand-edited or
        # corrupted header, same class of defect `_declares_own_id` catches
        # for claude.
        _write_rollout(self.home, filename_id="99999999-9999-4999-8999-999999999999")
        found = hs.candidates_codex(home=self.home, marker=MARKER, since=0)
        self.assertEqual(found, [])

    def test_non_session_meta_first_line_is_ignored(self):
        _write_rollout(self.home, record_type="turn.started")
        found = hs.candidates_codex(home=self.home, marker=MARKER, since=0)
        self.assertEqual(found, [])

    def test_missing_cwd_is_ignored_not_a_crash(self):
        _write_rollout(self.home, cwd=None)
        found = hs.candidates_codex(home=self.home, marker=MARKER, since=0)
        self.assertEqual(found, [])

    def test_two_lanes_two_worktrees_each_resolves_only_its_own(self):
        other_sid = "02b13040-5b26-89d1-a297-553852148d2f"
        _write_rollout(self.home, session_id=SID, cwd=MARKER)
        _write_rollout(self.home, session_id=other_sid, cwd=MARKER + "-2",
                        filename_id=other_sid)
        found_a = hs.candidates_codex(home=self.home, marker=MARKER, since=0)
        found_b = hs.candidates_codex(home=self.home, marker=MARKER + "-2", since=0)
        self.assertEqual(found_a, [SID])
        self.assertEqual(found_b, [other_sid])


class ResolveDispatchTests(unittest.TestCase):
    """resolve()'s own harness dispatch -- both implemented harnesses reachable,
    everything else refuses by name, same as before this change for claude
    alone."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.home = self.tmp.name

    def tearDown(self):
        self.tmp.cleanup()

    def test_codex_resolves_end_to_end(self):
        _write_rollout(self.home)
        got = hs.resolve(harness="codex", marker=MARKER, since=0, home=self.home, timeout=0)
        self.assertEqual(got, SID)

    def test_claude_still_resolves_unaffected_by_codex_support(self):
        project = Path(self.home) / ".claude" / "projects" / "lane"
        project.mkdir(parents=True)
        claude_sid = "33333333-3333-4333-8333-333333333333"
        transcript = project / f"{claude_sid}.jsonl"
        with transcript.open("w", encoding="utf-8") as handle:
            handle.write(json.dumps({"sessionId": claude_sid,
                                      "timestamp": "2026-08-23T15:31:13.303Z",
                                      "message": MARKER}) + "\n")
        got = hs.resolve(harness="claude", marker=MARKER, since=0, home=self.home, timeout=0)
        self.assertEqual(got, claude_sid)

    def test_unknown_harness_refuses_by_name_not_a_bare_claude_only_message(self):
        with self.assertRaises(LookupError) as ctx:
            hs.resolve(harness="copilot", marker=MARKER, since=0, home=self.home, timeout=0)
        message = str(ctx.exception)
        self.assertIn("copilot", message)
        self.assertIn("claude", message)
        self.assertIn("codex", message)

    def test_ambiguous_codex_match_refuses_rather_than_guessing(self):
        other_sid = "44444444-4444-4444-8444-444444444444"
        _write_rollout(self.home, session_id=SID, filename_id=SID, day="23")
        _write_rollout(self.home, session_id=other_sid, filename_id=other_sid, day="24")
        with self.assertRaises(LookupError) as ctx:
            hs.resolve(harness="codex", marker=MARKER, since=0, home=self.home, timeout=0)
        self.assertIn("2 transcripts", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
