import json
import sys
import tempfile
import unittest
from pathlib import Path


SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

from core import Ledger  # noqa: E402
from reconcile_sources import SourceTaskReconciler, UNKNOWN_SOURCE_STATE  # noqa: E402


class FakeRunner:
    """Records every `gh` invocation and answers from an in-memory repo map.

    `states` is `{"owner/repo": {"issue": {"109": "CLOSED", ...}, "pull": {...}}}`.
    A repo/kind key absent from `states` raises, simulating a `gh` failure
    (rate limit, auth, network) for exactly that call.
    """

    def __init__(self, states):
        self.states = states
        self.calls = []

    def __call__(self, command):
        # agent-supervisor#144: reconcile_sources.py now speaks REST
        # (`gh api --paginate repos/OWNER/REPO/issues|pulls?state=all&...`),
        # not GraphQL (`gh issue list`/`gh pr list`) -- parsed back out of
        # the endpoint path rather than off `--repo`/verb flags that no
        # longer exist.
        self.calls.append(command)
        endpoint = command[-1]
        path = endpoint.split("?", 1)[0]
        owner_repo, _, kind_path = path[len("repos/") :].rpartition("/")
        kind = "issue" if kind_path == "issues" else "pull"
        table = self.states.get(owner_repo, {}).get(kind)
        if table is None:
            raise RuntimeError(f"gh unavailable for {owner_repo} {kind}")
        return json.dumps([{"number": int(number), "state": state} for number, state in table.items()])


class SourceTaskReconcilerTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(Path(self.tempdir.name), clock=lambda: 1_000)
        for lane in ("free-1", "free-2", "free-3"):
            self.ledger.register_lane(
                lane=lane, pane_id=f"%{lane[-1]}", nonce=f"nonce-{lane[-1]}", harness="claude",
                repo=f"/repo/{lane}", server_id="server-a", session_id=f"${lane[-1]}", command="claude",
            )

    def dispatch(self, task_id, number, *, lane="free-1", repo="jonhill90/agent-supervisor"):
        """Records one dispatch the way `cli.py record_dispatch` does. Ends
        with `tasks.status == 'delivered'` -- `Ledger.record_dispatch` always
        runs assign + mark_delivery_pending + mark_delivered in one
        transaction (see its own docstring)."""
        return self.ledger.record_dispatch(
            lane=lane, pane_id=f"%{lane[-1]}", nonce=f"nonce-{lane[-1]}", harness="claude",
            repo=f"/repo/{lane}", server_id="server-a", session_id=f"${lane[-1]}", command="claude",
            task_id=task_id, source_kind="issue",
            source_url=f"https://github.com/{repo}/issues/{number}",
            source_ref=str(number), summary=f"issue #{number}", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane " + lane, f"issues: {number}"],
            status_marker=None,
        )

    def test_closed_issue_and_complete_task_both_advance(self):
        """The as#127 acceptance case: a merged/closed issue whose local task
        already completed must read closed/complete after one sweep, from a
        single batched `gh issue list` call, not 314 per-row calls."""
        dispatched = self.dispatch("as109-rail-selection", 109)
        self.ledger.complete(
            "as109-rail-selection", b"done", pane_nonce=dispatched["task"]["pane_nonce"]
        )
        runner = FakeRunner({"jonhill90/agent-supervisor": {"issue": {"109": "CLOSED"}, "pull": {}}})

        report = SourceTaskReconciler(self.ledger, runner=runner).sweep()

        source = self.ledger.get_source_task("as109-rail-selection")
        self.assertEqual("CLOSED", source["source_state"])
        self.assertEqual("complete", source["status"])
        self.assertEqual(["as109-rail-selection"], report["updated"])
        self.assertEqual(2, len(runner.calls))  # one issue + one pull call, not one per row

    def test_idempotent_second_sweep_writes_nothing(self):
        dispatched = self.dispatch("as109-rail-selection", 109)
        self.ledger.complete(
            "as109-rail-selection", b"done", pane_nonce=dispatched["task"]["pane_nonce"]
        )
        runner = FakeRunner({"jonhill90/agent-supervisor": {"issue": {"109": "CLOSED"}, "pull": {}}})
        reconciler = SourceTaskReconciler(self.ledger, runner=runner)

        first = reconciler.sweep()
        second = reconciler.sweep()

        self.assertEqual(["as109-rail-selection"], first["updated"])
        self.assertEqual([], second["updated"])
        self.assertEqual(["as109-rail-selection"], second["unchanged"])

    def test_unresolvable_source_url_stays_unknown_not_closed(self):
        """The fail-closed guard, broken deliberately: a row whose source_url
        cannot be resolved to a repo (the pre-#127 `issue:<n>@<dir>` fallback
        shape `record_dispatch` writes when no `--github` was given) must
        never be marked closed -- it must become `UNKNOWN`, explicitly, and
        stay there."""
        self.ledger.record_dispatch(
            lane="free-1", pane_id="%1", nonce="nonce-1", harness="claude",
            repo="/repo/free-1", server_id="server-a", session_id="$1", command="claude",
            task_id="ad200-mystery", source_kind="issue",
            source_url="issue:200@some-worktree", source_ref="200", summary="issue #200",
            source_state="OPEN", evidence=["claimed by dispatch.sh for lane free-1", "issues: 200"],
            status_marker=None,
        )
        runner = FakeRunner({})

        report = SourceTaskReconciler(self.ledger, runner=runner).sweep()

        source = self.ledger.get_source_task("ad200-mystery")
        self.assertEqual(UNKNOWN_SOURCE_STATE, source["source_state"])
        self.assertNotEqual("CLOSED", source["source_state"])
        self.assertIn("ad200-mystery", report["updated"])
        self.assertEqual(0, len(runner.calls))  # never even tried to call gh for it

        # A second sweep is the idempotence check for this same row.
        second = SourceTaskReconciler(self.ledger, runner=runner).sweep()
        self.assertEqual(UNKNOWN_SOURCE_STATE, self.ledger.get_source_task("ad200-mystery")["source_state"])
        self.assertIn("ad200-mystery", second["unchanged"])

    def test_gh_failure_for_one_repo_leaves_its_rows_untouched_not_unknown(self):
        """A transient `gh` failure must not overwrite a row's existing value
        with a guess -- not CLOSED, and not UNKNOWN either. It is reported as
        an error and left exactly as it was."""
        self.dispatch("as109-rail-selection", 109)
        runner = FakeRunner({})  # every repo/kind call raises

        report = SourceTaskReconciler(self.ledger, runner=runner).sweep()

        source = self.ledger.get_source_task("as109-rail-selection")
        self.assertEqual("OPEN", source["source_state"])  # untouched, not UNKNOWN
        self.assertIn("as109-rail-selection", report["unresolved"])
        self.assertEqual(2, len(report["errors"]))  # both the issue AND the pull fetch failed
        self.assertEqual("jonhill90/agent-supervisor", report["errors"][0]["repo"])

    def test_number_missing_from_gh_answer_is_left_unresolved(self):
        """`--state all` truncated by `--limit`, or a genuinely nonexistent
        number, must not be inferred either way -- even after the PR
        fallback (below) also comes up empty."""
        self.dispatch("as999-ghost", 999)
        runner = FakeRunner({"jonhill90/agent-supervisor": {"issue": {}, "pull": {}}})

        report = SourceTaskReconciler(self.ledger, runner=runner).sweep()

        source = self.ledger.get_source_task("as999-ghost")
        self.assertEqual("OPEN", source["source_state"])
        self.assertIn("as999-ghost", report["unresolved"])

    def test_a_pr_numbered_row_mislabelled_issue_resolves_through_the_pull_fallback(self):
        """agent-supervisor#127, measured against the live ledger:
        `record_dispatch` hardcodes every row's `source_kind` to `'issue'`
        regardless of whether the dispatched number was actually a PR. A
        batched `gh issue list --state all` genuinely does not return PR
        numbers (unlike `gh issue view <n>`, which answers for either) --
        without this fallback, every such row sits unresolved forever. The
        number must resolve through `gh pr list` for the same repo instead
        of being reported as merely unresolved."""
        self.dispatch("ad202-rev202", 202)  # source_kind is 'issue' -- see dispatch()
        runner = FakeRunner(
            {"jonhill90/agent-supervisor": {"issue": {}, "pull": {"202": "MERGED"}}}
        )

        report = SourceTaskReconciler(self.ledger, runner=runner).sweep()

        source = self.ledger.get_source_task("ad202-rev202")
        self.assertEqual("MERGED", source["source_state"])
        self.assertIn("ad202-rev202", report["updated"])
        self.assertNotIn("ad202-rev202", report["unresolved"])

    def test_batches_two_calls_per_repo_not_per_row(self):
        self.dispatch("as1-a", 1, lane="free-1")
        self.dispatch("as2-b", 2, lane="free-2")
        self.dispatch("as3-c", 3, lane="free-3")
        runner = FakeRunner(
            {"jonhill90/agent-supervisor": {"issue": {"1": "OPEN", "2": "CLOSED", "3": "OPEN"}, "pull": {}}}
        )

        SourceTaskReconciler(self.ledger, runner=runner).sweep()

        self.assertEqual(2, len(runner.calls))  # one issue list + one pull list, not one per row
        self.assertEqual("CLOSED", self.ledger.get_source_task("as2-b")["source_state"])

    def test_delivery_pending_local_status_maps_to_created_not_delivered(self):
        """`source_tasks.status` has no `delivery_pending` value -- mapping it
        to `delivered` would claim a send was confirmed when it was not."""
        self.ledger.reconstruct_task(
            task_id="as77-pending", source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/77",
            source_ref="77", summary="issue #77", source_state="OPEN", status="created",
            evidence=[], status_marker=None,
        )
        self.ledger.assign(task_id="as77-pending", lane="free-1", pane_nonce="nonce-1", summary="issue #77")
        self.ledger.mark_delivery_pending("as77-pending", pane_nonce="nonce-1")
        runner = FakeRunner({"jonhill90/agent-supervisor": {"issue": {"77": "OPEN"}, "pull": {}}})

        SourceTaskReconciler(self.ledger, runner=runner).sweep()

        self.assertEqual("created", self.ledger.get_source_task("as77-pending")["status"])


