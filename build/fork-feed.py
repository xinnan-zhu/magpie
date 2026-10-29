"""Create and verify the personal release's update feed in GitHub Actions."""
import hashlib
import json
import os
from pathlib import Path
import re
import sys

NAMES = [
    "magpie-darwin-arm64.zip",
    "magpie-windows-amd64.exe",
    "magpie-windows-arm64.exe",
    "magpie-cli-windows-amd64.exe",
    "magpie-cli-windows-arm64.exe",
    "magpie-cli-linux-amd64",
    "magpie-cli-linux-arm64",
]


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


if sys.argv[1] == "--verify":
    root, metadata = Path(sys.argv[2]), Path(sys.argv[3])
    assets = json.loads(metadata.read_text())["assets"]
    assert {a["name"] for a in assets} == set(NAMES + ["latest.json"])
    for a in assets:
        p = root / a["name"]
        assert a["state"] == "uploaded" and a["size"] == p.stat().st_size
        assert a["digest"] == "sha256:" + digest(p), a["name"]
    print("All eight uploaded assets match the build.")
else:
    version, root, notes = sys.argv[1], Path(sys.argv[2]), Path(sys.argv[3])
    assert re.fullmatch(r"\d+\.\d+\.\d+-ssh\.\d+", version)
    repo = os.environ["GITHUB_REPOSITORY"]
    base = f"https://github.com/{repo}/releases/download/v{version}/"
    assets = {}
    for name in NAMES:
        path = root / name
        assets[name] = {"url": base + name, "size": path.stat().st_size, "sha256": digest(path)}
    release = {"version": version, "notes": notes.read_text(),
               "url": f"https://github.com/{repo}/releases/tag/v{version}", "assets": assets}
    (root / "latest.json").write_text(json.dumps(release, indent=2, ensure_ascii=False) + "\n")
