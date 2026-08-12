import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from verdict import (  # noqa: E402
    GithubReviewVerdictSource,
    LedgerVerdictSource,
    build_source,
    main,
    resolve,
)


REPO = "jonhill90/agent-dotfiles"


class GithubReviewVerdictSourceTests(unittest.TestCase):
    def test_no_reviews_reads_none(self):
        source = GithubReviewVerdictSource(runner=lambda cmd: '{"reviews":[]}')
        self.assertEqual(source.verdict(repo=REPO, number=1), {"verdict": "none", "detail": ""})

    def test_changes_requested_reads_rejected(self):
        source = GithubReviewVerdictSource(
            runner=lambda cmd: '{"reviews":[{"state":"CHANGES_REQUESTED"}]}'
        )
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "rejected")

    def test_approved_reads_approved(self):
        source = GithubReviewVerdictSource(runner=lambda cmd: '{"reviews":[{"state":"APPROVED"}]}')
        result = source.verdict(repo=REPO, number=1)
        self.assertEqual(result["verdict"], "approved")

    def test_rejection_wins_over_approval(self):
        source = GithubReviewVerdictSource(
            runner=lambda cmd: '{"reviews":[{"state":"APPROVED"},{"state":"CHANGES_REQUESTED"}]}'
        )
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "rejected")

    def test_regression_comment_prose_is_never_consulted(self):
        """agent-dotfiles#203: the field this replaces regex-matched the last
        comment's prose and read "I cannot approve this, it is unsafe." as an
        APPROVE. This source only ever looks at each review's `state` field --
        a body containing "approve" on a COMMENTED (non-deciding) review must
        not flip the verdict."""
        payload = (
            '{"reviews":['
            '{"state":"COMMENTED","body":"I cannot approve this, it is unsafe."},'
            '{"state":"COMMENTED","body":"Verdict: APPROVE"}'
            "]}"
        )
        source = GithubReviewVerdictSource(runner=lambda cmd: payload)
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "none")

    def test_runner_failure_fails_closed_to_unknown(self):
        def raiser(cmd):
            raise RuntimeError("gh: command failed")

        source = GithubReviewVerdictSource(runner=raiser)
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "unknown")

    def test_malformed_json_fails_closed_to_unknown(self):
        source = GithubReviewVerdictSource(runner=lambda cmd: "not json")
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "unknown")

    def test_reviews_not_a_list_fails_closed_to_unknown(self):
        source = GithubReviewVerdictSource(runner=lambda cmd: '{"reviews": "oops"}')
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "unknown")


class LedgerVerdictSourceTests(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.ledger = Ledger(self._tmp.name, clock=lambda: 1000)
        self.source = LedgerVerdictSource(self.ledger)

    def test_never_recorded_reads_none(self):
        self.assertEqual(self.source.verdict(repo=REPO, number=1), {"verdict": "none", "detail": ""})

    def test_recorded_approval_reads_approved(self):
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="approved", head_sha="a" * 40, reviewer="lane-1"
        )
        self.assertEqual(self.source.verdict(repo=REPO, number=1)["verdict"], "approved")

    def test_recorded_rejection_reads_rejected(self):
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="rejected", head_sha="a" * 40, reviewer="lane-1"
        )
        self.assertEqual(self.source.verdict(repo=REPO, number=1)["verdict"], "rejected")

    def test_re_recording_overwrites_the_prior_verdict(self):
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="rejected", head_sha="a" * 40, reviewer="lane-1"
        )
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="approved", head_sha="b" * 40, reviewer="lane-2"
        )
        self.assertEqual(self.source.verdict(repo=REPO, number=1)["verdict"], "approved")

    def test_a_different_pr_number_is_unaffected(self):
        self.ledger.record_pr_verdict(
            repo=REPO, number=1, verdict="rejected", head_sha="a" * 40, reviewer="lane-1"
        )
        self.assertEqual(self.source.verdict(repo=REPO, number=2)["verdict"], "none")

    def test_broken_ledger_fails_closed_to_unknown(self):
        class BrokenLedger:
            def get_pr_verdict(self, *, repo, number):
                raise RuntimeError("sqlite3.DatabaseError: disk image is malformed")

        source = LedgerVerdictSource(BrokenLedger())
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "unknown")

    def test_invalid_verdict_value_row_fails_closed_to_unknown(self):
        class CorruptLedger:
            def get_pr_verdict(self, *, repo, number):
                return {"verdict": "maybe", "reviewer": "lane-1", "updated_at": 1}

        source = LedgerVerdictSource(CorruptLedger())
        self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "unknown")

    def test_record_rejects_bad_verdict_value(self):
        with self.assertRaises(ValueError):
            self.ledger.record_pr_verdict(
                repo=REPO, number=1, verdict="maybe", head_sha="a" * 40, reviewer="lane-1"
            )


