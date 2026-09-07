"""Tests for backup_and_restore_test.py. Every fixture is synthetic --
this suite never touches the real ~/corpus or ~/.local/state/estate."""
import importlib.util
import json
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

P = Path(__file__).with_name("backup_and_restore_test.py")
spec = importlib.util.spec_from_file_location("evidence_backup", P)
m = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = m  # dataclass field-type resolution needs the
                            # module registered before exec (py3.14)
spec.loader.exec_module(m)


def make_fixture_sqlite(path: Path) -> None:
    conn = sqlite3.connect(str(path))
    conn.execute("CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT)")
    conn.execute("INSERT INTO widgets (name) VALUES ('alpha'), ('beta')")
    conn.commit()
    conn.close()


def make_wal_fixture_sqlite(path: Path) -> sqlite3.Connection:
    """A fixture that mirrors the real corpus: WAL journal mode with the
    connection kept OPEN (not closed) so the -wal/-shm sidecars stay
    present on disk, exactly like the real live corpus.sqlite3 (an
    actively-open connection, not a cleanly-closed one) -- sqlite3's own
    close() auto-checkpoints and removes the sidecars, which would make
    this fixture stop exercising the real -readonly-on-WAL edge case the
    backup path's journal_mode=DELETE flatten step exists to handle.
    Caller must close() the returned connection when done."""
    conn = sqlite3.connect(str(path))
    conn.execute("PRAGMA journal_mode=WAL;")
    conn.execute("CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT)")
    conn.execute("INSERT INTO widgets (name) VALUES ('alpha'), ('beta')")
    conn.commit()
    return conn


class TypedAbsence(unittest.TestCase):
    def test_absent_sqlite_source_is_typed_not_a_silent_skip(self):
        with tempfile.TemporaryDirectory() as d:
            r = m.backup_sqlite(Path(d) / "does-not-exist.sqlite3", Path(d) / "dest.sqlite3")
            self.assertEqual(r.status, "absent")
            self.assertIn("absent", r.detail)

    def test_absent_jsonl_source_is_typed_not_a_silent_skip(self):
        with tempfile.TemporaryDirectory() as d:
            r = m.backup_jsonl(Path(d) / "does-not-exist.jsonl", Path(d) / "dest.jsonl")
            self.assertEqual(r.status, "absent")

    def test_unreadable_source_is_typed_distinctly_from_absent(self):
        with tempfile.TemporaryDirectory() as d:
            src = Path(d) / "unreadable.sqlite3"
            src.write_bytes(b"whatever")
            src.chmod(0o000)
            try:
                r = m.backup_sqlite(src, Path(d) / "dest.sqlite3")
                self.assertEqual(r.status, "unreadable")
                self.assertNotEqual(r.status, "absent")
            finally:
                src.chmod(0o644)