class GraphqlExhaustedMutationCheckTest(unittest.TestCase):
    """agent-supervisor#144 MUTATION CHECK, both directions, through the real
    `subprocess_runner` and a real (stub) `gh` binary on PATH -- not
    `FakeRunner`, which already assumes the REST command shape. Proves the
    conversion by construction: `gh issue list`/`gh pr list` fail exactly the
    way an exhausted GraphQL budget does, and the sweep must still complete
    from `gh api ...` (REST core) alone."""

    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.ledger = Ledger(Path(self.tempdir.name), clock=lambda: 1_000)
        self.ledger.register_lane(
            lane="free-1", pane_id="%1", nonce="nonce-1", harness="claude",
            repo="/repo/free-1", server_id="server-a", session_id="$1", command="claude",
        )
        self.ledger.record_dispatch(
            lane="free-1", pane_id="%1", nonce="nonce-1", harness="claude",
            repo="/repo/free-1", server_id="server-a", session_id="$1", command="claude",
            task_id="as109-rail-selection", source_kind="issue",
            source_url="https://github.com/jonhill90/agent-supervisor/issues/109",
            source_ref="109", summary="issue #109", source_state="OPEN",
            evidence=["claimed by dispatch.sh for lane free-1", "issues: 109"],
            status_marker=None,
        )

    def _stub_gh(self, tmp, *, graphql_exhausted, rest_alive):
        gh = Path(tmp) / "gh"
        gh.write_text(
            "#!/bin/bash\n"
            "set -uo pipefail\n"
            + ('case "$1" in\n  issue|pr)\n    echo \'gh: GraphQL: API rate limit exceeded\' >&2\n    exit 1 ;;\nesac\n'
               if graphql_exhausted else '')
            + (
                'case "$1 $2" in\n'
                '  "api --paginate")\n'
                '    endpoint="$3"; path="${endpoint%%\\?*}"\n'
                '    case "$path" in\n'
                '      */issues) echo \'[{"number":109,"state":"closed"}]\' ;;\n'
                '      */pulls)  echo \'[]\' ;;\n'
                '      *) exit 1 ;;\n'
                '    esac\n'
                '    exit 0 ;;\n'
                'esac\n'
                if rest_alive else 'exit 1\n'
            )
        )
        gh.chmod(0o755)
        return str(gh)

    def test_direction_1_graphql_exhausted_rest_alive(self):
        from reconcile_sources import subprocess_runner

        with tempfile.TemporaryDirectory() as bindir:
            gh_path = self._stub_gh(bindir, graphql_exhausted=True, rest_alive=True)
            report = SourceTaskReconciler(self.ledger, runner=subprocess_runner, gh_bin=gh_path).sweep()

        self.assertEqual(["as109-rail-selection"], report["updated"])
        source = self.ledger.get_source_task("as109-rail-selection")
        self.assertEqual("CLOSED", source["source_state"])
        self.assertEqual([], report["errors"])

    def test_direction_2_rest_also_unreachable(self):
        from reconcile_sources import subprocess_runner

        with tempfile.TemporaryDirectory() as bindir:
            gh_path = self._stub_gh(bindir, graphql_exhausted=True, rest_alive=False)
            report = SourceTaskReconciler(self.ledger, runner=subprocess_runner, gh_bin=gh_path).sweep()

        self.assertEqual([], report["updated"])
        self.assertTrue(report["errors"], "a REST outage must be named in errors, not silently skipped")
        source = self.ledger.get_source_task("as109-rail-selection")
        self.assertEqual("OPEN", source["source_state"])  # left untouched, not guessed


if __name__ == "__main__":
    unittest.main()
