import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(SUPERVISOR_DIR))

import itemize_prompts  # noqa: E402
from core import Ledger  # noqa: E402


class StateDirDefaultTests(unittest.TestCase):
    """agent-estate#1362: `--state-dir`'s default, with no env override,
    resolves to the corpus, not the dead ~/.local/state/agent-dotfiles-
    supervisor directory -- the same defect class PR #1360 fixed in
    prompt_capture_hook.py and mine_prompts.py, except this script writes
    `items` (judgements), not just `prompts` rows: 709 orphaned judgements,
    53 of them hard/acted law, were sitting in the dead database invisible
    to internal/corpus.Hard() because of exactly this default. Runs the
    real CLI end to end (`--drop-noise`, the one subcommand that needs no
    transcript input and is a no-op on an empty ledger) with a fake $HOME,
    so this never risks touching the real ~/corpus, and checks WHICH
    directory the ledger actually got created under -- not merely what a
    re-derived default string would be."""

    def _run_drop_noise(self, env):
        result = subprocess.run(
            [sys.executable, str(SUPERVISOR_DIR / "itemize_prompts.py"), "--drop-noise"],
            capture_output=True,
            text=True,
            env=env,
            timeout=30,
        )
        return result

    def test_default_state_dir_is_the_corpus_not_the_dead_state_dir(self):
        with tempfile.TemporaryDirectory() as fake_home:
            result = self._run_drop_noise({"PATH": os.environ.get("PATH", ""), "HOME": fake_home})
            self.assertEqual(0, result.returncode, msg=result.stderr)

            corpus_db = Path(fake_home) / "corpus" / "ledger.sqlite3"
            dead_db = Path(fake_home) / ".local" / "state" / "agent-dotfiles-supervisor" / "ledger.sqlite3"
            self.assertTrue(corpus_db.exists(), f"expected the ledger under the corpus dir, at {corpus_db}")
            self.assertFalse(
                dead_db.exists(),
                "itemize_prompts.py must never create/touch a ledger under the dead supervisor "
                "state dir by default (agent-estate#942/#1357/#1362)",
            )

    def test_agent_corpus_dir_env_var_overrides_the_default(self):
        with tempfile.TemporaryDirectory() as fake_home, tempfile.TemporaryDirectory() as override_dir:
            env = {"PATH": os.environ.get("PATH", ""), "HOME": fake_home, "AGENT_CORPUS_DIR": override_dir}
            result = self._run_drop_noise(env)
            self.assertEqual(0, result.returncode, msg=result.stderr)

            override_db = Path(override_dir) / "ledger.sqlite3"
            dead_db = Path(fake_home) / ".local" / "state" / "agent-dotfiles-supervisor" / "ledger.sqlite3"
            self.assertTrue(override_db.exists(), f"expected the ledger under the override dir, at {override_db}")
            self.assertFalse(dead_db.exists())


class ItemizePromptsTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ledger = Ledger(self.tmp.name, clock=lambda: 1_000)
        self.ledger.record_prompt("p1", at=1_000, text_raw="make render LIVE", context="deciding render mode")
        self.ledger.record_prompt("p2", at=2_000, text_raw="unrelated turn", context="ctx")

    def test_extract_returns_only_unitemised_prompts(self):
        self.ledger.add_item("i-existing", prompt_id="p2", kind="directive", body="b", weight="hard")
        rows = itemize_prompts.extract(self.ledger)
        self.assertEqual(["p1"], [row["id"] for row in rows])

    def test_load_writes_items_from_a_judged_batch(self):
        judged = [{
            "prompt_id": "p1",
            "items": [
                {"kind": "parameter", "body": "render=LIVE", "weight": "hard"},
            ],
        }]
        written, skipped = itemize_prompts.load(judged, self.ledger)
        self.assertEqual((1, 0), (written, skipped))
        rows = self.ledger.read_prompt_view("live_parameters")
        self.assertEqual(["render=LIVE"], [row["body"] for row in rows])

    def test_load_is_idempotent_on_a_second_pass_over_the_same_judgement(self):
        judged = [{
            "prompt_id": "p1",
            "items": [{"kind": "parameter", "body": "render=LIVE", "weight": "hard"}],
        }]
        itemize_prompts.load(judged, self.ledger)
        written, skipped = itemize_prompts.load(judged, self.ledger)
        self.assertEqual((0, 1), (written, skipped))

    def test_load_never_calls_link_items(self):
        """agent-supervisor#303: conflicts reports recorded links only --
        `load()` has no code path that calls `Ledger.link_items` at all."""
        import inspect
        source = inspect.getsource(itemize_prompts.load)
        self.assertNotIn("link_items", source)