class SqliteBackupAndRestore(unittest.TestCase):
    def test_backup_is_integrity_checked_and_restore_is_byte_identical_and_queryable(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            src = root / "src.sqlite3"
            conn = make_wal_fixture_sqlite(src)
            try:
                before = m.stat_snapshot(src)

                dest = root / "backup" / "corpus-sqlite3.sqlite3"
                r = m.backup_sqlite(src, dest)

                self.assertEqual(r.status, "ok")
                self.assertTrue(r.integrity_check["ok"])
                self.assertEqual(r.integrity_check["result"], "ok")
                self.assertIsNotNone(r.sha256)

                # live source untouched
                after = m.stat_snapshot(src)
                self.assertEqual(before, after)
                self.assertTrue(r.live_untouched)

                # flattened to a plain journal mode -- openable -readonly
                # without -wal/-shm sidecars (the exact bug this script's
                # own flatten step exists to avoid)
                self.assertFalse(dest.with_name(dest.name + "-wal").exists())
                self.assertFalse(dest.with_name(dest.name + "-shm").exists())

                restore_dir = root / "restore"
                restore_dir.mkdir()
                rt = m.restore_test_sqlite(dest, restore_dir)

                self.assertTrue(rt["byte_identical"])
                self.assertEqual(rt["backup_sha256"], rt["restored_sha256"])
                self.assertTrue(rt["integrity_check"]["ok"])
                self.assertEqual(rt["query_proof"]["table_count"], "1")
                self.assertIn("widgets", rt["query_proof"]["sample_tables"])
            finally:
                conn.close()

    def test_corrupted_backup_copy_fails_integrity_check_not_silently_accepted(self):
        # An honestly-corrupt source fails at the .backup step itself
        # (sqlite3 refuses to copy a malformed database), which is
        # already a correctly-typed "error" -- but it does not exercise
        # backup_sqlite's OWN integrity_check gate specifically. To
        # isolate that gate, intercept run_sqlite so the .backup and
        # flatten steps succeed against a genuinely valid fixture, and
        # only the integrity_check call is forced to report failure --
        # proving backup_sqlite itself refuses to report "ok" status on
        # a failing integrity_check, not merely that sqlite3 can detect
        # corruption in general.
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            src = root / "src.sqlite3"
            make_fixture_sqlite(src)
            dest = root / "backup" / "corpus-sqlite3.sqlite3"

            real_run_sqlite = m.run_sqlite

            def faking_integrity_check(args, check=True):
                if "PRAGMA integrity_check;" in args:
                    fake = subprocess.CompletedProcess(args, returncode=0, stdout="*** in database main ***\nPage 3: btreeInitPage() returns error code 11\n", stderr="")
                    return fake
                return real_run_sqlite(args, check=check)

            with patch.object(m, "run_sqlite", side_effect=faking_integrity_check):
                r = m.backup_sqlite(src, dest)

            self.assertEqual(r.status, "error")
            self.assertFalse(r.integrity_check["ok"])
            self.assertIn("integrity_check", r.detail)

    def test_corrupted_source_is_caught_at_the_backup_step_end_to_end(self):
        # Complementary real-world check: an actually-corrupt SOURCE
        # (not mocked) is caught too -- sqlite3's own .backup refuses a
        # malformed database, and backup_sqlite reports that as "error",
        # never as "ok".
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            src = root / "src.sqlite3"
            make_fixture_sqlite(src)
            full_size = src.stat().st_size
            with open(src, "r+b") as f:
                f.truncate(full_size // 2)

            dest = root / "backup" / "corpus-sqlite3.sqlite3"
            r = m.backup_sqlite(src, dest)
            self.assertEqual(r.status, "error")


class JsonlBackupAndRestore(unittest.TestCase):
    def test_backup_is_integrity_checked_and_restore_is_byte_identical_and_queryable(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            src = root / "ledger.jsonl"
            records = [{"id": 1, "state": "done"}, {"id": 2, "state": "open"}]
            src.write_text("\n".join(json.dumps(r) for r in records) + "\n")
            before = m.stat_snapshot(src)

            dest = root / "backup" / "estate-ledger-jsonl.jsonl"
            r = m.backup_jsonl(src, dest)

            self.assertEqual(r.status, "ok")
            self.assertTrue(r.integrity_check["ok"])
            self.assertEqual(r.integrity_check["total_records"], 2)
            self.assertEqual(r.integrity_check["malformed_count"], 0)

            after = m.stat_snapshot(src)
            self.assertEqual(before, after)
            self.assertTrue(r.live_untouched)

            restore_dir = root / "restore"
            restore_dir.mkdir()
            rt = m.restore_test_jsonl(dest, restore_dir)

            self.assertTrue(rt["byte_identical"])
            self.assertEqual(rt["query_proof"]["records_parsed"], 2)
            self.assertEqual(rt["query_proof"]["first_record_keys"], ["id", "state"])

    def test_malformed_json_line_is_caught_not_silently_backed_up_as_clean(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            src = root / "ledger.jsonl"
            src.write_text('{"id": 1}\nNOT JSON AT ALL\n{"id": 2}\n')

            dest = root / "backup" / "estate-ledger-jsonl.jsonl"
            r = m.backup_jsonl(src, dest)

            self.assertEqual(r.status, "error")
            self.assertEqual(r.integrity_check["malformed_count"], 1)
            self.assertEqual(r.integrity_check["malformed_lines"], [2])
            self.assertFalse(r.integrity_check["ok"])


class LiveUntouchedProof(unittest.TestCase):
    def test_live_source_size_and_mtime_survive_a_failed_backup_attempt_too(self):
        # Even when the backup step itself fails partway, the live
        # source's own before/after snapshot must still be captured and
        # compared -- untouched-ness is proven on every path, not just
        # the happy one.
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            src = root / "src.sqlite3"
            make_fixture_sqlite(src)
            before_bytes = src.read_bytes()

            # Point dest at a path whose parent cannot be created (a file
            # standing where a directory is needed) to force a failure
            # inside backup_sqlite after live_before was already taken.
            blocker = root / "blocker"
            blocker.write_text("occupying the path")
            dest = blocker / "cannot-create-here" / "dest.sqlite3"

            r = m.backup_sqlite(src, dest)
            self.assertIn(r.status, ("error",))
            self.assertEqual(src.read_bytes(), before_bytes)


if __name__ == "__main__":
    unittest.main()
