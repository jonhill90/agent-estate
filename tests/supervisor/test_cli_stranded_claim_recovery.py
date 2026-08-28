import contextlib
import hashlib
import io
import json
import os
import socket
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import cli  # noqa: E402
from core import Ledger  # noqa: E402


class StrandedClaimRecoveryIsReachable(unittest.TestCase):
    """agent-dotfiles#209. The recovery for a stranded claim has to be
    RUNNABLE, not merely implemented.

    `Ledger.cancel_open_task` was the #144 review's own example of a method
    with no caller, and it still had none: not in `cli.py`'s parser, not
    invoked anywhere outside `tests/`. `reap_stale_lane_claims` would have
    been the second if it shipped without wiring. These drive `cli.py` as a
    SUBPROCESS, the way `dispatch.sh` and a human operator both reach it --
    an import-level test would pass against a parser that never learned the
    subcommand.
    """

    def _run(self, root, *args):
        return subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "cli.py"), "--state-dir", str(root), *args],
            capture_output=True,
            text=True,
            timeout=30,
        )

    def _lane(self, root):
        ledger = Ledger(Path(root))
        ledger.register_lane(
            lane="free-9", pane_id="%9", nonce="nonce-9", harness="claude",
            repo=root, server_id="socket:1", session_id="$0", command="claude.exe",
        )
        return ledger

    def test_claim_lane_records_the_owner_pid_it_is_given(self):
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            proc = self._run(root, "claim-lane", "--lane", "free-9", "--token", "ad1-x", "--owner-pid", "4242")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertTrue(json.loads(proc.stdout)["claimed"])
            task = Ledger(Path(root)).get_task("ledger-claim:free-9:ad1-x")
            self.assertIn(f"{socket.gethostname()}:4242", task["summary"])

    def test_claim_lane_without_an_owner_is_still_accepted(self):
        """`--owner-pid` is additive. A caller that omits it gets exactly the
        pre-#209 behaviour rather than an error."""
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            proc = self._run(root, "claim-lane", "--lane", "free-9", "--token", "ad1-x")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertTrue(json.loads(proc.stdout)["claimed"])
            self.assertNotIn("[owner=", Ledger(Path(root)).get_task("ledger-claim:free-9:ad1-x")["summary"])

    def test_reap_lane_claims_is_runnable_and_frees_a_dead_owners_lane(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = self._lane(root)
            dead = subprocess.Popen([sys.executable, "-c", "pass"])
            dead.wait()
            self.assertEqual(0, self._run(
                root, "claim-lane", "--lane", "free-9", "--token", "ad1-x", "--owner-pid", str(dead.pid),
            ).returncode)
            self.assertFalse(ledger.lane_available("free-9"))

            proc = self._run(root, "reap-lane-claims")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertEqual(1, json.loads(proc.stdout)["count"])
            self.assertTrue(Ledger(Path(root)).lane_available("free-9"))

    def test_reap_lane_claims_leaves_a_live_owners_lane_alone(self):
        with tempfile.TemporaryDirectory() as root:
            ledger = self._lane(root)
            self.assertEqual(0, self._run(
                root, "claim-lane", "--lane", "free-9", "--token", "ad1-x", "--owner-pid", str(os.getpid()),
            ).returncode)
            proc = self._run(root, "reap-lane-claims")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertEqual(0, json.loads(proc.stdout)["count"])
            self.assertFalse(ledger.lane_available("free-9"))

    def test_commit_lane_claim_is_runnable_and_puts_the_claim_out_of_reap_range(self):
        """agent-dotfiles#209 round 2. `dispatch.sh` calls this immediately
        before the `send-keys Enter` that submits the brief, so from the CLI's
        side the contract is: after it returns committed, a reap that finds
        the owner provably dead must still leave the lane held."""
        with tempfile.TemporaryDirectory() as root:
            ledger = self._lane(root)
            dead = subprocess.Popen([sys.executable, "-c", "pass"])
            dead.wait()
            self.assertEqual(0, self._run(
                root, "claim-lane", "--lane", "free-9", "--token", "ad1-x", "--owner-pid", str(dead.pid),
            ).returncode)

            proc = self._run(root, "commit-lane-claim", "--lane", "free-9", "--token", "ad1-x")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertTrue(json.loads(proc.stdout)["committed"])

            reap = self._run(root, "reap-lane-claims")
            self.assertEqual(0, reap.returncode, reap.stderr)
            self.assertEqual(0, json.loads(reap.stdout)["count"])
            self.assertFalse(ledger.lane_available("free-9"))

            # ...and the scoped release the trap uses will not free it either,
            # and says so: an operator following the refusal's recovery steps
            # must not read `released: true` off a command that freed nothing.
            rel = self._run(root, "release-lane-claim", "--lane", "free-9", "--token", "ad1-x")
            self.assertEqual(0, rel.returncode, rel.stderr)
            self.assertFalse(json.loads(rel.stdout)["released"])
            self.assertIn("cancel-open-task", json.loads(rel.stdout)["hint"])
            self.assertFalse(Ledger(Path(root)).lane_available("free-9"))

    def test_release_lane_claim_reports_true_only_when_it_freed_something(self):
        """The control: the same command on a claim that is still only a
        reservation reports the release it actually performed."""
        with tempfile.TemporaryDirectory() as root:
            ledger = self._lane(root)
            self.assertEqual(0, self._run(
                root, "claim-lane", "--lane", "free-9", "--token", "ad1-x", "--owner-pid", "4242",
            ).returncode)
            rel = self._run(root, "release-lane-claim", "--lane", "free-9", "--token", "ad1-x")
            self.assertEqual(0, rel.returncode, rel.stderr)
            self.assertTrue(json.loads(rel.stdout)["released"])
            self.assertTrue(ledger.lane_available("free-9"))

    def test_release_lane_claim_names_no_claim_when_the_token_was_never_used(self):
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            rel = self._run(root, "release-lane-claim", "--lane", "free-9", "--token", "never-claimed")
            self.assertEqual(0, rel.returncode, rel.stderr)
            body = json.loads(rel.stdout)
            self.assertFalse(body["released"])
            self.assertIn("no claim by this token exists", body["hint"])

    def test_release_lane_claim_on_a_reused_token_says_it_is_already_closed(self):
        """agent-supervisor#174. `release-lane-claim` used to call this "no
        reserved claim matched" -- true of the DELETE, but read by an
        operator as "there is nothing here", when the token it names was
        exactly the one `dispatch.sh` reported as blocking the lane. Once the
        row is provably closed, the message must say that instead of leaving
        the operator to guess whether cancel-open-task still applies."""
        with tempfile.TemporaryDirectory() as root:
            ledger = self._lane(root)
            self.assertEqual(0, self._run(
                root, "claim-lane", "--lane", "free-9", "--token", "as163-rev168", "--owner-pid", "4242",
            ).returncode)
            self.assertTrue(self._run(
                root, "commit-lane-claim", "--lane", "free-9", "--token", "as163-rev168",
            ).returncode == 0)
            ledger.record_dispatch(
                lane="free-9", pane_id="%9", nonce="nonce-9-b", harness="claude",
                repo=root, server_id="socket:1", session_id="$0", command="claude.exe",
                task_id="ad163-rev168", source_kind="issue", source_url="https://example/163",
                source_ref="163", summary="review #168", source_state="open",
                evidence=["claimed by dispatch.sh for lane free-9"],
            )
            self.assertEqual(
                "cancelled", Ledger(Path(root)).get_task("ledger-claim:free-9:as163-rev168")["status"]
            )
            ledger.complete("ad163-rev168", b"ok", pane_nonce="nonce-9-b")

            rel = self._run(root, "release-lane-claim", "--lane", "free-9", "--token", "as163-rev168")
            self.assertEqual(0, rel.returncode, rel.stderr)
            body = json.loads(rel.stdout)
            self.assertFalse(body["released"])
            self.assertIn("already closed", body["hint"])
            self.assertNotIn("cancel-open-task", body["hint"])

            # And the recovery it points at actually works: the lane, freed
            # by nothing this command did, is still claimable under the same
            # reused token (the `claim_lane` half of #174's fix).
            self.assertTrue(Ledger(Path(root)).lane_available("free-9"))
            retry = self._run(root, "claim-lane", "--lane", "free-9", "--token", "as163-rev168")
            self.assertEqual(0, retry.returncode, retry.stderr)
            self.assertTrue(json.loads(retry.stdout)["claimed"])

    def test_commit_lane_claim_refuses_a_claim_that_was_never_made(self):
        """`dispatch.sh` treats a non-committed result as fatal and does not
        send, so this refusal has to be visible in the exit-0 JSON rather than
        implied by an absence."""
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            proc = self._run(root, "commit-lane-claim", "--lane", "free-9", "--token", "never-claimed")
            self.assertEqual(0, proc.returncode, proc.stderr)
            value = json.loads(proc.stdout)
            self.assertFalse(value["committed"])
            self.assertEqual("missing", value["reason"])
            self.assertTrue(Ledger(Path(root)).lane_available("free-9"))

    def test_cancel_open_task_is_the_operators_hammer_and_is_runnable(self):
        """The manual half of the recovery `dispatch.sh`'s refusal now names:
        it clears whatever outstanding task holds the lane, including a
        `ledger-hold:` row the automatic reap deliberately will not touch."""
        with tempfile.TemporaryDirectory() as root:
            ledger = self._lane(root)
            ledger.mark_lane_held("free-9", note="ledger record failed")
            self.assertFalse(ledger.lane_available("free-9"))
            self.assertEqual(0, self._run(root, "reap-lane-claims").returncode)
            self.assertFalse(ledger.lane_available("free-9"), "the reap must not touch a deliberate hold")

            proc = self._run(root, "cancel-open-task", "--lane", "free-9", "--abandoned")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertEqual("cancelled", json.loads(proc.stdout)["cancelled"]["status"])
            self.assertIsNone(json.loads(proc.stdout)["cancelled"]["result_path"])
            self.assertTrue(Ledger(Path(root)).lane_available("free-9"))

    def test_cancel_open_task_on_an_already_free_lane_is_a_no_op(self):
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            proc = self._run(root, "cancel-open-task", "--lane", "free-9", "--abandoned")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertIsNone(json.loads(proc.stdout)["cancelled"])

    def test_cancel_open_task_requires_saying_which(self):
        """agent-supervisor#649: no default. A caller that supplies none of
        --result-file/--note/--abandoned must be refused, not silently
        treated as an abandonment -- that silent default is exactly how all
        951 of the ledger's cancelled rows ended up with a null result."""
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            proc = self._run(root, "cancel-open-task", "--lane", "free-9")
            self.assertNotEqual(0, proc.returncode)
            self.assertIn("requires --result-file, --note, or --abandoned", proc.stderr)

    def test_cancel_open_task_refuses_a_result_and_abandoned_together(self):
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            proc = self._run(root, "cancel-open-task", "--lane", "free-9", "--note", "shipped", "--abandoned")
            self.assertNotEqual(0, proc.returncode)
            self.assertIn("exactly one of", proc.stderr)

    def _record_dispatch(self, root, *, lane, task, issue=649, pr=None):
        output = io.StringIO()
        argv = [
            "--state-dir", root, "record-dispatch",
            "--lane", lane, "--task", task, "--summary", f"{task} summary",
            "--pane-id", "%9", "--pane-path", root, "--command", "claude.exe",
            "--server-id", "socket:1", "--session-id", "$0",
            "--issue", str(issue), "--github", "jonhill90/agent-supervisor",
        ]
        if pr is not None:
            argv += ["--pr", str(pr)]
        with contextlib.redirect_stdout(output):
            rc = cli.main(argv)
        self.assertEqual(0, rc, output.getvalue())

    def test_cancel_open_task_can_record_a_result_via_note(self):
        """agent-supervisor#649's core fix: a cancel that carries a result
        must persist it -- with a hash, the same as `record-completion`
        writes -- rather than looking identical to an abandonment."""
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            self._record_dispatch(root, lane="free-9", task="as649-shipped", pr=649)
            proc = self._run(root, "cancel-open-task", "--lane", "free-9", "--note", "PR #649 merged; pane was gone")
            self.assertEqual(0, proc.returncode, proc.stderr)
            cancelled = json.loads(proc.stdout)["cancelled"]
            self.assertEqual("cancelled", cancelled["status"])
            self.assertIsNotNone(cancelled["result_path"])
            self.assertIsNotNone(cancelled["result_sha256"])
            self.assertEqual(
                cancelled["result_sha256"],
                hashlib.sha256(b"PR #649 merged; pane was gone").hexdigest(),
            )
            # And the regression the issue named: a PR-scoped task whose PR
            # merged never ends up cancelled with no result -- it is not
            # even reachable through this call without one of the three
            # flags, and this one has a result.
            missing = self._run(root, "missing-results")
            self.assertEqual(0, missing.returncode, missing.stderr)
            self.assertNotIn(
                "as649-shipped", [task["id"] for task in json.loads(missing.stdout)["tasks"]]
            )

    def test_cancel_open_task_with_result_file_writes_the_files_bytes(self):
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            self._record_dispatch(root, lane="free-9", task="as649-file-result")
            result_file = Path(root) / "result.md"
            result_file.write_text("# delivered before the pane went away\n")
            proc = self._run(root, "cancel-open-task", "--lane", "free-9", "--result-file", str(result_file))
            self.assertEqual(0, proc.returncode, proc.stderr)
            cancelled = json.loads(proc.stdout)["cancelled"]
            self.assertEqual(
                hashlib.sha256(result_file.read_bytes()).hexdigest(), cancelled["result_sha256"]
            )

    def test_missing_results_surfaces_an_abandoned_cancel_but_not_a_result_bearing_one(self):
        """agent-supervisor#649: the discoverability half of the fix -- a
        terminal row with no result must be findable without knowing the
        schema."""
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            self._record_dispatch(root, lane="free-9", task="as649-abandoned")
            proc = self._run(root, "cancel-open-task", "--lane", "free-9", "--abandoned")
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertEqual("cancelled", json.loads(proc.stdout)["cancelled"]["status"])

            missing = self._run(root, "missing-results")
            self.assertEqual(0, missing.returncode, missing.stderr)
            self.assertIn("as649-abandoned", [task["id"] for task in json.loads(missing.stdout)["tasks"]])

    def test_cancel_open_task_on_an_unknown_lane_errors_distinctly(self):
        """agent-supervisor#17. `cancel-open-task --lane 2` against a lane id
        that does not exist used to return `{"cancelled":null}` -- byte
        identical to the no-op above, a real lane with nothing outstanding.
        An unknown lane and an empty lane are different facts: an operator
        reading `null` as "nothing to cancel" on a typo'd id has no signal
        anything went wrong. It must now error, and the error must not be
        exit-0 JSON indistinguishable from the no-op case above."""
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            proc = self._run(root, "cancel-open-task", "--lane", "no-such-lane", "--abandoned")
            self.assertNotEqual(0, proc.returncode)
            self.assertIn("unknown lane", proc.stderr)
            # The two refusals must be told apart, not just both non-null.
            noop = self._run(root, "cancel-open-task", "--lane", "free-9", "--abandoned")
            self.assertEqual(0, noop.returncode, noop.stderr)
            self.assertNotEqual(proc.stdout, noop.stdout)

    def test_record_completion_by_lane_resolves_a_live_claim_row(self):
        """agent-supervisor#36 (second issue comment): the codex harness's
        completions land as a `ledger-claim:<lane>:<token>` row, not a task
        row, so the only recovery verb that used to work on one was
        `cancel-open-task` -- which records a genuinely completed review as
        cancelled.
        `--lane` alone must resolve that row and mark it complete, mirroring
        `cancel_open_task`'s own "whatever owns this lane" lookup but writing
        the honest outcome instead of a cancellation."""
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            self.assertEqual(0, self._run(
                root, "claim-lane", "--lane", "free-9", "--token", "ad1-review", "--owner-pid", "4242",
            ).returncode)
            self.assertEqual(0, self._run(
                root, "commit-lane-claim", "--lane", "free-9", "--token", "ad1-review",
            ).returncode)
            proc = self._run(root, "record-completion", "--lane", "free-9", "--note", "review posted, PR merged")
            self.assertEqual(0, proc.returncode, proc.stderr)
            value = json.loads(proc.stdout)
            self.assertEqual("ledger-claim:free-9:ad1-review", value["id"])
            self.assertEqual("complete", value["status"])
            self.assertTrue(Ledger(Path(root)).lane_available("free-9"))

    def test_record_completion_by_task_and_lane_resolves_the_matching_claim_row(self):
        """The combined form: an operator who read the bare token off the
        pane (as the issue's operator did) can pass it back with `--lane`
        rather than needing to type the full `ledger-claim:` id."""
        with tempfile.TemporaryDirectory() as root:
            self._lane(root)
            self.assertEqual(0, self._run(
                root, "claim-lane", "--lane", "free-9", "--token", "ad1-review", "--owner-pid", "4242",
            ).returncode)
            self.assertEqual(0, self._run(
                root, "commit-lane-claim", "--lane", "free-9", "--token", "ad1-review",
            ).returncode)
            proc = self._run(
                root, "record-completion", "--task", "ad1-review", "--lane", "free-9", "--note", "done",
            )
            self.assertEqual(0, proc.returncode, proc.stderr)
            self.assertEqual("ledger-claim:free-9:ad1-review", json.loads(proc.stdout)["id"])

    def test_record_completion_without_task_or_lane_raises(self):
        with tempfile.TemporaryDirectory() as root:
            proc = self._run(root, "record-completion", "--note", "done")
            self.assertNotEqual(0, proc.returncode)
            self.assertIn("requires --task or --lane", proc.stderr)