class DropNoiseTests(unittest.TestCase):
    """agent-supervisor#313: FILTER NON-JON TEXT FIRST, mechanically, no model."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ledger = Ledger(self.tmp.name, clock=lambda: 1_000)

    def test_dispatch_brief_is_dropped_not_extracted(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="dispatch",
            text_raw="Read /tmp/brief.md and do exactly what it says. "
                     "That file is your complete brief.",
        )
        self.ledger.record_prompt("p2", at=2_000, text_raw="make render LIVE", context="ctx")

        dropped, needs_review, kept = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((1, 0, 1), (dropped, needs_review, kept))

        rows = itemize_prompts.extract(self.ledger)
        self.assertEqual(["p2"], [row["id"] for row in rows])

    def test_dropped_row_carries_a_reason_and_never_reaches_open_views(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="cron", text_raw="Supervisor loop tick. Follow loop-tick.md.",
        )
        itemize_prompts.drop_noise(self.ledger)

        self.assertEqual([], self.ledger.read_prompt_view("unacknowledged"))
        self.assertEqual([], self.ledger.read_prompt_view("live_parameters"))
        item = self.ledger.get_item(itemize_prompts._item_id(
            "p1", 0, "noise:loop-tick cron text (scripts/supervisor/loop-tick.md)"))
        self.assertIsNotNone(item)
        self.assertEqual("dropped", item["status"])
        self.assertTrue(item["status_reason"])

    def test_drop_noise_is_idempotent(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="ctx", text_raw="## Context Usage\n\n**Model:** x",
        )
        first = itemize_prompts.drop_noise(self.ledger)
        second = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((1, 0, 0), first)
        self.assertEqual((0, 0, 0), second)

    def test_jon_text_that_merely_mentions_a_brief_is_kept(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="ctx",
            text_raw="did you read the brief I sent? what did it say about scope",
        )
        dropped, needs_review, kept = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((0, 0, 1), (dropped, needs_review, kept))


class SyntheticProvenanceTests(unittest.TestCase):
    """agent-supervisor#583/#652: eval-scenario fixture prompts, itemised as
    if Jon had typed them, are FLAGGED structurally on `context` -- never on
    how the text reads, per #583's own point that a well-written fixture is
    indistinguishable from a real directive by content alone. #652: that
    marker is a candidate, not proof (a real post-`/clear` turn carries the
    same marker), so a match lands in `needs_review`, never straight in
    `dropped` -- see `itemize_prompts.synthetic_provenance_reason`'s own
    comment for the measurement that forced this."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ledger = Ledger(self.tmp.name, clock=lambda: 1_000)

    def test_drop_noise_flags_a_known_eval_fixture_prompt_for_review(self):
        """Mutation direction 1: a known eval-fixture-shaped prompt (names a
        file, `reconcile.py`, that exists only under
        skills/*/references/eval-scenario*/) must be flagged needs_review --
        pulled out of `unacknowledged` -- when its transcript carries no
        prior-turn context. Not dropped outright: #652 found this same
        marker also matches a real directive (see
        test_drop_noise_keeps_the_real_post_clear_directive_agent_supervisor_652_found
        below), so nothing routes straight to `dropped` on this signal
        alone any more."""
        self.ledger.record_prompt(
            "p1", at=1_000, context=itemize_prompts.CONTEXT_UNDETERMINED,
            text_raw="Find and fix the bug in reconcile.py that let a mid-reconcile "
                     "crash leave some claims marked released while their result "
                     "files are missing.",
        )
        dropped, needs_review, kept = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((0, 1, 0), (dropped, needs_review, kept))
        item = self.ledger.get_item(itemize_prompts._item_id(
            "p1", 0, f"noise:{itemize_prompts.NEEDS_REVIEW_REASON}"))
        self.assertIsNotNone(item)
        self.assertEqual("needs_review", item["status"])
        self.assertIn("652", item["status_reason"])
        self.assertEqual([], self.ledger.read_prompt_view("unacknowledged"))
        self.assertEqual(1, len(self.ledger.read_prompt_view("needs_review")))

    def test_drop_noise_keeps_a_real_directive_with_the_same_shape_of_content(self):
        """Mutation direction 2: a real operator directive -- same
        directive-shaped phrasing naming a real file -- must survive when its
        transcript carries genuine prior-turn context. Content alone must
        not be what trips the filter."""
        self.ledger.record_prompt(
            "p2", at=2_000, context="deciding how the transcript-mining pass should run",
            text_raw="Find and fix the bug in send_input.sh that drops keystrokes "
                     "when the pane is scrolled.",
        )
        dropped, needs_review, kept = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((0, 0, 1), (dropped, needs_review, kept))
        rows = itemize_prompts.extract(self.ledger)
        self.assertEqual(["p2"], [row["id"] for row in rows])

    def test_drop_noise_keeps_the_real_post_clear_directive_agent_supervisor_652_found(self):
        """The exact counter-example agent-supervisor#652 traced by hand: a
        real operator turn that is the FIRST turn of its transcript file
        because the session opened with `/clear`, so it carries the identical
        CONTEXT_UNDETERMINED marker a synthetic fixture does. It must not be
        dropped -- it may be flagged for review (same as any other
        context-alone match), but never silently removed."""
        self.ledger.record_prompt(
            "p1", at=1_000, context=itemize_prompts.CONTEXT_UNDETERMINED,
            text_raw="Update the stale defect note in AGENTS.md referencing commit b00db9b.",
        )
        itemize_prompts.drop_noise(self.ledger)
        item = self.ledger.get_item(itemize_prompts._item_id(
            "p1", 0, f"noise:{itemize_prompts.NEEDS_REVIEW_REASON}"))
        self.assertIsNotNone(item)
        self.assertNotEqual("dropped", item["status"])
        self.assertEqual("needs_review", item["status"])

    def test_drop_noise_on_synthetic_fixture_is_idempotent(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context=itemize_prompts.CONTEXT_UNDETERMINED,
            text_raw="Review finalize.py's two-write crash sequence.",
        )
        first = itemize_prompts.drop_noise(self.ledger)
        second = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((0, 1, 0), first)
        self.assertEqual((0, 0, 0), second)

    def test_reclassify_synthetic_flags_an_already_itemised_open_item_for_review(self):
        """A prompt itemised BEFORE this filter existed (already has an open
        `directive`/`hard` item) gets that item corrected to needs_review,
        not deleted and not duplicated with a second item, and never
        straight to dropped (agent-supervisor#652)."""
        self.ledger.record_prompt(
            "p1", at=1_000, context=itemize_prompts.CONTEXT_UNDETERMINED,
            text_raw="Review finalize.py's two-write crash sequence and fix "
                     "anything that could leave the record wrong.",
        )
        self.ledger.add_item(
            "it-preexisting", prompt_id="p1", kind="directive",
            body="Review finalize.py's two-write crash sequence.", weight="hard",
        )
        reclassified, kept = itemize_prompts.reclassify_synthetic(self.ledger)
        self.assertEqual((1, 0), (reclassified, kept))
        item = self.ledger.get_item("it-preexisting")
        self.assertEqual("needs_review", item["status"])
        self.assertIn("652", item["status_reason"])
        # Judgement fields are untouched -- only status/status_reason changed.
        self.assertEqual("directive", item["kind"])
        self.assertEqual("hard", item["weight"])
        self.assertEqual([], self.ledger.read_prompt_view("unacknowledged"))
        self.assertEqual(1, len(self.ledger.read_prompt_view("needs_review")))

    def test_reclassify_synthetic_leaves_a_real_open_item_alone(self):
        self.ledger.record_prompt(
            "p2", at=2_000, context="a watchdog report from earlier in the session",
            text_raw="the worktree sweep should run by content, not ancestry",
        )
        self.ledger.add_item(
            "it-real", prompt_id="p2", kind="question",
            body="should the worktree sweep run by content or ancestry", weight="hard",
        )
        reclassified, kept = itemize_prompts.reclassify_synthetic(self.ledger)
        self.assertEqual((0, 1), (reclassified, kept))
        item = self.ledger.get_item("it-real")
        self.assertEqual("open", item["status"])

    def test_reclassify_synthetic_is_idempotent(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context=itemize_prompts.CONTEXT_UNDETERMINED,
            text_raw="Confirm the credential check in check-credential.sh.",
        )
        self.ledger.add_item("it-1", prompt_id="p1", kind="directive", body="b", weight="hard")
        first = itemize_prompts.reclassify_synthetic(self.ledger)
        second = itemize_prompts.reclassify_synthetic(self.ledger)
        self.assertEqual((1, 0), first)
        self.assertEqual((0, 0), second)


