import json
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("agent_contract_release.py").resolve()
MANIFEST = "agents/contract/contract.json"


class ReleaseNotesTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git("init", "-q")
        self.git("config", "user.name", "Contract test")
        self.git("config", "user.email", "contract@example.invalid")
        self.git("config", "tag.gpgSign", "false")
        self.git("config", "commit.gpgSign", "false")
        self.git("commit", "--allow-empty", "-qm", "initial")

    def git(self, *args):
        return subprocess.run(["git", *args], cwd=self.root, check=True,
                              capture_output=True, text=True, timeout=10).stdout

    def manifest(self, version=1, capabilities=None):
        path = self.root / MANIFEST
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps({"protocolVersion": version,
                                    "capabilities": capabilities or ["container-recovery-scope/v1"]}))

    def previous(self):
        self.git("add", ".")
        self.git("commit", "--allow-empty", "-qm", "previous")
        self.git("tag", "v0.3.38")

    def notes(self):
        return subprocess.run(["python3", str(SCRIPT), "--version", "0.3.39"],
                              cwd=self.root, check=True, capture_output=True, text=True, timeout=10).stdout

    def test_first_declaration_after_legacy_release(self):
        self.previous()
        self.manifest()
        self.assertIn("Contract introduced", self.notes())
        self.assertIn("adopt protocol v1 once", self.notes())

    def test_unchanged_core_bump(self):
        self.manifest()
        self.previous()
        self.git("tag", "not-a-release")
        self.assertIn("Contract unchanged from v0.3.38", self.notes())
        self.assertIn("No agent rebuild", self.notes())

    def test_protocol_change(self):
        self.manifest()
        self.previous()
        self.manifest(version=2)
        self.assertIn("Protocol changed from 1", self.notes())
        self.assertNotIn("No agent rebuild", self.notes())

    def test_capability_addition_and_removal(self):
        self.manifest(capabilities=["old/v1"])
        self.previous()
        self.manifest(capabilities=["new/v1"])
        notes = self.notes()
        self.assertIn("Added: `new/v1`", notes)
        self.assertIn("Removed: `old/v1`", notes)
        self.assertIn("Protocol unchanged", notes)
        self.assertNotIn("No agent rebuild", notes)

    def test_capability_order_does_not_change_contract(self):
        self.manifest(capabilities=["a/v1", "b/v1"])
        self.previous()
        self.manifest(capabilities=["b/v1", "a/v1"])
        self.assertIn("Contract unchanged", self.notes())

    def test_broken_previous_manifest_is_an_error(self):
        self.manifest()
        (self.root / MANIFEST).write_text("not json")
        self.previous()
        self.manifest()
        with self.assertRaises(subprocess.CalledProcessError):
            self.notes()


if __name__ == "__main__":
    unittest.main()
