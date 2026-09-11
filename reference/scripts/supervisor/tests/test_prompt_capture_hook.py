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

REPO_ROOT = Path(__file__).resolve().parents[4]
SUPERVISOR_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(SUPERVISOR_DIR))

import prompt_capture_hook  # noqa: E402
from adapter import TmuxAdapter  # noqa: E402
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


class CorpusDirDefaultTests(unittest.TestCase):
    """agent-estate#1357: the central claim this issue's fix rests on -- with
    no env var override, the hook's Ledger root resolves to the corpus, not
    the dead ~/.local/state/agent-dotfiles-supervisor directory. Runs a
    subprocess with a fake $HOME (never the real one, so this never risks
    touching the real ~/corpus) and neither AGENT_CORPUS_DIR nor
    AGENT_SUPERVISOR_STATE_DIR set -- the exact unset-env shape every real
    Claude Code session hit before this fix, and the shape agent-estate#1357
    measured 1,873 orphaned prompts under."""

    def _default_corpus_dir(self, home):
        result = subprocess.run(
            [sys.executable, "-c", "import prompt_capture_hook; print(prompt_capture_hook.CORPUS_DIR)"],
            capture_output=True,
            text=True,
            env={
                "PATH": os.environ.get("PATH", ""),
                "HOME": home,
                "PYTHONPATH": str(SUPERVISOR_DIR),
                # Deliberately absent: AGENT_CORPUS_DIR, AGENT_SUPERVISOR_STATE_DIR.
            },
            timeout=30,
        )
        self.assertEqual(0, result.returncode, msg=result.stderr)
        return result.stdout.strip()

    def test_default_corpus_dir_is_the_corpus_not_the_dead_state_dir(self):
        with tempfile.TemporaryDirectory() as fake_home:
            got = self._default_corpus_dir(fake_home)
            self.assertEqual(os.path.join(fake_home, "corpus"), got)
            self.assertNotIn("agent-dotfiles-supervisor", got,
                              "the default must never resolve into the dead supervisor state dir "
                              "(agent-estate#942/#1357) -- this is the exact regression this test exists "
                              "to catch if the default is ever reverted or re-typed wrong")

    def test_agent_corpus_dir_env_var_overrides_the_default(self):
        with tempfile.TemporaryDirectory() as fake_home, tempfile.TemporaryDirectory() as override_dir:
            result = subprocess.run(
                [sys.executable, "-c", "import prompt_capture_hook; print(prompt_capture_hook.CORPUS_DIR)"],
                capture_output=True,
                text=True,
                env={
                    "PATH": os.environ.get("PATH", ""),
                    "HOME": fake_home,
                    "PYTHONPATH": str(SUPERVISOR_DIR),
                    "AGENT_CORPUS_DIR": override_dir,
                },
                timeout=30,
            )
            self.assertEqual(0, result.returncode, msg=result.stderr)
            self.assertEqual(override_dir, result.stdout.strip())


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


