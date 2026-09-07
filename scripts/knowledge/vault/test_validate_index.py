import contextlib, importlib.util, io, pathlib, tempfile, unittest
P = pathlib.Path(__file__).with_name('validate_index.py')
spec = importlib.util.spec_from_file_location('validator', P)
v = importlib.util.module_from_spec(spec); spec.loader.exec_module(v)
class Paths(unittest.TestCase):
    def check(self, target, exists=True, alias=False):
        with tempfile.TemporaryDirectory() as d:
            root=pathlib.Path(d); agent=root/'agent'; (agent/'facts').mkdir(parents=True); (root/'01 - Notes').mkdir()
            p=(root/target.removeprefix('../')) if '01p - Parameters' in target else (root/'01 - Notes/202607120001.md') if ('Notes' in target or alias) else agent/'facts/test.md'
            p.parent.mkdir(parents=True,exist_ok=True)
            if exists: p.write_text('---\ntype: user\ncreated: 2026-07-12\nsource: fixture\nid: "202607120001"\naliases: [test]\n---\n# Test\n')
            (agent/'index.md').write_text('- '+('[[test]]' if alias else '[Test]('+target+')')+' — fixture\n')
            v.vault_root=lambda: str(agent)
            with contextlib.redirect_stdout(io.StringIO()): return v.main()
    def test_nested_valid(self): self.assertEqual(self.check('../01 - Notes/01p - Parameters/202607120001.md'),0)
    def test_old_valid(self): self.assertEqual(self.check('facts/test.md'),0)
    def test_new_valid(self): self.assertEqual(self.check('../01 - Notes/202607120001.md'),0)
    def test_encoded_valid(self): self.assertEqual(self.check('../01%20-%20Notes/202607120001.md'),0)
    def test_alias_valid(self): self.assertEqual(self.check('',alias=True),0)
    def test_old_broken(self): self.assertEqual(self.check('facts/test.md',False),1)
    def test_new_broken(self): self.assertEqual(self.check('../01 - Notes/202607120001.md',False),1)
    def test_wrong_directory(self): self.assertEqual(self.check('facts/202607120001.md',True,False),1)
if __name__=='__main__': unittest.main()
