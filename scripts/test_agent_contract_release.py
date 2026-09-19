import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from agent_contract_release import publication_steps

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

    def manifest(self, version=1, capabilities=None, startup=2):
        path = self.root / MANIFEST
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps({"protocolVersion": version,
                                    "startupProtocolVersion": startup,
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

    def test_startup_change_is_not_compatible(self):
        self.manifest(startup=1)
        self.previous()
        self.manifest(startup=2)
        notes = self.notes()
        self.assertIn("Startup protocol changed from 1", notes)
        self.assertNotIn("No agent rebuild", notes)

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


class PublicationRecoveryTest(unittest.TestCase):
    def test_new_release_verifies_asset_before_publication(self):
        self.assertEqual(publication_steps(None),
                         ("create-draft", "upload", "verify", "publish"))

    def test_interrupted_before_upload_resumes_draft(self):
        self.assertEqual(publication_steps({"draft": True, "assets": []}),
                         ("upload", "verify", "publish"))

    def test_interrupted_after_upload_still_publishes(self):
        release = {"draft": True, "assets": [{"name": "contract.json"}]}
        self.assertEqual(publication_steps(release), ("upload", "verify", "publish"))

    def test_published_release_is_verified_without_mutation(self):
        release = {"draft": False, "assets": [{"name": "contract.json"}]}
        self.assertEqual(publication_steps(release), ("verify",))

    def test_published_release_without_manifest_is_not_complete(self):
        with self.assertRaisesRegex(ValueError, "missing contract.json"):
            publication_steps({"draft": False, "assets": []})


if __name__ == "__main__":
    unittest.main()
