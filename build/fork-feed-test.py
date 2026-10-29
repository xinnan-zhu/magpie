"""Exercise release publication guards without downloading or building binaries."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("fork-feed.py")
NAMES = [
    "magpie-darwin-arm64.zip", "magpie-windows-amd64.exe", "magpie-windows-arm64.exe",
    "magpie-cli-windows-amd64.exe", "magpie-cli-windows-arm64.exe",
    "magpie-cli-linux-amd64", "magpie-cli-linux-arm64",
]


class FeedTests(unittest.TestCase):
    def test_publication_checks_uploaded_bytes(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            notes = root / "notes.md"
            notes.write_text("fixture release")
            for name in NAMES:
                (root / name).write_bytes(name.encode())
            env = dict(os.environ, GITHUB_REPOSITORY="fixture/magpie")
            subprocess.run([sys.executable, SCRIPT, "0.1.1-ssh.1", root, notes], env=env, check=True)
            feed = json.loads((root / "latest.json").read_text())
            self.assertEqual(set(feed["assets"]), set(NAMES))
            self.assertEqual(feed["assets"][NAMES[0]]["sha256"], hashlib.sha256(NAMES[0].encode()).hexdigest())
            metadata = root / "assets.json"
            assets = [{"name": n, "state": "uploaded", "size": (root / n).stat().st_size,
                       "digest": "sha256:" + hashlib.sha256((root / n).read_bytes()).hexdigest()}
                      for n in NAMES + ["latest.json"]]
            metadata.write_text(json.dumps({"assets": assets}))

            def verify():
                return subprocess.run([sys.executable, SCRIPT, "--verify", root, metadata],
                                      capture_output=True).returncode

            self.assertEqual(verify(), 0)
            # Same length: size alone must never be enough to publish.
            (root / NAMES[0]).write_bytes(b"x" * len(NAMES[0]))
            self.assertNotEqual(verify(), 0)
            (root / NAMES[0]).write_bytes(NAMES[0].encode())
            metadata.write_text(json.dumps({"assets": assets[:-1]}))
            self.assertNotEqual(verify(), 0)
            assets[0]["state"] = "starter"
            metadata.write_text(json.dumps({"assets": assets}))
            self.assertNotEqual(verify(), 0)


if __name__ == "__main__":
    unittest.main()