class ResolveTests(unittest.TestCase):
    class Stub:
        def __init__(self, result):
            self.result = result

        def verdict(self, *, repo, number):
            return self.result

    def test_first_decisive_source_wins(self):
        stubs = {"a": self.Stub({"verdict": "rejected", "detail": ""}), "b": self.Stub({"verdict": "approved", "detail": ""})}
        with self._patched(stubs):
            self.assertEqual(resolve(["a", "b"], state_dir="unused", repo=REPO, number=1)["verdict"], "rejected")

    def test_later_decisive_source_used_when_earlier_is_none(self):
        stubs = {"a": self.Stub({"verdict": "none", "detail": ""}), "b": self.Stub({"verdict": "approved", "detail": ""})}
        with self._patched(stubs):
            self.assertEqual(resolve(["a", "b"], state_dir="unused", repo=REPO, number=1)["verdict"], "approved")

    def test_unknown_is_not_masked_by_a_later_none(self):
        """Fail-closed across sources: an error from one adapter must not be
        silently swallowed just because another configured source has
        nothing on record."""
        stubs = {"a": self.Stub({"verdict": "unknown", "detail": "broken"}), "b": self.Stub({"verdict": "none", "detail": ""})}
        with self._patched(stubs):
            self.assertEqual(resolve(["a", "b"], state_dir="unused", repo=REPO, number=1)["verdict"], "unknown")

    def test_all_none_reads_none(self):
        stubs = {"a": self.Stub({"verdict": "none", "detail": ""}), "b": self.Stub({"verdict": "none", "detail": ""})}
        with self._patched(stubs):
            self.assertEqual(resolve(["a", "b"], state_dir="unused", repo=REPO, number=1)["verdict"], "none")

    def test_unknown_source_name_fails_closed_to_unknown(self):
        result = resolve(["not-a-real-source"], state_dir="unused", repo=REPO, number=1)
        self.assertEqual(result["verdict"], "unknown")

    def _patched(self, stubs):
        import verdict as verdict_module

        return _PatchSources(verdict_module, stubs)


class _PatchSources:
    def __init__(self, module, stubs):
        self.module = module
        self.stubs = stubs
        self._original = module.build_source

    def __enter__(self):
        def fake_build_source(name, *, state_dir):
            if name not in self.stubs:
                raise ValueError(f"unknown verdict source: {name}")
            return self.stubs[name]

        self.module.build_source = fake_build_source
        return self

    def __exit__(self, *exc):
        self.module.build_source = self._original


class BuildSourceTests(unittest.TestCase):
    def test_unknown_name_raises(self):
        with self.assertRaises(ValueError):
            build_source("not-a-real-source", state_dir="unused")

    def test_ledger_name_resolves_to_a_working_ledger_source(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = build_source("ledger", state_dir=tmp)
            self.assertIsInstance(source, LedgerVerdictSource)
            self.assertEqual(source.verdict(repo=REPO, number=1)["verdict"], "none")

    def test_github_name_resolves_to_a_github_source(self):
        source = build_source("github", state_dir="unused")
        self.assertIsInstance(source, GithubReviewVerdictSource)


class CliTests(unittest.TestCase):
    def test_record_then_get_roundtrips_through_the_ledger(self):
        with tempfile.TemporaryDirectory() as tmp:
            rc = main(
                [
                    "--state-dir",
                    tmp,
                    "record",
                    "--repo",
                    REPO,
                    "--number",
                    "9",
                    "--verdict",
                    "rejected",
                    "--head-sha",
                    "a" * 40,
                    "--reviewer",
                    "lane-3",
                ]
            )
            self.assertEqual(rc, 0)

            import io
            import contextlib
            import json

            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                rc = main(
                    ["--state-dir", tmp, "get", "--repo", REPO, "--number", "9", "--source", "ledger"]
                )
            self.assertEqual(rc, 0)
            self.assertEqual(json.loads(out.getvalue())["verdict"], "rejected")

    def test_get_never_raises_even_for_a_broken_state_dir(self):
        """main() must always produce a well-formed unknown verdict, never a
        traceback a caller could mistake for a hung or crashed process."""
        import io
        import contextlib
        import json

        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            rc = main(
                [
                    "--state-dir",
                    "/nonexistent/definitely/not/writable/anywhere",
                    "get",
                    "--repo",
                    REPO,
                    "--number",
                    "1",
                    "--source",
                    "ledger",
                ]
            )
        self.assertEqual(rc, 0)
        self.assertEqual(json.loads(out.getvalue())["verdict"], "unknown")


if __name__ == "__main__":
    unittest.main()