class DirectorPaneReasonTests(unittest.TestCase):
    """agent-supervisor#755 part B: pane identity (`prompts.tmux_pane_target`)
    is a CANDIDATE signal, same discipline as `synthetic_provenance_reason`
    (#652) -- a match must never itself decide `dropped`."""

    def setUp(self):
        self._saved_env = dict(os.environ)
        self.addCleanup(lambda: (os.environ.clear(), os.environ.update(self._saved_env)))
        os.environ.pop("AGENT_SUPERVISOR_MACHINE_PANES", None)

    def test_no_pane_target_is_not_a_match(self):
        self.assertIsNone(itemize_prompts.director_pane_reason(None))
        self.assertIsNone(itemize_prompts.director_pane_reason(""))

    def test_unconfigured_allowlist_matches_nothing(self):
        """Mutation direction 1: with no allowlist configured, EVERY pane
        target -- including the Director's own -- must be left alone rather
        than guessed at. This is the fail-safe direction: no configuration
        means no candidate signal, never an assumed one."""
        self.assertIsNone(itemize_prompts.director_pane_reason("estate:1"))

    def test_configured_target_via_env_is_a_candidate(self):
        os.environ["AGENT_SUPERVISOR_MACHINE_PANES"] = "estate:1,agent-supervisor:3"
        reason = itemize_prompts.director_pane_reason("estate:1")
        self.assertIsNotNone(reason)
        self.assertIn("755", reason)
        self.assertIn("estate:1", reason)

    def test_configured_target_via_explicit_known_targets_arg(self):
        reason = itemize_prompts.director_pane_reason("estate:1", known_targets={"estate:1"})
        self.assertIsNotNone(reason)

    def test_pane_target_not_in_allowlist_is_kept(self):
        """Mutation direction 2: a real Jon session, captured from an
        ordinary (non-estate) terminal pane, must never match even when an
        allowlist IS configured -- proving this keys on exact identity, not
        on 'any pane target present'."""
        os.environ["AGENT_SUPERVISOR_MACHINE_PANES"] = "estate:1"
        self.assertIsNone(itemize_prompts.director_pane_reason("mytmux:0"))


