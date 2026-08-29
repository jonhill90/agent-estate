import os
import sys
import tempfile
import unittest
from pathlib import Path

SUPERVISOR_DIR = Path(__file__).resolve().parents[2] / "scripts" / "supervisor"
sys.path.insert(0, str(SUPERVISOR_DIR))

import itemize_prompts  # noqa: E402
from core import Ledger  # noqa: E402


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
        # agent-estate#810 REQUEST_CHANGES: `noise_reason` used unanchored
        # `.search()`, so it matched the dispatcher's fixed scaffold
        # anywhere in the text -- including inside a genuinely human prompt
        # that merely QUOTES the scaffold while asking to change it. Pinned
        # verbatim from the reviewer's own PR comment. Must stay None.
        text = (
            "The brief text always says `Check `gh pr checks 805`` for the "
            "agent-estate PR -- can we vary that wording so it does not "
            "read as robotic?"
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


if __name__ == "__main__":
    unittest.main()
