#!/usr/bin/env python3
"""Make a *new* split profile for demo-server.sh. Never takes an existing HOME."""
import argparse
import os
from pathlib import Path
import tempfile


def build(parent: Path, large: bool) -> Path:
    sandbox = Path(tempfile.mkdtemp(prefix="ori-search-demo-", dir=parent)).resolve()
    for name in ("Documents/Rough", "Documents/Final", "Downloads", "Music", "Empty", "Partial/Locked", "Moved", "Unseen"):
        (sandbox / "home" / name).mkdir(parents=True, mode=0o700)
    home = sandbox / "home"
    for folder, stamp in (("Documents/Rough", 1704067200), ("Documents/Final", 1735689600)):
        doc = home / folder / "Harbor.txt"
        doc.write_text("Synthetic mix notes: " + folder.split("/")[-1] + "\n")
        os.utime(doc, (stamp, stamp))
    (home / "Documents" / "Welcome.txt").touch()
    (home / "Music" / "Harbor recording.txt").touch()
    (home / "Partial" / "Visible.txt").touch()
    (home / "Partial" / "Locked" / "Not readable.txt").touch()
    (home / "Partial" / "Locked").chmod(0)
    (home / "Moved" / "Move after mapping.txt").write_text("Synthetic stale selection.\n")
    (home / "Unseen" / "Decoy.txt").touch()
    (home / "Documents" / "outside-link").symlink_to(home / "Unseen", target_is_directory=True)
    if large:
        for n in range(100):
            directory = home / "Documents" / f"collection-{n:03}"
            directory.mkdir()
            for i in range(200):
                (directory / f"mix-{i:03}.txt").touch()
    # demo-server will create the app root. A fabricated receipt in data would
    # only test the renderer, not the real exclusive-creation decision.
    (sandbox / ".search-first-fixture").write_text("synthetic-v1\n")
    return sandbox


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("parent", type=Path, help="existing scratch directory for a fresh sandbox")
    parser.add_argument("--large", action="store_true")
    args = parser.parse_args()
    print(build(args.parent, args.large))
