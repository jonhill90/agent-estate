import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from repair_401_reconcile_stamps import repair  # noqa: E402


class Repair401Test(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.state_dir = Path(self.tempdir.name)
        self.results_dir = self.state_dir / "results"
        self.lane_logs_dir = self.state_dir / "lane-logs"
        self.results_dir.mkdir()
        self.lane_logs_dir.mkdir()

    def write_result(self, task_id, text):
        (self.results_dir / f"{task_id}.md").write_text(text)

    def write_lane_log(self, task_id, text):
        (self.lane_logs_dir / f"{task_id}.log").write_text(text)

    def test_dry_run_leaves_files_untouched(self):
        self.write_result("ad275-fix275", "reconcile-lane-completions: ... failed, not completed\n")
        self.write_lane_log("ad275-fix275", "https://github.com/jonhill90/agent-dotfiles/pull/283\n")

        report = repair(self.results_dir, self.lane_logs_dir, apply=False)

        self.assertEqual(["ad275-fix275"], report["repaired"])
        # Nothing actually written -- dry run.
        self.assertFalse((self.results_dir / "ad275-fix275.md.pre-401-repair").exists())
        content = (self.results_dir / "ad275-fix275.md").read_text()
        self.assertIn("failed, not completed", content)

    def test_apply_backs_up_original_and_corrects_the_canonical_file(self):
        original_text = "reconcile-lane-completions: ... failed, not completed\n"
        self.write_result("ad275-fix275", original_text)
        self.write_lane_log("ad275-fix275", "https://github.com/jonhill90/agent-dotfiles/pull/283\n")

        report = repair(self.results_dir, self.lane_logs_dir, apply=True)

        self.assertEqual(["ad275-fix275"], report["repaired"])
        backup = self.results_dir / "ad275-fix275.md.pre-401-repair"
        self.assertTrue(backup.exists())
        self.assertEqual(original_text, backup.read_text())
        corrected = (self.results_dir / "ad275-fix275.md").read_text()
        self.assertIn("pull/283", corrected)
        # The retracted claim must not survive in the canonical file -- only
        # in the backup -- or agent-supervisor#401's own acceptance script
        # would still flag this specimen after the "repair".
        self.assertNotIn("failed, not completed", corrected)

    def test_no_lane_log_is_left_untouched(self):
        self.write_result("as999-no-log", "reconcile-lane-completions: ... failed, not completed\n")

        report = repair(self.results_dir, self.lane_logs_dir, apply=True)

        self.assertEqual([], report["repaired"])
        self.assertEqual(["as999-no-log"], report["no_evidence"])
        self.assertFalse((self.results_dir / "as999-no-log.md.pre-401-repair").exists())

    def test_lane_log_without_pr_url_is_left_untouched(self):
        self.write_result("as999-no-pr", "reconcile-lane-completions: ... failed, not completed\n")
        self.write_lane_log("as999-no-pr", "did some work, no PR opened\n")

        report = repair(self.results_dir, self.lane_logs_dir, apply=True)

        self.assertEqual([], report["repaired"])
        self.assertEqual(["as999-no-pr"], report["no_evidence"])

    def test_result_without_the_wrong_phrase_is_skipped_entirely(self):
        self.write_result("as1-fine", "reconcile-lane-completions: completed normally\n")

        report = repair(self.results_dir, self.lane_logs_dir, apply=True)

        self.assertEqual([], report["repaired"])
        self.assertEqual([], report["no_evidence"])
        self.assertEqual([], report["already_repaired"])

    def test_second_run_is_idempotent(self):
        """Once repaired, the canonical file no longer carries the wrong
        phrase at all -- a second run does not even re-scope it (it isn't
        `_WRONG_PHRASE`-matched any more), let alone touch its backup."""
        self.write_result("ad275-fix275", "reconcile-lane-completions: ... failed, not completed\n")
        self.write_lane_log("ad275-fix275", "https://github.com/jonhill90/agent-dotfiles/pull/283\n")
        repair(self.results_dir, self.lane_logs_dir, apply=True)
        backup_path = self.results_dir / "ad275-fix275.md.pre-401-repair"
        backup_before = backup_path.read_bytes()

        second = repair(self.results_dir, self.lane_logs_dir, apply=True)

        self.assertEqual([], second["repaired"])
        self.assertEqual([], second["already_repaired"])
        self.assertEqual(backup_before, backup_path.read_bytes())


if __name__ == "__main__":
    unittest.main()
