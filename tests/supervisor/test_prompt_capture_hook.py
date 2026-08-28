import fcntl
import json
import multiprocessing
import os
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
SUPERVISOR_DIR = REPO_ROOT / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import prompt_capture_hook  # noqa: E402
from core import Ledger  # noqa: E402
from mine_prompts import CONTEXT_UNDETERMINED  # noqa: E402


def _hold_ledger_lock(lock_path, hold_seconds, ready):
    """Run in a separate process (not thread -- flock is per open file
    description, and a thread in this same process would share the parent's
    fd table in a way that doesn't reproduce cross-process contention) so the
    hook subprocess under test hits a *real* held lock, the same shape a
    concurrent `itemize_prompts.py --load` or a process that died holding
    the lock would leave behind."""
    with open(lock_path, "a+") as lock_file:
        fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX)
        ready.set()
        time.sleep(hold_seconds)


class CaptureUnitTests(unittest.TestCase):
    """agent-supervisor#687: `capture()` is the part of the hook that talks
    to the ledger, split out from `main()`'s stdin/exit-code plumbing so
    these tests can call it directly rather than shell out for every case."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(self.tempdir.name)

    def test_real_prompt_is_captured_and_left_unitemised(self):
        status = prompt_capture_hook.capture(
            {"session_id": "s1", "prompt": "make render mode LIVE"}, self.ledger
        )
        self.assertIn("left unitemised for judging", status)
        rows = [p for p in self._all_prompts()]
        self.assertEqual(1, len(rows))
        self.assertEqual("make render mode LIVE", rows[0]["text_raw"])
        self.assertEqual(CONTEXT_UNDETERMINED, rows[0]["context"])
        # No transcript_path given -> nothing to itemise from, and no model
        # ran -- confirmed by there being no items row at all yet.
        self.assertEqual([], self.ledger.list_open_items())
        unitemised = self.ledger.list_unitemised_prompts()
        self.assertEqual(1, len(unitemised))
        self.assertEqual(rows[0]["id"], unitemised[0]["id"])

    def test_dispatch_brief_boilerplate_is_dropped_mechanically_not_deleted(self):
        text = "Read /tmp/brief.md. That file is your complete brief. Do the work."
        status = prompt_capture_hook.capture({"session_id": "s1", "prompt": text}, self.ledger)
        self.assertIn("dropped as noise", status)
        prompts = self._all_prompts()
        self.assertEqual(1, len(prompts), "the prompt row itself is never deleted, only its item")
        self.assertEqual(text, prompts[0]["text_raw"])
        self.assertEqual([], self.ledger.list_unitemised_prompts(),
                          "a dropped prompt must leave the itemisation queue, or it resurfaces forever")

    def test_idempotent_rerun_writes_zero(self):
        payload = {"session_id": "s1", "prompt": "keep the CLI narrow", "transcript_path": ""}
        first = prompt_capture_hook.capture(payload, self.ledger)
        self.assertIn("written", first)
        before = len(self._all_prompts())
        second = prompt_capture_hook.capture(payload, self.ledger)
        self.assertIn("already present", second)
        after = len(self._all_prompts())
        self.assertEqual(before, after, "a re-run over the identical submission must write zero new rows")

    def test_context_comes_from_transcript_last_assistant_turn(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        transcript = Path(tmp.name) / "session.jsonl"
        transcript.write_text(
            json.dumps({"message": {"role": "assistant", "content": "deciding LIVE vs PREVIEW"}}) + "\n"
        )
        prompt_capture_hook.capture(
            {"session_id": "s2", "prompt": "make it LIVE", "transcript_path": str(transcript)},
            self.ledger,
        )
        rows = self._all_prompts()
        self.assertEqual("deciding LIVE vs PREVIEW", rows[0]["context"])

    def test_empty_prompt_writes_nothing(self):
        status = prompt_capture_hook.capture({"session_id": "s1", "prompt": "   "}, self.ledger)
        self.assertIn("nothing to capture", status)
        self.assertEqual([], self._all_prompts())

    def _all_prompts(self):
        import sqlite3
        connection = sqlite3.connect(Path(self.tempdir.name) / "ledger.sqlite3")
        connection.row_factory = sqlite3.Row
        try:
            return [dict(row) for row in connection.execute("SELECT * FROM prompts ORDER BY at").fetchall()]
        finally:
            connection.close()


class CaptureHealthViewTests(unittest.TestCase):
    """agent-supervisor#687: the staleness signal itself."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(self.tempdir.name)

    def test_empty_corpus_reports_null_not_zero(self):
        rows = self.ledger.read_prompt_view("capture_health")
        self.assertEqual(1, len(rows))
        self.assertIsNone(rows[0]["newest_prompt_at"])
        self.assertIsNone(rows[0]["seconds_since_capture"])

    def test_seconds_since_capture_reflects_the_newest_row(self):
        import time
        now = int(time.time())
        self.ledger.record_prompt(
            "hp-oldrow", at=now - 400000, text_raw="old", context="ctx"
        )
        self.ledger.record_prompt(
            "hp-newrow", at=now - 5, text_raw="new", context="ctx"
        )
        rows = self.ledger.read_prompt_view("capture_health")
        self.assertEqual(now - 5, rows[0]["newest_prompt_at"])
        self.assertGreaterEqual(rows[0]["seconds_since_capture"], 5)
        self.assertLess(rows[0]["seconds_since_capture"], 60)  # generous slack for test runtime


