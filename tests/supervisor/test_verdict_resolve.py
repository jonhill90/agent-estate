import sys
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from verdict import resolve  # noqa: E402

from tests.supervisor.test_verdict_helpers import REPO  # noqa: E402


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


class ResolveTests(unittest.TestCase):
    class Stub:
        def __init__(self, result):
            self.result = result

        def verdict(self, *, repo, number, head_sha=None):
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

    def test_head_sha_is_threaded_through_to_the_source(self):
        """resolve() must pass its head_sha argument on to each source's
        verdict() call, not swallow it -- a source cannot detect a stale
        review or ledger record (#218) if resolve() never gives it the
        current head to compare against."""
        received = {}

        class CapturingStub:
            def verdict(self, *, repo, number, head_sha=None):
                received["head_sha"] = head_sha
                return {"verdict": "none", "detail": ""}

        with self._patched({"a": CapturingStub()}):
            resolve(["a"], state_dir="unused", repo=REPO, number=1, head_sha="deadbeef" * 5)
        self.assertEqual(received["head_sha"], "deadbeef" * 5)

    def test_unknown_source_name_fails_closed_to_unknown(self):
        result = resolve(["not-a-real-source"], state_dir="unused", repo=REPO, number=1)
        self.assertEqual(result["verdict"], "unknown")

    def _patched(self, stubs):
        import verdict as verdict_module

        return _PatchSources(verdict_module, stubs)
