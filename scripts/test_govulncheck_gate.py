"""The vulnerability gate answers only when the scanner did.

scripts/govulncheck.sh once swallowed every non-zero status of govulncheck and
reported "No vulnerabilities found" when the scanner could not fetch its
database: a gate green about nothing. These run the real script against a
stub scanner on PATH and require a non-zero status, with no clean verdict,
when the scanner fails — and the clean verdict when it reports nothing.
"""

import os
import stat
import subprocess
import tempfile
import unittest

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
SCRIPT = os.path.join(ROOT, "scripts", "govulncheck.sh")


def _run_with_stub_scanner(body):
    with tempfile.TemporaryDirectory() as bin_dir:
        stub = os.path.join(bin_dir, "govulncheck")
        with open(stub, "w", encoding="utf-8") as f:
            f.write("#!/bin/sh\n" + body)
        os.chmod(stub, os.stat(stub).st_mode | stat.S_IEXEC)
        env = dict(os.environ, PATH=bin_dir + os.pathsep + os.environ.get("PATH", ""))
        return subprocess.run(
            ["bash", SCRIPT], cwd=ROOT, env=env, capture_output=True, text=True, check=False
        )


class GovulncheckGateTest(unittest.TestCase):
    def test_a_scanner_that_fails_fails_the_gate_with_no_clean_verdict(self):
        for status in (1, 2):
            with self.subTest(status=status):
                result = _run_with_stub_scanner(
                    'echo "govulncheck: fetching vulnerability database: connection refused" >&2\nexit %d\n' % status
                )
                self.assertNotEqual(0, result.returncode)
                self.assertIn("did not complete", result.stderr)
                self.assertIn("connection refused", result.stderr)
                self.assertNotIn("No vulnerabilities found", result.stdout + result.stderr)

    def test_a_scanner_that_reports_nothing_is_the_clean_verdict(self):
        result = _run_with_stub_scanner('echo "No vulnerabilities found."\nexit 0\n')
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("No vulnerabilities found", result.stdout)

    def test_a_scanner_that_found_something_is_partitioned_not_refused_outright(self):
        # Exit 3 is "vulnerabilities found": the script partitions them itself.
        # An actionable one with a fix fails the gate as actionable, not as a
        # scanner failure.
        result = _run_with_stub_scanner(
            'cat <<"V"\nVulnerability #1: GO-2099-0001\n    Fixed in: example.org/m@v1.2.3\nV\nexit 3\n'
        )
        self.assertNotIn("did not complete", result.stderr)


if __name__ == "__main__":
    unittest.main()
