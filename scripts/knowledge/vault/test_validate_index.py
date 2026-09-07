import contextlib, importlib.util, io, pathlib, tempfile, unittest

P = pathlib.Path(__file__).with_name('validate_index.py')
spec = importlib.util.spec_from_file_location('validator', P)
v = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v)

FACT_FRONTMATTER = '---\ntype: user\ncreated: 2026-07-12\nsource: fixture\ntitle: Test\ndescription: fixture fact\n---\n# Test\n'


class ValidateStartHere(unittest.TestCase):
    """A2-COMPLETION (run/iteration-queue.md): the validator's subject
    moved from agent/index.md to Start Here.md's own `## Facts` section --
    these tests exercise that, not the retired agent/ shape."""

    def run_against(self, root, facts_body, note_subdirs_rows=""):
        (root / "01 - Notes").mkdir(parents=True, exist_ok=True)
        (root / "99 - Meta").mkdir(parents=True, exist_ok=True)
        if note_subdirs_rows:
            (root / "99 - Meta" / "note-subdirs.md").write_text(
                "| Prefix | Subdirectory | Purpose | Earned |\n|---|---|---|---|\n" + note_subdirs_rows
            )
        (root / "Start Here.md").write_text(
            "---\nokf_version: \"0.1\"\ntitle: Start Here\n---\n\n"
            "# Start Here\n\nintro text\n\n## Facts\n\n" + facts_body + "\n## The areas\n\nnot a fact bullet\n"
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
        # not just from Start Here.md's own root-relative form.
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            self.write_fact(root, "202607120001.md")
            code, _ = self.run_against(root, "- [Test](../01 - Notes/202607120001.md) — fixture\n")
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
        # Start Here.md's own Facts section, is not orphaned (P10).
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


if __name__ == "__main__":
    unittest.main()