class DropNoiseDirectorPaneTests(unittest.TestCase):
    """agent-supervisor#755 part B: `drop_noise` routes a director-pane
    match to `needs_review`, never `dropped` -- and a genuine Jon prompt
    captured from an ordinary terminal is never touched by this check."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ledger = Ledger(self.tmp.name, clock=lambda: 1_000)
        self._saved_env = dict(os.environ)
        self.addCleanup(lambda: (os.environ.clear(), os.environ.update(self._saved_env)))
        os.environ["AGENT_SUPERVISOR_MACHINE_PANES"] = "estate:1"

    def test_prompt_from_known_machine_pane_is_flagged_needs_review_not_dropped(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="ctx", text_raw="#748 cannot get a reviewer -- diagnosed on the PR",
            tmux_pane="%22", tmux_pane_target="estate:1",
        )
        dropped, needs_review, kept = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((0, 1, 0), (dropped, needs_review, kept))
        self.assertEqual([], self.ledger.read_prompt_view("unacknowledged"))
        self.assertEqual(1, len(self.ledger.read_prompt_view("needs_review")))

    def test_mutation_a_genuine_jon_prompt_from_an_ordinary_terminal_is_never_misclassified(self):
        """The exact failure this whole corpus exists to prevent (#755's own
        non-negotiable): a real Jon directive, typed at an ordinary terminal
        outside tmux entirely (`tmux_pane_target` is NULL, the common case
        for an interactive Claude Code session run directly in a shell), must
        survive `drop_noise` untouched and reach `unacknowledged`/`extract`."""
        self.ledger.record_prompt(
            "p1", at=1_000, context="deciding what to build next",
            text_raw="scrap that approach, use the ledger's own session table instead",
            tmux_pane=None, tmux_pane_target=None,
        )
        dropped, needs_review, kept = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((0, 0, 1), (dropped, needs_review, kept))
        rows = itemize_prompts.extract(self.ledger)
        self.assertEqual(["p1"], [row["id"] for row in rows])

    def test_mutation_b_a_different_estate_managed_pane_not_in_the_allowlist_is_kept(self):
        """A pane resolved successfully, but to a target NOT on the
        configured allowlist (e.g. a plain interactive tmux session Jon
        opened himself, `mytmux:0`), must be kept -- proving this checks
        exact configured identity, not merely 'was there a resolvable
        pane'."""
        self.ledger.record_prompt(
            "p1", at=1_000, context="ctx", text_raw="yes, ship it",
            tmux_pane="%5", tmux_pane_target="mytmux:0",
        )
        dropped, needs_review, kept = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((0, 0, 1), (dropped, needs_review, kept))

    def test_needs_review_item_carries_the_755_reason(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="ctx", text_raw="Director decision required. brief at /tmp/x.md",
            tmux_pane_target="estate:1",
        )
        itemize_prompts.drop_noise(self.ledger)
        item = self.ledger.get_item(itemize_prompts._item_id(
            "p1", 0, f"noise:{itemize_prompts.director_pane_reason('estate:1', known_targets={'estate:1'})}"))
        self.assertIsNotNone(item)
        self.assertEqual("needs_review", item["status"])
        self.assertIn("755", item["status_reason"])

    def test_drop_noise_on_director_pane_prompt_is_idempotent(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="ctx", text_raw="Director decision required.",
            tmux_pane_target="estate:1",
        )
        first = itemize_prompts.drop_noise(self.ledger)
        second = itemize_prompts.drop_noise(self.ledger)
        self.assertEqual((0, 1, 0), first)
        self.assertEqual((0, 0, 0), second)