class ResolveTmuxPaneTests(unittest.TestCase):
    """agent-supervisor#755 part B: `_resolve_tmux_pane` never blocks or
    raises, and never guesses when `$TMUX_PANE` is unset."""

    def setUp(self):
        self._saved_env = dict(os.environ)
        self.addCleanup(lambda: (os.environ.clear(), os.environ.update(self._saved_env)))

    def test_no_tmux_pane_env_returns_none_none(self):
        os.environ.pop("TMUX_PANE", None)
        self.assertEqual((None, None), prompt_capture_hook._resolve_tmux_pane())

    def test_resolves_via_a_fake_tmux_binary_on_path(self):
        """A fake `tmux` on PATH stands in for the real binary, proving the
        call shape (`display-message -p -t "$TMUX_PANE" '#{session_name}:
        #{window_index}'`, never a bare `display-message` -- invariant 10)
        without depending on a real tmux server existing in this test
        environment."""
        with tempfile.TemporaryDirectory() as bindir:
            fake_tmux = Path(bindir) / "tmux"
            fake_tmux.write_text(
                "#!/bin/sh\n"
                'if [ "$1" = "display-message" ] && [ "$2" = "-p" ] && [ "$3" = "-t" ] && [ "$4" = "%22" ]; then\n'
                '  echo "estate:1"\n'
                "  exit 0\n"
                "fi\n"
                "exit 1\n"
            )
            fake_tmux.chmod(0o755)
            os.environ["TMUX_PANE"] = "%22"
            os.environ["TMUX_BIN"] = str(fake_tmux)
            import importlib
            importlib.reload(prompt_capture_hook)
            try:
                self.assertEqual(("%22", "estate:1"), prompt_capture_hook._resolve_tmux_pane())
            finally:
                os.environ.pop("TMUX_BIN", None)
                importlib.reload(prompt_capture_hook)

    def test_tmux_pane_set_but_unresolvable_keeps_raw_value_only(self):
        """A pane that no longer exists (closed between submit and hook
        execution) must not silently invent a target -- and must not raise
        or hang either."""
        with tempfile.TemporaryDirectory() as bindir:
            fake_tmux = Path(bindir) / "tmux"
            fake_tmux.write_text("#!/bin/sh\nexit 1\n")
            fake_tmux.chmod(0o755)
            os.environ["TMUX_PANE"] = "%99"
            os.environ["TMUX_BIN"] = str(fake_tmux)
            import importlib
            importlib.reload(prompt_capture_hook)
            try:
                self.assertEqual(("%99", None), prompt_capture_hook._resolve_tmux_pane())
            finally:
                os.environ.pop("TMUX_BIN", None)
                importlib.reload(prompt_capture_hook)

    def test_missing_tmux_binary_fails_open_not_raise(self):
        os.environ["TMUX_PANE"] = "%1"
        os.environ["TMUX_BIN"] = "/no/such/tmux/binary/anywhere"
        import importlib
        importlib.reload(prompt_capture_hook)
        try:
            self.assertEqual(("%1", None), prompt_capture_hook._resolve_tmux_pane())
        finally:
            os.environ.pop("TMUX_BIN", None)
            importlib.reload(prompt_capture_hook)


