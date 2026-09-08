import contextlib, importlib.util, io, os, pathlib, sys, tempfile, unittest
from unittest import mock

P = pathlib.Path(__file__).with_name('validate_index.py')
spec = importlib.util.spec_from_file_location('validator', P)
v = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v)

# Captured once, before any test below overwrites v.vault_dir with a fixture
# stub (every test using run_against() does) -- the two argv tests need the
# real function, the one that actually reads sys.argv, not whatever the
# previously-run test happened to leave behind.
REAL_VAULT_DIR = v.vault_dir

FACT_FRONTMATTER = '---\ntype: user\ncreated: 2026-07-12\nsource: fixture\ntitle: Test\ndescription: fixture fact\n---\n# Test\n'


class ValidateIndex(unittest.TestCase):
    """A2-COMPLETION (run/iteration-queue.md), fix pass (Director, OKF
    §12/§8 adjudication): the validator's subject is the vault-root
    index.md -- a first pass briefly moved it onto Start Here.md's own
    `## Facts` section, corrected because OKF names the bundle-root
    index.md FILENAME specifically as the sole legal okf_version carrier.
    Start Here.md is a separate human-entry-point page with no bullet
    list of its own, outside this validator's scope entirely."""

    def run_against(self, root, facts_body, note_subdirs_rows=""):
        (root / "01 - Notes").mkdir(parents=True, exist_ok=True)
        (root / "99 - Meta").mkdir(parents=True, exist_ok=True)
        if note_subdirs_rows:
            (root / "99 - Meta" / "note-subdirs.md").write_text(
                "| Prefix | Subdirectory | Purpose | Earned |\n|---|---|---|---|\n" + note_subdirs_rows
            )
        (root / "index.md").write_text(
            "---\nokf_version: \"0.1\"\n---\n\n"
            "# Facts\n\nintro text\n\n" + facts_body
        )
        v.vault_dir = lambda: str(root)
        with contextlib.redirect_stdout(io.StringIO()) as out:
            code = v.main()
        return code, out.getvalue()

    def write_fact(self, root, rel, body=FACT_FRONTMATTER):
        p = root / "01 - Notes" / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(body)

    def test_root_relative_link_valid(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md")
            code, _ = self.run_against(root, "- [Test](01 - Notes/202607120001.md) — fixture\n")
            self.assertEqual(code, 0)

    def test_encoded_root_relative_link_valid(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md")
            code, _ = self.run_against(root, "- [Test](01%20-%20Notes/202607120001.md) — fixture\n")
            self.assertEqual(code, 0)

    def test_one_level_down_link_also_valid(self):
        # ../01 - Notes/... still resolves -- the same regex validates
        # links written from a 02 - MOCs/*.md hub or another fact file,
        # not just from index.md's own root-relative form. Exercised from
        # an actual hub file (one level below the vault root), not from
        # index.md itself: index.md sits AT the root, so a "../" link
        # written there is not just accepted syntax, it is a literal
        # on-disk path one level above the vault -- check 3a (2026-09-07)
        # correctly flags that as broken, since a real reader's link
        # would not resolve either. Caught by check 3a's own mutation
        # test: this case originally exercised that combination and
        # check 3a rightly failed it.
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md")
            mocs = root / "02 - MOCs"
            mocs.mkdir()
            (mocs / "Facts.md").write_text(
                "# Facts\n\n- [Test](../01 - Notes/202607120001.md)\n"
            )
            code, _ = self.run_against(root, "")
            self.assertEqual(code, 0)

    def test_registered_subdir_link_valid(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "01f - Facts/202607120001.md")
            code, _ = self.run_against(
                root,
                "- [Test](01 - Notes/01f - Facts/202607120001.md) — fixture\n",
                note_subdirs_rows="| `01f` | `01 - Notes/01f - Facts/` | facts | test |\n",
            )
            self.assertEqual(code, 0)

    def test_unregistered_subdir_link_fails(self):
        # The exact stall P9 was built to avoid: an unregistered subdir
        # is correctly rejected, not silently accepted.
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "01z - Unregistered/202607120001.md")
            code, out = self.run_against(
                root, "- [Test](01 - Notes/01z - Unregistered/202607120001.md) — fixture\n"
            )
            self.assertEqual(code, 1)
            self.assertIn("no source link", out)

    def test_alias_wikilink_valid(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(
                root, "202607120001.md",
                '---\ntype: user\ncreated: 2026-07-12\nsource: fixture\naliases: [test-alias]\n---\n# Test\n',
            )
            code, _ = self.run_against(root, "- [[test-alias]] — fixture\n")
            self.assertEqual(code, 0)

    def test_broken_link_is_a_violation(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            code, out = self.run_against(root, "- [Test](01 - Notes/202607120001.md) — fixture\n")
            self.assertEqual(code, 1)
            self.assertIn("does not exist", out)

    def test_bullet_with_no_link_is_a_violation(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            code, out = self.run_against(root, "- prose with no link at all\n")
            self.assertEqual(code, 1)
            self.assertIn("no source link", out)

    def test_orphaned_fact_is_a_violation(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md")
            self.write_fact(root, "202607120002.md")  # never referenced
            code, out = self.run_against(root, "- [Test](01 - Notes/202607120001.md) — fixture\n")
            self.assertEqual(code, 1)
            self.assertIn("orphaned", out)

    def test_hub_reference_prevents_orphan(self):
        # A note reachable only through a 02 - MOCs/*.md hub, not through
        # index.md directly, is not orphaned (P10).
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "01p - Parameters/202607120001.md")
            mocs = root / "02 - MOCs"
            mocs.mkdir()
            (mocs / "Parameters.md").write_text(
                "# Parameters\n\n- [Test](../01 - Notes/01p - Parameters/202607120001.md)\n"
            )
            code, _ = self.run_against(
                root, "",
                note_subdirs_rows="| `01p` | `01 - Notes/01p - Parameters/` | params | test |\n",
            )
            self.assertEqual(code, 0)

    def test_missing_required_frontmatter_is_a_violation(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md", "---\ntitle: Test\n---\n# Test\n")
            code, out = self.run_against(root, "- [Test](01 - Notes/202607120001.md) — fixture\n")
            self.assertEqual(code, 1)
            self.assertIn("missing required frontmatter", out)

    def test_missing_recommended_frontmatter_is_a_warning_not_a_violation(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md", "---\ntype: user\ncreated: 2026-07-12\nsource: fixture\n---\n# Test\n")
            code, out = self.run_against(root, "- [Test](01 - Notes/202607120001.md) — fixture\n")
            self.assertEqual(code, 0)
            self.assertIn("missing recommended frontmatter", out)

    def test_14_digit_note_id_valid(self):
        # Newer notes carry a 14-digit id (full timestamp), not just the
        # original 12-digit form -- both must resolve.
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "20260712000130.md")
            code, _ = self.run_against(root, "- [Test](01 - Notes/20260712000130.md) — fixture\n")
            self.assertEqual(code, 0)

    def test_hub_link_that_does_not_resolve_is_a_violation(self):
        # Check 3a (2026-09-07): the vault reported "Contract holds" while
        # carrying 63 dead links -- hubs pointing at deleted iCloud conflict
        # copies among them. Checks 1-3 only ask whether every NOTE is
        # reachable; this is the first check that asks whether every LINK
        # arrives somewhere.
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md")
            mocs = root / "02 - MOCs"
            mocs.mkdir()
            (mocs / "Facts.md").write_text(
                "# Facts\n\n"
                "- [Test](../01 - Notes/202607120001.md)\n"
                "- [Gone](../01 - Notes/202607120099.md)\n"
            )
            code, out = self.run_against(root, "- [Test](01 - Notes/202607120001.md) — fixture\n")
            self.assertEqual(code, 1)
            self.assertIn("link does not resolve", out)
            self.assertIn("202607120099.md", out)

    def test_unreadable_file_is_a_violation_not_silence(self):
        # Reviewer's finding on PR #1296 (fix pass, 2026-09-07): check 3a was
        # the only read_text() in the file wrapped in try/except, and an
        # unreadable file's broken link vanished with no trace -- chmod 644
        # reports the violation, chmod 000 reports nothing, same exit code
        # otherwise. A read that failed and an empty result must not look
        # the same (it-d43a08d739bf32a8). Isolated fixture, not the live
        # vault, per the brief.
        if os.name != "posix" or hasattr(os, "geteuid") and os.geteuid() == 0:
            self.skipTest("permission bits are not enforced for root or on this platform")
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md")
            scratch = root / "03 - Scratch"
            scratch.mkdir()
            unreadable = scratch / "unreadable.md"
            unreadable.write_text("- [Gone](../01 - Notes/202607120099.md)\n")
            unreadable.chmod(0o000)
            try:
                code, out = self.run_against(root, "- [Test](01 - Notes/202607120001.md) — fixture\n")
            finally:
                unreadable.chmod(0o644)
            self.assertEqual(code, 1)
            self.assertIn("03 - Scratch/unreadable.md", out)

    def test_named_scratch_dirs_still_skipped(self):
        # The narrowed check (fix pass, PR #1296) must still exempt exactly
        # the directories the old blanket startswith(".") skip was meant
        # for -- Obsidian's own plus the numbered backup snapshots.
        for name in (".obsidian", ".trash", ".p15-fix-backup", ".source-hub-backup-123"):
            with tempfile.TemporaryDirectory() as d:
                root = pathlib.Path(d)
                self.write_fact(root, "202607120001.md")
                scratch = root / name
                scratch.mkdir()
                (scratch / "stale.md").write_text(
                    "- [Gone](../01 - Notes/202607120099.md)\n"
                )
                code, _ = self.run_against(root, "- [Test](01 - Notes/202607120001.md) — fixture\n")
                self.assertEqual(code, 0, f"{name} should still be exempt")

    def test_unnamed_dot_dir_is_not_silently_exempt(self):
        # The whole point of narrowing to a named list: a real dot-prefixed
        # directory that isn't one of the known scratch/backup names is no
        # longer given a free pass the way a blanket startswith(".") would.
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md")
            odd = root / ".in-progress"
            odd.mkdir()
            (odd / "notes.md").write_text(
                "- [Gone](../01 - Notes/202607120099.md)\n"
            )
            code, out = self.run_against(root, "- [Test](01 - Notes/202607120001.md) — fixture\n")
            self.assertEqual(code, 1)
            self.assertIn(".in-progress/notes.md", out)

    def test_run_by_argument_from_outside_the_vault(self):
        # Director finding, second fix pass on PR #1296: the repo copy must
        # be runnable against an arbitrary vault by path, not only from
        # inside one -- that's the entire point of tracking it in git
        # separately from the vault's own deployed copy. Uses the REAL
        # vault_dir() (REAL_VAULT_DIR), not the v.vault_dir stub every other
        # test here installs, so this actually exercises the argv-reading
        # code and not a bypass of it.
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md")
            (root / "99 - Meta").mkdir(parents=True, exist_ok=True)
            (root / "index.md").write_text(
                "---\nokf_version: \"0.1\"\n---\n\n"
                "# Facts\n\n- [Test](01 - Notes/202607120001.md) — fixture\n"
            )
            v.vault_dir = REAL_VAULT_DIR
            try:
                with mock.patch.object(sys, "argv", ["validate_index.py", str(root)]):
                    with contextlib.redirect_stdout(io.StringIO()) as out:
                        code = v.main()
            finally:
                v.vault_dir = REAL_VAULT_DIR
            self.assertEqual(code, 0, out.getvalue())
            self.assertIn(str(root), out.getvalue())

    def test_bad_argument_path_fails_loudly_not_silently(self):
        # A path that is not a vault root must error, never silently fall
        # back to validating whatever tree this file's own location derives
        # (which could be the wrong tree entirely, reporting a false-clean
        # contract on it -- the same failed-read-looks-like-a-clean-result
        # shape the rest of this PR closes).
        with tempfile.TemporaryDirectory() as d:
            not_a_vault = pathlib.Path(d) / "not-a-vault"
            not_a_vault.mkdir()
            v.vault_dir = REAL_VAULT_DIR
            try:
                with mock.patch.object(sys, "argv", ["validate_index.py", str(not_a_vault)]):
                    with self.assertRaises(SystemExit) as cm:
                        v.main()
            finally:
                v.vault_dir = REAL_VAULT_DIR
            self.assertNotEqual(cm.exception.code, 0)

    def test_placeholder_link_with_angle_bracket_is_not_a_violation(self):
        # A target containing "<" is a documented placeholder in a contract
        # or spec ("01 - Notes/<subdir>/<id>.md"), not a link anyone can
        # follow -- check 3a must not flag it.
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md")
            (root / "99 - Meta").mkdir(exist_ok=True)
            (root / "99 - Meta" / "index-contract.md").write_text(
                "# Contract\n\nLinks look like [example](01 - Notes/<subdir>/<id>.md).\n"
            )
            code, _ = self.run_against(root, "- [Test](01 - Notes/202607120001.md) — fixture\n")
            self.assertEqual(code, 0)


if __name__ == "__main__":
    unittest.main()
