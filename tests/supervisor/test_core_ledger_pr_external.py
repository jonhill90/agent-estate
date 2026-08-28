import subprocess
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class PrExternalTest(LedgerTestBase):
    def test_mark_pr_external_round_trips_through_get_pr_external(self):
        """agent-supervisor#308 item 3: "no lane contributed" as a
        first-class, recordable fact distinct from "unresolvable" -- the
        #316/#301/#300 shape, a PR authored outside the lane system
        entirely."""
        self.assertIsNone(self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="316"))

        self.ledger.mark_pr_external(
            repo="acme/agent-supervisor", pr_number="316", note="authored directly, no lane ever dispatched",
            chain_verified=True,
        )
        found = self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="316")
        self.assertIsNotNone(found)
        self.assertEqual("authored directly, no lane ever dispatched", found["note"])

        # A second marking corrects (idempotent), not duplicates.
        self.ledger.mark_pr_external(
            repo="acme/agent-supervisor", pr_number="316", note="updated note", chain_verified=True
        )
        found = self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="316")
        self.assertEqual("updated note", found["note"])

        # A different, never-marked PR stays unknown -- marking is per-PR,
        # not a global switch.
        self.assertIsNone(self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="301"))

    def test_mark_pr_external_refuses_a_pr_with_an_explicit_authorship_record(self):
        """agent-supervisor#308 item 3 / #321's own review, item 5: the
        laundering gate. `mark_pr_external` must not accept a caller's word
        alone -- it independently refuses when the ledger already records
        `record_pr_for_task`'s explicit "task X opened PR N" fact, the most
        direct evidence this method can check with no external process
        (`gh`/`git`, which only the shell wrapper `mark-pr-external.sh`
        reaches). Otherwise the contributing lane itself could call this and
        erase its own record, then have any lane -- including itself --
        review the PR it just laundered."""
        self.ledger.record_dispatch(
            lane="free-3", pane_id="%3", nonce="nonce-3", harness="claude", repo="/repo/free-3",
            server_id="server-a", session_id="$3", command="claude.exe",
            task_id="as308-original", source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/308",
            source_ref="308", summary="#308 original fix", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"], status_marker=None,
        )
        self.ledger.record_pr_for_task(task_id="as308-original", repo="acme/agent-supervisor", pr_number="316")

        with self.assertRaises(ValueError) as caught:
            self.ledger.mark_pr_external(
                repo="acme/agent-supervisor", pr_number="316", note="trying to launder my own PR",
                chain_verified=True,
            )
        self.assertIn("as308-original", str(caught.exception))
        self.assertIn("free-3", str(caught.exception))
        self.assertIsNone(
            self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="316"),
            "the refused call must not have written the row",
        )

    def test_mark_pr_external_refuses_a_pr_with_a_pull_scoped_contributor(self):
        """Same gate, the other ledger-only source it can check without
        `gh`/`git`: a task dispatched DIRECTLY against this PR
        (`source_kind='pull'`, `get_contributor_tasks_for_pr`) -- the #302
        shape. A review or fix-pass task dispatched with `--pr <N>` is a
        real, structured contributor record; marking that PR external must
        not be allowed to erase it."""
        self.ledger.record_dispatch(
            lane="free-3", pane_id="%3", nonce="nonce-3", harness="claude", repo="/repo/free-3",
            server_id="server-a", session_id="$3", command="claude.exe",
            task_id="as302-fix1", source_kind="pull",
            source_url="https://github.com/jonhill90/agent-supervisor/pull/302",
            source_ref="302", summary="fix pass on PR #302", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 302"], status_marker=None,
        )

        with self.assertRaises(ValueError) as caught:
            self.ledger.mark_pr_external(
                repo="acme/agent-supervisor", pr_number="302", note="trying to launder my own PR",
                chain_verified=True,
            )
        self.assertIn("as302-fix1", str(caught.exception))
        self.assertIsNone(self.ledger.get_pr_external(repo="acme/agent-supervisor", pr_number="302"))

    def test_mark_pr_external_bypass_via_cli_direct_call_is_refused(self):
        """PR #331 review, finding 2: `mark_pr_external`'s own backstop only
        checks two of the five resolution paths (`get_task_for_pr_number`,
        `get_contributor_tasks_for_pr`) -- it never consults issue-linkage,
        which needs `gh` and so cannot live here. `record_pr_for_task` is
        only written by `lane-done.sh` at completion, so an ORDINARY,
        still-in-progress, issue-scoped task (the most common dispatch
        shape) has no PR-keyed row yet and no pull-scoped `source_tasks` row
        either -- neither backstop check can see it. Before this fix, a lane
        calling `python3 cli.py mark-pr-external` directly (bypassing
        `mark-pr-external.sh` and its `gh`-based issue-linkage check
        entirely) sailed straight through for exactly this shape, reproduced
        in the #331 review. `chain_verified` now gates this regardless of
        contributor shape -- a direct call that never went through the
        exhaustive chain has no way to claim it did.
        """
        self.ledger.record_dispatch(
            lane="t:2", pane_id="%2", nonce="nonce-t2", harness="claude", repo="/repo/t2",
            server_id="server-a", session_id="$2", command="claude.exe",
            task_id="ad40-fix", source_kind="issue",
            source_url="https://github.com/acme/agent-dotfiles/issues/40",
            source_ref="40", summary="genuine fix for #40", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane t:2"], status_marker=None,
        )
        # Neither backstop path can see this contributor: no record_pr_for_task
        # row (only lane-done.sh writes that, at completion) and no
        # source_kind='pull' row (this task was dispatched by issue, not by
        # --pr). The bug this test guards against is proven by the fact that
        # BOTH checks below pass -- the gap is real, not a setup mistake.
        self.assertIsNone(self.ledger.get_task_for_pr_number(repo="acme/agent-dotfiles", pr_number="500"))
        self.assertEqual([], self.ledger.get_contributor_tasks_for_pr("500"))

        proc = subprocess.run(
            [
                sys.executable, str(SUPERVISOR_DIR / "cli.py"),
                "--state-dir", self.tempdir.name,
                "mark-pr-external", "--repo", "acme/agent-dotfiles", "--pr", "500",
                "--note", "bypass attempt -- direct cli.py call, no chain run",
            ],
            capture_output=True, text=True, timeout=30,
        )
        self.assertNotEqual(0, proc.returncode, proc.stdout + proc.stderr)
        self.assertIn("chain_verified", proc.stdout + proc.stderr)
        self.assertIsNone(
            self.ledger.get_pr_external(repo="acme/agent-dotfiles", pr_number="500"),
            "the bypass attempt must not have written the row",
        )