class CaptureWritesPaneColumnsTests(unittest.TestCase):
    """`capture()` end to end: the resolved pane identity actually reaches
    the `prompts` row, and a pane-less submission records NULL rather than
    a guess."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(self.tempdir.name)
        self._saved_env = dict(os.environ)
        self.addCleanup(lambda: (os.environ.clear(), os.environ.update(self._saved_env)))

    def test_capture_records_resolved_pane_target(self):
        with tempfile.TemporaryDirectory() as bindir:
            fake_tmux = Path(bindir) / "tmux"
            fake_tmux.write_text("#!/bin/sh\necho 'estate:1'\nexit 0\n")
            fake_tmux.chmod(0o755)
            os.environ["TMUX_PANE"] = "%22"
            os.environ["TMUX_BIN"] = str(fake_tmux)
            import importlib
            importlib.reload(prompt_capture_hook)
            try:
                prompt_capture_hook.capture(
                    {"session_id": "s1", "prompt": "Director decision required. brief at /tmp/x.md"},
                    self.ledger,
                )
            finally:
                os.environ.pop("TMUX_BIN", None)
                importlib.reload(prompt_capture_hook)
        rows = self.ledger.list_unitemised_prompts()
        self.assertEqual(1, len(rows))
        self.assertEqual("%22", rows[0]["tmux_pane"])
        self.assertEqual("estate:1", rows[0]["tmux_pane_target"])

    def test_capture_with_no_tmux_pane_records_null(self):
        os.environ.pop("TMUX_PANE", None)
        prompt_capture_hook.capture({"session_id": "s1", "prompt": "a claude-print lane's own turn"}, self.ledger)
        rows = self.ledger.list_unitemised_prompts()
        self.assertEqual(1, len(rows))
        self.assertIsNone(rows[0]["tmux_pane"])
        self.assertIsNone(rows[0]["tmux_pane_target"])


class CaptureWritesAuthorColumnTests(unittest.TestCase):
    """agent-estate#1395/#1394 (label-prompt-author-1395): `capture()`
    consults `Ledger.consume_pending_author` and writes the result (or
    'unknown') into `prompts.author`.

    MUTATION-CHECK, BOTH DIRECTIONS (the task brief's own required check):
    a prompt whose exact text was registered as machine-sent must be
    labelled as such (`test_a_registered_supervisor_prompt_is_labelled_supervisor`),
    and -- the direction that actually matters, because false attribution
    is the defect #1395 measured, not an absent label -- a prompt from
    Jon's own session, with nothing registered for it, must NEVER be
    mislabelled `supervisor`/`director`; it stays `unknown`
    (`test_an_unregistered_prompt_from_jons_own_session_is_never_mislabelled`)."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(self.tempdir.name)

    def test_a_registered_supervisor_prompt_is_labelled_supervisor(self):
        text = "Hill90 supervisor events:\n- event happened\nAcknowledge with the usual command."
        self.ledger.register_pending_author(text, author="supervisor")

        prompt_capture_hook.capture({"session_id": "estate-pane-session", "prompt": text}, self.ledger)

        rows = self.ledger.list_unitemised_prompts()
        self.assertEqual(1, len(rows))
        self.assertEqual("supervisor", rows[0]["author"])

    def test_a_registered_director_prompt_is_labelled_director(self):
        text = "Director decision required. Your complete brief is at /tmp/brief-9001.md."
        self.ledger.register_pending_author(text, author="director")

        status = prompt_capture_hook.capture({"session_id": "director-session", "prompt": text}, self.ledger)

        # This particular text also matches a NOISE_MARKERS boilerplate
        # shape and is mechanically dropped -- but the prompt row itself
        # (never deleted, only its item) still carries the real author,
        # proving the two mechanisms are independent: noise-dropping does
        # not skip the author lookup, and the author lookup does not
        # change noise classification.
        self.assertIn("dropped as noise", status)
        rows = self._all_prompts()
        self.assertEqual(1, len(rows))
        self.assertEqual("director", rows[0]["author"])
        self.assertEqual([], self.ledger.list_unitemised_prompts(),
                          "dropped as noise -- confirms the prompt row (checked above) is distinct "
                          "from the unitemised queue, same as test_dispatch_brief_boilerplate_...")

    def test_an_unregistered_prompt_from_jons_own_session_is_never_mislabelled(self):
        # Nothing registered for this text -- the shape of every one of
        # Jon's own real prompts, including the exact kind #1395 found
        # mislabelled downstream ("the mistake was mine" attributed to
        # "Jon's mistake"). The defect being fixed is a FALSE label, not a
        # missing one, so the only acceptable outcome here is 'unknown' --
        # never 'supervisor' or 'director' guessed from the text's content,
        # length, or tone.
        text = "the mistake was mine: I dispatched h#748 off #757's 'none fixed yet' table"
        prompt_capture_hook.capture({"session_id": "jons-real-session", "prompt": text}, self.ledger)

        rows = self.ledger.list_unitemised_prompts()
        self.assertEqual(1, len(rows))
        self.assertEqual("unknown", rows[0]["author"])
        self.assertNotIn(rows[0]["author"], ("supervisor", "director"))

    def test_consuming_a_registration_is_one_time_only(self):
        text = "status report: three tasks completed"
        self.ledger.register_pending_author(text, author="supervisor")
        prompt_capture_hook.capture({"session_id": "s1", "prompt": text}, self.ledger)

        # A second, later submission of the IDENTICAL text (a different
        # session, a coincidence, or literally the same text re-typed by
        # Jon) must not inherit the already-spent registration.
        prompt_capture_hook.capture({"session_id": "s2", "prompt": text}, self.ledger)

        rows = sorted(self.ledger.list_unitemised_prompts(), key=lambda r: r["session"])
        # The idempotent `hp-` id is derived from (session_id, text), so
        # two different sessions submitting the same text DO produce two
        # distinct prompt rows here -- both real, worth asserting on both.
        self.assertEqual(2, len(rows))
        by_session = {row["session"]: row["author"] for row in rows}
        self.assertEqual("supervisor", by_session["s1"])
        self.assertEqual("unknown", by_session["s2"])

    def _all_prompts(self):
        import sqlite3
        connection = sqlite3.connect(Path(self.tempdir.name) / "ledger.sqlite3")
        connection.row_factory = sqlite3.Row
        try:
            return [dict(row) for row in connection.execute("SELECT * FROM prompts ORDER BY at").fetchall()]
        finally:
            connection.close()


class WiredInjectionSiteEndToEndTests(unittest.TestCase):
    """agent-estate#1395/#1394 (wire-author-registration): the full path,
    real `TmuxAdapter.assign_task` through to `prompt_capture_hook.capture`,
    no shortcuts. This is the task brief's own required check, pasted as
    both outcomes:

    - A prompt injected through a wired call site (`TmuxAdapter.assign_task`,
      via `adapter.py`'s own `register_pending_author` call) lands with its
      real author when the pane's harness later re-submits that exact text
      as a `UserPromptSubmit` event -- simulated here the same way a real
      lane's own capture hook would see it.
    - A DIFFERENT prompt, submitted in the same ledger around the same time
      but never registered by anything (Jon typing directly into his own
      session), lands `unknown` -- never `supervisor`/`director`, even
      though a real registration exists concurrently for other text."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(self.tempdir.name, clock=lambda: 1_000)
        import test_adapter  # noqa: E402 -- sibling test module, same sys.path entry

        self.transport = test_adapter.FakeTransport()
        self.adapter = TmuxAdapter(self.ledger, self.transport, clock=lambda: 1_000)
        self.adapter.register_lane(
            lane="architecture", target="%19", harness="codex", repo="/repo/hill90", nonce="nonce-19"
        )
        self.ledger.reconstruct_task(
            task_id="codex-task",
            source_kind="issue",
            source_url="https://github.com/jonhill90/Hill90/issues/9001",
            source_ref="a" * 40,
            summary="Review one artifact",
            source_state="OPEN",
            status="created",
            evidence=[],
            status_marker=None,
        )

    def test_wired_call_site_prompt_lands_with_its_real_author(self):
        self.adapter.assign_task(lane="architecture", task_id="codex-task", summary="Review one artifact")
        injected_text = self.transport.sends[-1][1]

        # What the lane's own harness process would do next: the injected
        # text becomes that pane's next `UserPromptSubmit` event, captured
        # by the same hook every prompt goes through.
        prompt_capture_hook.capture({"session_id": "lane-session", "prompt": injected_text}, self.ledger)

        rows = [row for row in self._all_prompts() if row["session"] == "lane-session"]
        self.assertEqual(1, len(rows))
        self.assertEqual("supervisor", rows[0]["author"])

    def test_an_unrelated_prompt_with_nothing_registered_stays_unknown(self):
        self.adapter.assign_task(lane="architecture", task_id="codex-task", summary="Review one artifact")
        # A registration now genuinely exists in the ledger for the injected
        # text above -- proves the negative case isn't merely "nothing was
        # ever registered", it's "this specific, different text was not".
        jons_own_text = "actually, hold off on that -- do the tui work instead"
        prompt_capture_hook.capture({"session_id": "jons-real-session", "prompt": jons_own_text}, self.ledger)

        rows = [row for row in self._all_prompts() if row["session"] == "jons-real-session"]
        self.assertEqual(1, len(rows))
        self.assertEqual("unknown", rows[0]["author"])
        self.assertNotIn(rows[0]["author"], ("supervisor", "director"))

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
            env={
                **__import__("os").environ,
                "AGENT_SUPERVISOR_STATE_DIR": self.tempdir.name,
                # agent-estate#1357: the hook's Ledger root now reads
                # AGENT_CORPUS_DIR, separately from AGENT_SUPERVISOR_STATE_DIR
                # (which stays correct for FAILURE_LOG only) -- both must
                # point at the same tempdir here so this test's own
                # `Ledger(self.tempdir.name)` reads what the subprocess
                # actually wrote, instead of silently falling through to the
                # real ~/corpus.
                "AGENT_CORPUS_DIR": self.tempdir.name,
            },
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
            env={
                **__import__("os").environ,
                "AGENT_SUPERVISOR_STATE_DIR": self.tempdir.name,
                # agent-estate#1357: the hook's Ledger root now reads
                # AGENT_CORPUS_DIR, separately from AGENT_SUPERVISOR_STATE_DIR
                # (which stays correct for FAILURE_LOG only) -- both must
                # point at the same tempdir here so this test's own
                # `Ledger(self.tempdir.name)` reads what the subprocess
                # actually wrote, instead of silently falling through to the
                # real ~/corpus.
                "AGENT_CORPUS_DIR": self.tempdir.name,
            },
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
                # agent-estate#1357: see the identical comment in
                # MainEndToEndTests._run -- both env vars must point at the
                # same tempdir so this test's own Ledger(state_dir) reads
                # what the registered command actually wrote.
                "AGENT_CORPUS_DIR": state_dir,
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