class MainEndToEndTests(unittest.TestCase):
    """Exercises the real subprocess entry point Claude Code actually
    invokes -- stdin in, exit code and stdout/stderr behaviour verified
    directly rather than assumed from the unit-level `capture()` tests."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)

    def _run(self, payload):
        return subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "prompt_capture_hook.py")],
            input=json.dumps(payload),
            capture_output=True,
            text=True,
            env={**__import__("os").environ, "AGENT_SUPERVISOR_STATE_DIR": self.tempdir.name},
            timeout=30,
        )

    def test_exit_code_is_zero_and_stdout_is_empty(self):
        result = self._run({"session_id": "s1", "prompt": "a real directive"})
        self.assertEqual(0, result.returncode, msg=result.stderr)
        self.assertEqual("", result.stdout, "stdout is injected into model context -- must stay empty")

    def test_malformed_stdin_still_exits_zero(self):
        result = subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "prompt_capture_hook.py")],
            input="not json{{{",
            capture_output=True,
            text=True,
            env={**__import__("os").environ, "AGENT_SUPERVISOR_STATE_DIR": self.tempdir.name},
            timeout=30,
        )
        self.assertEqual(0, result.returncode)
        self.assertEqual("", result.stdout)

    def test_prompt_is_actually_written_via_the_real_entry_point(self):
        self._run({"session_id": "s1", "prompt": "verify end to end capture"})
        ledger = Ledger(self.tempdir.name)
        unitemised = ledger.list_unitemised_prompts()
        self.assertEqual(1, len(unitemised))
        self.assertEqual("verify end to end capture", unitemised[0]["text_raw"])

    def test_locked_ledger_fails_open_within_bound_not_hangs(self):
        """agent-supervisor#693 review finding: `record_prompt()` ->
        `Ledger._locked()`'s flock was a blocking call with no timeout, so a
        held lock hung the hook past the point its own try/except ever got a
        chance to fail open. `Ledger(..., lock_timeout=...)` bounds that wait;
        this reproduces the reviewer's exact mutation -- hold the lock from a
        second process, then invoke the real hook binary -- and asserts the
        hook still returns quickly and writes nothing, rather than trusting
        the unit-level `capture()` call alone."""
        # `Ledger(self.tempdir.name)` (no lock_timeout) creates the state dir
        # and schema up front so the held-lock process below doesn't race the
        # hook's own first-time `_initialize()` for who creates the lock file.
        Ledger(self.tempdir.name)
        lock_path = os.path.join(self.tempdir.name, "ledger.lock")

        ready = multiprocessing.Event()
        holder = multiprocessing.Process(
            target=_hold_ledger_lock, args=(lock_path, 10, ready)
        )
        holder.start()
        self.addCleanup(holder.join)
        self.addCleanup(holder.terminate)
        self.assertTrue(ready.wait(timeout=5), "lock holder never acquired the flock")

        start = time.monotonic()
        result = self._run({"session_id": "lock-test", "prompt": "should fail open, not hang"})
        elapsed = time.monotonic() - start

        self.assertEqual(0, result.returncode, msg=result.stderr)
        self.assertEqual("", result.stdout)
        # LOCK_TIMEOUT_SECONDS (2.0) plus generous scheduling slack -- this
        # is the property the fix exists for: a locked ledger costs a
        # bounded, small delay, never the 10s the holder process sleeps for.
        self.assertLess(
            elapsed,
            prompt_capture_hook.LOCK_TIMEOUT_SECONDS + 5,
            "hook did not fail open within its bounded wait -- it hung",
        )
        self.assertIn("LockTimeout", result.stderr)

        holder.terminate()
        holder.join()

        # The prompt from the locked attempt above must never have landed --
        # otherwise this is silently succeeding at something other than
        # what it claims.
        ledger = Ledger(self.tempdir.name)
        unitemised = ledger.list_unitemised_prompts()
        self.assertEqual(
            0, len(unitemised), "the locked-out attempt must not have written a prompt row"
        )


class RegistrationFailsOpenTests(unittest.TestCase):
    """agent-supervisor#730: the incident was NOT a bug in this file's own
    Python -- `main()`'s try/except never even got a chance to run, because
    `python3 $CLAUDE_PROJECT_DIR/.../prompt_capture_hook.py` fails to launch
    at all when `CLAUDE_PROJECT_DIR` is stale (captured before a repo
    rename), and CPython's own exit code for "can't open file" is 2 --
    which collides with Claude Code's `UserPromptSubmit` contract, where
    exit 2 specifically means "blocking error: discard the prompt" (see
    `.claude/references/hooks-guide.md`'s exit-code table). No unit test on
    `capture()` or `main()` can catch this class of failure, because both
    run inside the same process that never starts -- this test runs the
    exact command string `.claude/settings.json` registers, the same way
    the harness does, under `bash -c`."""

    def setUp(self):
        settings = json.loads((REPO_ROOT / ".claude" / "settings.json").read_text())
        [hook_entry] = settings["hooks"]["UserPromptSubmit"]
        [hook] = hook_entry["hooks"]
        self.command = hook["command"]
        self.assertIn("prompt_capture_hook.py", self.command)

    def _run_registered_command(self, project_dir, state_dir):
        return subprocess.run(
            ["bash", "-c", self.command],
            input=json.dumps({"session_id": "s1", "prompt": "a directive that must not be lost"}),
            capture_output=True,
            text=True,
            env={
                **os.environ,
                "CLAUDE_PROJECT_DIR": project_dir,
                "AGENT_SUPERVISOR_STATE_DIR": state_dir,
            },
            timeout=30,
        )

    def test_stale_project_dir_fails_open_not_blocking(self):
        with tempfile.TemporaryDirectory() as state_dir:
            result = self._run_registered_command(
                project_dir="/tmp/nonexistent-agent-supervisor-project-dir", state_dir=state_dir
            )
            self.assertEqual(
                0,
                result.returncode,
                "a stale CLAUDE_PROJECT_DIR must fail open (exit 0), not exit 2 -- "
                "exit 2 is Claude Code's own 'discard the prompt' code, and this exact "
                "collision (python3's file-not-found exit code == 2) is #730's incident",
            )
            self.assertEqual("", result.stdout)
            failure_log = Path(state_dir) / "prompt-capture-hook-failures.log"
            self.assertTrue(failure_log.exists(), "the launch failure must still be visible somewhere")
            self.assertIn("No such file or directory", failure_log.read_text())

    def test_real_project_dir_still_captures_the_prompt(self):
        with tempfile.TemporaryDirectory() as state_dir:
            result = self._run_registered_command(project_dir=str(REPO_ROOT), state_dir=state_dir)
            self.assertEqual(0, result.returncode, msg=result.stderr)
            ledger = Ledger(state_dir)
            unitemised = ledger.list_unitemised_prompts()
            self.assertEqual(1, len(unitemised), "the registered command must still capture on the happy path")
            self.assertEqual("a directive that must not be lost", unitemised[0]["text_raw"])


if __name__ == "__main__":
    unittest.main()