class ReclassifyDirectorPaneTests(unittest.TestCase):
    """agent-supervisor#755 part B: corrects already-itemised OPEN items
    whose prompt predates this check, same shape as `reclassify_synthetic`
    (#583/#652)."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.ledger = Ledger(self.tmp.name, clock=lambda: 1_000)
        self._saved_env = dict(os.environ)
        self.addCleanup(lambda: (os.environ.clear(), os.environ.update(self._saved_env)))
        os.environ["AGENT_SUPERVISOR_MACHINE_PANES"] = "estate:1"

    def test_reclassify_flags_an_already_itemised_open_item_for_review(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="ctx", text_raw="Decision required on the PR you authored.",
            tmux_pane_target="estate:1",
        )
        self.ledger.add_item(
            "it-preexisting", prompt_id="p1", kind="directive",
            body="Decision required on the PR you authored.", weight="hard",
        )
        reclassified, kept = itemize_prompts.reclassify_director_pane(self.ledger)
        self.assertEqual((1, 0), (reclassified, kept))
        item = self.ledger.get_item("it-preexisting")
        self.assertEqual("needs_review", item["status"])
        self.assertIn("755", item["status_reason"])
        # Judgement fields are untouched -- only status/status_reason changed.
        self.assertEqual("directive", item["kind"])
        self.assertEqual("hard", item["weight"])

    def test_reclassify_leaves_a_real_open_item_from_an_ordinary_terminal_alone(self):
        self.ledger.record_prompt(
            "p2", at=2_000, context="ctx", text_raw="use the ledger's own session table",
            tmux_pane_target=None,
        )
        self.ledger.add_item(
            "it-real", prompt_id="p2", kind="directive",
            body="use the ledger's own session table", weight="hard",
        )
        reclassified, kept = itemize_prompts.reclassify_director_pane(self.ledger)
        self.assertEqual((0, 1), (reclassified, kept))
        self.assertEqual("open", self.ledger.get_item("it-real")["status"])

    def test_reclassify_is_idempotent(self):
        self.ledger.record_prompt(
            "p1", at=1_000, context="ctx", text_raw="Director decision required.",
            tmux_pane_target="estate:1",
        )
        self.ledger.add_item("it-1", prompt_id="p1", kind="directive", body="b", weight="hard")
        first = itemize_prompts.reclassify_director_pane(self.ledger)
        second = itemize_prompts.reclassify_director_pane(self.ledger)
        self.assertEqual((1, 0), first)
        self.assertEqual((0, 0), second)


class NoiseMarkersTests(unittest.TestCase):
    """agent-estate#755: the two mechanical markers added for the director-tick
    brief-pointer and liveness-probe shapes, plus the exact near-miss #755
    measured (the existing "That file is your complete brief." marker missing
    the word-order variant) -- pinned so neither marker regresses silently."""

    def test_director_tick_brief_pointer_is_caught(self):
        text = (
            "Director decision required. Your complete brief is "
            "/private/tmp/director-750-tick.md — read it and decide, then "
            "record the decision on #750 and act on it."
        )
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_liveness_probe_is_caught(self):
        self.assertIsNotNone(
            itemize_prompts.noise_reason("Reply with exactly the single word: ready.")
        )

    def test_a_real_status_report_to_the_director_is_not_caught(self):
        # #755's own example of the general case this fix does NOT attempt --
        # a tick-loop status send with no structural marker. Must stay None:
        # a false positive here is the silently-discarded-directive failure
        # the whole corpus exists to prevent, not a false negative to fix here.
        text = (
            "#748 cannot get a reviewer: resolve_pr_contributors returns "
            "AUTHOR_LANES=[] and CONTRIBUTORS_RESOLVED empty -- branch "
            "fix/hvecore-path-747b, task none, no dispatched issue behind it."
        )
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_pre_755_word_order_did_not_match_the_existing_marker(self):
        # Regression pin for the near-miss itself: the ORIGINAL marker
        # ("That file is your complete brief.") alone must not match the
        # director-tick shape -- if it ever does, #755's own measurement
        # (mechanically verified, not assumed) was wrong.
        director_tick_text = "Your complete brief is /private/tmp/x.md — read it and decide"
        self.assertNotIn("That file is your complete brief.", director_tick_text)


class TailNoisePatternsTests(unittest.TestCase):
    """agent-estate#705 tail: the three shapes behind the five stragglers the
    684-prompt backlog left after it drained -- a raw <task-notification>
    block (fixed literal, NOISE_MARKERS), and two dispatcher templates that
    repeat verbatim except for one variable field (a PR number, a
    /private/tmp/ filename -- NOISE_PATTERNS regexes). Each gets a match
    case pinned against the exact live-ledger text (agent-estate#705's own
    issue comment) and a non-match case against plausible hand-written text
    that shares vocabulary but not structure -- the same two-directional
    discipline #755's NoiseMarkersTests above already established, and the
    same warning this issue's own brief restates: a topic filter previously
    matched real PR discussions ("career"/"resume" catching "Hill90 resume
    sweep"), so these match on fixed scaffolding, never on "PR", "read", or
    "task" appearing anywhere in the text."""

    def test_gh_pr_checks_dispatch_is_caught(self):
        # Verbatim shape of the two identical stragglers agent-estate#705's
        # own issue comment quotes.
        text = (
            "Check `gh pr checks 805` for the agent-estate PR (worktree at "
            "/var/folders/_b/n12wrrv55hlfyfcpsx6smkqm0000gn/T/ad-804-ci805-95947). "
            "unit-tests already passed (confirmed: Ran 1108 tests, OK skipped=3, "
            "both previously-failing tests now pass)."
        )
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_gh_pr_checks_dispatch_is_caught_regardless_of_pr_number(self):
        # The variable field: a different PR number must still match --
        # this is the whole reason it's a regex and not another literal
        # NOISE_MARKERS entry.
        text = "Check `gh pr checks 291` for the agent-estate PR (worktree at /tmp/x)."
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_a_real_pr_check_request_is_not_caught(self):
        # Plausible hand-typed text that shares vocabulary ("Check", "gh pr
        # checks", "PR", "805") but not the dispatcher's fixed scaffold --
        # must stay None.
        text = "Check the gh pr checks output for 805 yourself, I don't trust the bot on this one."
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_director_tmp_read_and_decide_is_caught(self):
        # Verbatim shape of the two director-*.md stragglers.
        text = (
            "Read /private/tmp/director-watchdog.md and decide. Short version: "
            "the supervisor-watchdog LaunchAgent you loaded in #659 has been "
            "booted out (state=not running, runs=10, clean exit)."
        )
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_director_tmp_read_and_decide_is_caught_regardless_of_filename(self):
        text = "Read /private/tmp/director-smoke.md and decide. #800 is down to one lever."
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_a_real_request_to_read_a_scratch_file_is_not_caught(self):
        # Plausible hand-typed text naming the same directory and verb but
        # not the dispatcher's fixed "and decide." scaffold -- must stay
        # None.
        text = "Can you read the notes I left at /private/tmp/scratch.md and tell me what you think, no rush."
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_task_notification_block_is_caught(self):
        text = (
            "<task-notification>\n<task-id>bl2oi771b</task-id>\n"
            "<tool-use-id>toolu_0145WTb2gM7HPxybvkbYfjrW</tool-use-id>\n"
            "<status>killed</status>\n"
            "<summary>Background command \"Wait for full test suite to complete\" "
            "was stopped</summary>\n</task-notification>"
        )
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_a_real_message_about_task_notifications_is_not_caught(self):
        # Mentions the topic ("task notification") without the literal tag
        # -- must stay None; this is the exact class of false positive the
        # brief warns about (topic, not structure).
        text = "I want to discuss task notification UX -- should we surface a toast for background task completion?"
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_anchor_regression_quoted_scaffold_in_a_longer_prompt_gh_pr_checks(self):
        # agent-estate#810 REQUEST_CHANGES (second round): the first fix
        # pass's pinned string had a doubled backtick right after the PR
        # number ("`Check `gh pr checks 805`` for") -- one backtick too
        # many for the regex's own single `` ` `` before " for", so it
        # never matched *either* anchored or unanchored and proved nothing.
        # This string reproduces the dispatcher's scaffold with correct
        # single backticks, quoted mid-sentence (not at the string's own
        # start) -- so the unanchored regex genuinely matches it via
        # `.search()`, and the anchor is what turns that match back to None.
        text = (
            "Yesterday the dispatcher sent: Check `gh pr checks 805` for "
            "the agent-estate PR (worktree at /tmp/x) -- can we vary that "
            "wording so it does not read as robotic?"
        )
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_anchor_regression_quoted_scaffold_in_a_longer_prompt_tmp_read(self):
        # Same defect, the other pattern. Pinned verbatim from the
        # reviewer's own PR comment. Must stay None.
        text = (
            'I noticed the dispatcher literally said "Read '
            '/private/tmp/foo.md and decide." which seems lazy, can we '
            "improve the wording?"
        )
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_reviewer_third_case_task_notification_still_matches_pre_existing(self):
        # The reviewer's third case: 'Yesterday I saw <task-notification>
        # tags spam the terminal, can we suppress them?' This matches
        # through NOISE_MARKERS' plain-substring check on the literal
        # "<task-notification>" tag -- a PRE-EXISTING mechanism this PR did
        # not introduce and is not anchored (NOISE_MARKERS never was; only
        # the two NOISE_PATTERNS regexes above needed the `^\s*` anchor).
        # Decision (agent-estate#810 fix-pass): leave NOISE_MARKERS
        # unanchored here. Anchoring every literal marker to "start of
        # prompt" would change matching behaviour for every existing entry
        # in NOISE_MARKERS, a wider blast radius than this PR's subject (two
        # new regexes), and belongs in its own change if pursued. This test
        # documents the current, unfixed behaviour so a future change to it
        # is a deliberate decision, not a silent regression either way.
        text = "Yesterday I saw <task-notification> tags spam the terminal, can we suppress them?"
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_drop_noise_on_each_new_shape_is_idempotent(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        ledger = Ledger(tmp.name, clock=lambda: 1_000)
        text = "Check `gh pr checks 805` for the agent-estate PR (worktree at /tmp/x)."
        ledger.record_prompt("p1", at=1_000, context="ctx", text_raw=text)

        first = itemize_prompts.drop_noise(ledger)
        second = itemize_prompts.drop_noise(ledger)
        # The prompt already has an items row from the first pass, so
        # list_unitemised_prompts no longer returns it at all -- the second
        # pass sees nothing left to classify, same idempotency shape
        # DropNoiseTests.test_drop_noise_is_idempotent already pins for the
        # existing NOISE_MARKERS entries.
        self.assertEqual((1, 0, 0), first)
        self.assertEqual((0, 0, 0), second)

        item = ledger.get_item(itemize_prompts._item_id(
            "p1", 0, f"noise:{itemize_prompts.noise_reason(text)}"))
        self.assertIsNotNone(item)
        self.assertEqual(item["status"], "dropped")
        self.assertEqual(item["kind"], "thought")
        self.assertEqual(item["weight"], "retracted")


class SecondTailNoisePatternsTests(unittest.TestCase):
    """agent-estate#705, second pass: five more structural shapes found by
    directly measuring the 4,542-prompt unjudged backlog (issue comment
    2026-09-10) -- three fixed harness/system tags (NOISE_MARKERS, same
    unanchored-literal shape as <task-notification> above) and two
    scaffolds needing a regex because their fixed part is not a single
    unvarying string (NOISE_PATTERNS). Each gets a match case against
    representative text (the corpus's own real paths/ids are not
    reproduced here; only the fixed structural shape matters) and a
    non-match case proving the anchor/literal does not over-match plausible
    hand-written text discussing the same topic -- the same two-directional
    discipline every prior class in this file already establishes, and the
    same warning this issue's own brief restates: match on structure,
    never on topic.

    Measured against the live corpus before writing any of this (Python
    `file:...?mode=ro`, read-only, no CLI): each pattern's match count
    among the 4,542 unjudged prompts equals the issue's own number
    (334/158/152/138/98), and zero of the 4,093 already-judged prompts
    carrying a real (weight=hard or weight=preference) item match any of
    the five -- the false-positive check this issue's brief says is the
    one that matters."""

    def test_project_instructions_injection_is_caught(self):
        # Representative shape: a markdown header naming the instructions
        # file and (usually) a project path, a blank line, then the
        # <INSTRUCTIONS> tag -- the harness's own turn-opener when a fresh
        # session first reads a project's AGENTS.md/CLAUDE.md.
        text = (
            "# AGENTS.md instructions for /tmp/example-project\n\n"
            "<INSTRUCTIONS>\n## Skills\nA skill is a set of local "
            "instructions to follow...\n"
        )
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_project_instructions_injection_is_caught_without_a_path(self):
        # A real variant found in the corpus: some skill routers omit the
        # "for <path>" suffix entirely ("# AGENTS.md instructions\n\n
        # <INSTRUCTIONS>..."). The pattern must not depend on the path
        # being present -- only on the header-line/blank-line/tag shape.
        text = "# AGENTS.md instructions\n\n<INSTRUCTIONS>\n<!-- Generated by APM CLI -->\n"
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_a_real_question_about_agents_md_is_not_caught(self):
        # Mentions the file and the word "instructions" without the
        # harness's own header-line/blank-line/<INSTRUCTIONS>-tag shape --
        # must stay None.
        text = "Can you check whether the AGENTS.md instructions for the repo root are still accurate?"
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_turn_aborted_block_is_caught(self):
        text = (
            "<turn_aborted>\nThe user interrupted the previous turn on "
            "purpose. If any tools/commands were aborted, they may have "
            "partially executed; verify current state before continuing.\n"
        )
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_a_real_message_about_aborted_turns_is_not_caught(self):
        text = "Why did my last turn get aborted, did something time out?"
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_environment_context_block_is_caught(self):
        text = (
            "<environment_context>\n  <cwd>/Users/jon</cwd>\n"
            "  <approval_policy>on-request</approval_policy>\n"
            "  <sandbox_mode>read-only</sandbox_mode>\n"
            "</environment_context>\n"
        )
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_a_real_question_about_the_environment_is_not_caught(self):
        text = "What's the current environment context -- are we in read-only sandbox mode right now?"
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_recommended_plugins_block_is_caught(self):
        text = (
            "<recommended_plugins>\nHere is a list of plugins that are "
            "available but not installed. If the user's query would "
            "benefit from one of these plugins, use it.\n</recommended_plugins>\n"
        )
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_a_real_question_about_plugins_is_not_caught(self):
        text = "Which recommended plugins do we actually have installed right now?"
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_tool_output_prefix_is_caught(self):
        # The shape found in the corpus: an entire prompt that is actually
        # captured assistant/agent output (Claude Code's own terminal
        # bullet), landing as a role=user row by capture-pipeline artifact,
        # not because Jon typed it.
        text = "⏺ Everything is working. Here's the final summary:\n\n  Migration complete."
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_tool_output_prefix_mid_message_is_not_caught(self):
        # agent-estate#705's own brief warning, reproduced exactly: a real,
        # human-authored prompt that pastes or quotes terminal output
        # containing the same glyph MID-message must not be dropped --
        # measured directly against the corpus, this is the true shape of
        # 19 of the 157 total occurrences of the glyph in the unjudged
        # backlog (only 138 are at position 0). The anchor is what
        # separates "this whole prompt IS captured agent output" from "a
        # human quoted some agent output as part of a real question".
        text = (
            "how come claude did this no issues\n\n"
            "❯ transcribe this video https://example.com/watch?v=xyz\n\n"
            "⏺ Skill(transcribe)\n  ⎿ done"
        )
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_drop_noise_on_each_second_tail_shape_is_idempotent(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        ledger = Ledger(tmp.name, clock=lambda: 1_000)
        text = "<environment_context>\n  <cwd>/tmp</cwd>\n</environment_context>\n"
        ledger.record_prompt("p1", at=1_000, context="ctx", text_raw=text)

        first = itemize_prompts.drop_noise(ledger)
        second = itemize_prompts.drop_noise(ledger)
        self.assertEqual((1, 0, 0), first)
        self.assertEqual((0, 0, 0), second)

        item = ledger.get_item(itemize_prompts._item_id(
            "p1", 0, f"noise:{itemize_prompts.noise_reason(text)}"))
        self.assertIsNotNone(item)
        self.assertEqual(item["status"], "dropped")
        self.assertEqual(item["kind"], "thought")
        self.assertEqual(item["weight"], "retracted")


class AgentOutputCaptureArtifactTests(unittest.TestCase):
    """agent-estate#1351: #1349's own reviewer read all 19 unanchored `⏺`
    occurrences the 138-match `^⏺` anchor left behind and found up to 8 are
    themselves capture artifacts, just starting with a different marker.
    Three more shapes, each re-measured directly against the live
    4,542-prompt unjudged backlog before being added (2026-09-10): `Bash(`
    (2 unjudged matches), `Conversation compacted` (2, covering both literal
    forms the corpus carries), and the unanchored `Background command "..."
    completed (exit code` harness notice (11). Zero of the 2,197 prompts
    already carrying a real (weight=hard or weight=preference) item match
    any of the three -- the false-positive check this issue's brief says is
    the one that matters. Same two-directional discipline as every class
    above: a match case and a plain-language non-match case per pattern."""

    def test_bash_tool_call_transcript_is_caught(self):
        text = 'Bash(gh pr create --title "fix: replace curl with container-native readiness check")\n  ⎿  done'
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_a_real_question_about_a_bash_command_is_not_caught(self):
        text = "Can you run Bash(ls -la) for me and tell me what's in that directory?"
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_anchor_regression_bash_paste_mid_message_agent_estate_1351(self):
        # The exact corpus row (id 3500e830...) #1351's own investigation
        # found: genuine human framing, THEN a pasted `Bash(...)` transcript
        # line mid-message. Unanchored, this would match; the anchor is what
        # keeps it None, same shape as the `^⏺` anchor regression above.
        text = (
            "help me fix this for good claude said this but i want it fixed "
            "not a tmp dir workaround\n\n⏺ Bash(gh pr create --title x)\n  ⎿  done"
        )
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_conversation_compacted_banner_is_caught(self):
        text = "Conversation compacted · ctrl+o for history\n═══════════════════════"
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_conversation_compacted_banner_alternate_form_is_caught(self):
        # The corpus carries a second literal form with a leading "✻" and a
        # parenthetical rather than a middle-dot -- both are fixed harness
        # chrome, never a real prompt's own wording.
        text = "✻ Conversation compacted (ctrl+o for history)\n\n  ⎿  Read src/foo.ts"
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_a_real_message_about_compaction_is_not_caught(self):
        text = "i am very mad at claude code. why did it compact our conversation without asking"
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_anchor_regression_compacted_banner_mid_message_is_not_caught(self):
        # A human row the corpus carries: genuine framing, THEN the banner
        # text quoted mid-message -- must stay None, proving the anchor
        # (not just the literal) is what does the work.
        text = (
            "i am very made at claude code. Tell it why its fucking stupid\n\n\n"
            "✻ Conversation compacted (ctrl+o for history)\n\n  ⎿  some transcript"
        )
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_background_command_completion_notice_is_caught(self):
        text = (
            "VPS Runtime Verification Evidence — V1 through V8\n  ┌─────┬──────┐\n"
            "...\n⏺ Background command \"Wait for full test suite\" completed (exit code 0)"
        )
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_background_command_completion_notice_is_caught_regardless_of_command_name(self):
        text = 'Background command "npm run dev" completed (exit code 0)'
        self.assertIsNotNone(itemize_prompts.noise_reason(text))

    def test_a_real_message_about_a_background_command_is_not_caught(self):
        # Plausible hand-typed text discussing the same topic in plain
        # language, without the harness's own fixed quote-and-exit-code
        # template -- must stay None.
        text = "My background command finished with exit code 1, any idea why it failed?"
        self.assertIsNone(itemize_prompts.noise_reason(text))

    def test_drop_noise_on_each_agent_output_capture_shape_is_idempotent(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        ledger = Ledger(tmp.name, clock=lambda: 1_000)
        text = 'Bash(cd /repo && npm run dev 2>&1)\n  ⎿  done'
        ledger.record_prompt("p1", at=1_000, context="ctx", text_raw=text)

        first = itemize_prompts.drop_noise(ledger)
        second = itemize_prompts.drop_noise(ledger)
        self.assertEqual((1, 0, 0), first)
        self.assertEqual((0, 0, 0), second)

        item = ledger.get_item(itemize_prompts._item_id(
            "p1", 0, f"noise:{itemize_prompts.noise_reason(text)}"))
        self.assertIsNotNone(item)
        self.assertEqual(item["status"], "dropped")
        self.assertEqual(item["kind"], "thought")
        self.assertEqual(item["weight"], "retracted")


if __name__ == "__main__":
    unittest.main()
