import subprocess
import sys
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from tests.supervisor.test_core_helpers import LedgerTestBase  # noqa: E402


class PrDirectorAuthoredTest(LedgerTestBase):
    """agent-estate#741: "authored directly by the Director, verified, no
    lane contributed" as its OWN first-class, recordable fact -- distinct
    from `pr_external_authorship`, because the Director is an internal
    estate actor register-lane-self.sh structurally excludes from ever
    becoming a lane row, not one authored outside the lane system entirely.
    """

    def test_mark_pr_director_authored_round_trips_through_get_pr_director_authored(self):
        self.assertIsNone(self.ledger.get_pr_director_authored(repo="acme/agent-estate", pr_number="748"))

        self.ledger.mark_pr_director_authored(
            repo="acme/agent-estate", pr_number="748", note="director-authored directly, no lane ever dispatched",
            chain_verified=True,
        )
        found = self.ledger.get_pr_director_authored(repo="acme/agent-estate", pr_number="748")
        self.assertIsNotNone(found)
        self.assertEqual("director-authored directly, no lane ever dispatched", found["note"])

        # A second marking corrects (idempotent), not duplicates.
        self.ledger.mark_pr_director_authored(
            repo="acme/agent-estate", pr_number="748", note="updated note", chain_verified=True
        )
        found = self.ledger.get_pr_director_authored(repo="acme/agent-estate", pr_number="748")
        self.assertEqual("updated note", found["note"])

        # A different, never-marked PR stays unknown -- marking is per-PR,
        # not a global switch.
        self.assertIsNone(self.ledger.get_pr_director_authored(repo="acme/agent-estate", pr_number="745"))

    def test_mark_pr_director_authored_refuses_without_chain_verified(self):
        """Same #331-review gate mark_pr_external carries: an unmissable
        claim the exhaustive chain actually ran, not an unchecked default."""
        with self.assertRaises(ValueError) as caught:
            self.ledger.mark_pr_director_authored(
                repo="acme/agent-estate", pr_number="748", note="no chain claim at all",
            )
        self.assertIn("chain_verified", str(caught.exception))
        self.assertIsNone(self.ledger.get_pr_director_authored(repo="acme/agent-estate", pr_number="748"))

    def test_mark_pr_director_authored_refuses_a_pr_with_an_explicit_authorship_record(self):
        """The laundering gate, mirrored from mark_pr_external: must not
        accept a caller's word alone when the ledger already records
        `record_pr_for_task`'s explicit "task X opened PR N" fact."""
        self.ledger.record_dispatch(
            lane="free-3", pane_id="%3", nonce="nonce-3", harness="claude", repo="/repo/free-3",
            server_id="server-a", session_id="$3", command="claude.exe",
            task_id="ae745-original", source_kind="issue",
            source_url="https://github.com/jonhill90/agent-estate/issues/745",
            source_ref="745", summary="#745 original fix", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3"], status_marker=None,
        )
        self.ledger.record_pr_for_task(task_id="ae745-original", repo="acme/agent-estate", pr_number="745")

        with self.assertRaises(ValueError) as caught:
            self.ledger.mark_pr_director_authored(
                repo="acme/agent-estate", pr_number="745", note="trying to launder a real contributor",
                chain_verified=True,
            )
        self.assertIn("ae745-original", str(caught.exception))
        self.assertIn("free-3", str(caught.exception))
        self.assertIsNone(
            self.ledger.get_pr_director_authored(repo="acme/agent-estate", pr_number="745"),
            "the refused call must not have written the row",
        )

    def test_mark_pr_director_authored_refuses_a_pr_with_a_pull_scoped_contributor(self):
        """Same gate, the other ledger-only source it can check without
        gh/git: a task dispatched DIRECTLY against this PR
        (source_kind='pull', get_contributor_tasks_for_pr)."""
        self.ledger.record_dispatch(
            lane="free-3", pane_id="%3", nonce="nonce-3", harness="claude", repo="/repo/free-3",
            server_id="server-a", session_id="$3", command="claude.exe",
            task_id="ae745-fix1", source_kind="pull",
            source_url="https://github.com/jonhill90/agent-estate/pull/745",
            source_ref="745", summary="fix pass on PR #745", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-3", "pr: 745"], status_marker=None,
        )

        with self.assertRaises(ValueError) as caught:
            self.ledger.mark_pr_director_authored(
                repo="acme/agent-estate", pr_number="745", note="trying to launder a real contributor",
                chain_verified=True,
            )
        self.assertIn("ae745-fix1", str(caught.exception))
        self.assertIsNone(self.ledger.get_pr_director_authored(repo="acme/agent-estate", pr_number="745"))

    def test_mark_pr_director_authored_bypass_via_cli_direct_call_is_refused(self):
        """PR #331 review, finding 2's fix, mirrored: a direct
        `cli.py mark-pr-director-authored` call (bypassing
        mark-pr-director-authored.sh and its gh-based issue-linkage check
        entirely) must be refused for lack of --chain-verified, for the
        same ordinary issue-scoped-task shape neither ledger-only backstop
        can see."""
        self.ledger.record_dispatch(
            lane="t:2", pane_id="%2", nonce="nonce-t2", harness="claude", repo="/repo/t2",
            server_id="server-a", session_id="$2", command="claude.exe",
            task_id="ae748-fix", source_kind="issue",
            source_url="https://github.com/acme/agent-estate/issues/748",
            source_ref="748", summary="genuine fix for #748", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane t:2"], status_marker=None,
        )
        self.assertIsNone(self.ledger.get_task_for_pr_number(repo="acme/agent-estate", pr_number="900"))
        self.assertEqual([], self.ledger.get_contributor_tasks_for_pr("900"))

        proc = subprocess.run(
            [
                sys.executable, str(SUPERVISOR_DIR / "cli.py"),
                "--state-dir", self.tempdir.name,
                "mark-pr-director-authored", "--repo", "acme/agent-estate", "--pr", "900",
                "--note", "bypass attempt -- direct cli.py call, no chain run",
            ],
            capture_output=True, text=True, timeout=30,
        )
        self.assertNotEqual(0, proc.returncode, proc.stdout + proc.stderr)
        self.assertIn("chain_verified", proc.stdout + proc.stderr)
        self.assertIsNone(
            self.ledger.get_pr_director_authored(repo="acme/agent-estate", pr_number="900"),
            "the bypass attempt must not have written the row",
        )
