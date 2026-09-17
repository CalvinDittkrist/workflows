import json
import unittest

from helpers import STANDARDS, ShimTest


class StandardsTests(ShimTest):
    def test_scaffold_then_check_passes_and_never_overwrites(self):
        r = self.run_script(STANDARDS / "check.sh")
        self.assertEqual(r.returncode, 1)
        self.assertIn("fail: docs/architecture.md missing", r.stdout)
        r = self.run_script(STANDARDS / "scaffold.sh")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("created: docs/architecture.md", r.stdout)
        self.assertIn("created: .claude/settings.json", r.stdout)
        settings = json.loads((self.repo / ".claude/settings.json").read_text())
        self.assertTrue(settings["enabledPlugins"]["worker@workflows"])
        self.assertEqual(settings["attribution"]["commit"], "")
        (self.repo / "CLAUDE.md").write_text("# mine\n")
        r = self.run_script(STANDARDS / "scaffold.sh")
        self.assertIn("kept: CLAUDE.md", r.stdout)
        self.assertEqual((self.repo / "CLAUDE.md").read_text(), "# mine\n")
        r = self.run_script(STANDARDS / "check.sh")
        self.assertEqual(r.returncode, 0, r.stdout)
        self.assertIn("result: pass", r.stdout)

    def test_new_adr_numbers_sequentially_and_indexes(self):
        self.run_script(STANDARDS / "scaffold.sh")
        r1 = self.run_script(STANDARDS / "new-adr.sh", "Use", "Postgres")
        r2 = self.run_script(STANDARDS / "new-adr.sh", "Drop Redis!")
        self.assertIn("created: docs/adr/0001-use-postgres.md", r1.stdout)
        self.assertIn("created: docs/adr/0002-drop-redis.md", r2.stdout)
        adr = (self.repo / "docs/adr/0001-use-postgres.md").read_text()
        self.assertIn("# 0001. Use Postgres", adr)
        self.assertIn("Status: proposed", adr)
        index = (self.repo / "docs/adr/README.md").read_text()
        self.assertIn("| [0002](0002-drop-redis.md) | Drop Redis! | proposed |", index)
        r = self.run_script(STANDARDS / "check.sh")
        self.assertIn("ok: ADRs: 2", r.stdout)


if __name__ == "__main__":
    unittest.main()
