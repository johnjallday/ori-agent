#!/usr/bin/env python3
"""Offline contract: sharding cannot silently narrow the unit/coverage gate."""

import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("unit-shards.py")
spec = importlib.util.spec_from_file_location("unit_shards", SCRIPT)
shards = importlib.util.module_from_spec(spec)
spec.loader.exec_module(shards)


class UnitShardsTest(unittest.TestCase):
    def setUp(self):
        self.packages = [shards.MODULE + f"internal/p{i}" for i in range(8)]
        self.weights = {p: (8 - i) * 10 for i, p in enumerate(self.packages)}
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.groups = shards.partition(self.packages, 4, self.weights)
        for i, group in enumerate(self.groups, 1):
            directory = self.root / f"unit-results-{i}"
            directory.mkdir()
            (directory / "packages.txt").write_text("\n".join(group) + "\n")
            (directory / "all-packages.txt").write_text("\n".join(self.packages) + "\n")
            (directory / "metadata.txt").write_text(
                f"revision=abc\nscope=shard-{i}-of-4\nflags={shards.FLAGS}\ntest_exit=0\n"
            )
            (directory / "coverage.txt").write_text(
                "mode: atomic\n"
                + "".join(f"{p}/file.go:1.1,2.2 2 {i}\n" for p in group)
            )

    def merge(self):
        return shards.merge(self.root, self.packages, 4, self.weights, "abc")

    def test_complete_balanced_deterministic_partition(self):
        self.assertEqual(self.groups, shards.partition(self.packages[::-1], 4, self.weights))
        self.assertEqual(sorted(p for group in self.groups for p in group), self.packages)
        self.assertEqual([sum(self.weights[p] for p in group) for group in self.groups], [90] * 4)
        extended = self.packages + [shards.MODULE + "internal/new_package"]
        self.assertEqual(
            sorted(p for group in shards.partition(extended, 4, self.weights) for p in group),
            sorted(extended),
        )
        # Stale weights never reintroduce a deleted package.
        shortened = self.packages[1:]
        self.assertEqual(
            sorted(p for group in shards.partition(shortened, 4, self.weights) for p in group),
            shortened,
        )

    def test_invalid_counts_and_package_lists_fail(self):
        for count in (0, -1, 9, 33):
            with self.subTest(count=count), self.assertRaises(ValueError):
                shards.partition(self.packages, count, self.weights)
        for text in ("", "./...\n", self.packages[0] + "\n" + self.packages[0], "../evil"):
            with self.subTest(text=text), self.assertRaises(ValueError):
                shards.packages(text)

    def test_merge_keeps_all_blocks_and_counts(self):
        merged = self.merge().splitlines()
        self.assertEqual(merged[0], "mode: atomic")
        self.assertEqual(len(merged), 9)
        self.assertEqual(merged[1:], sorted(merged[1:]))
        for i, group in enumerate(self.groups, 1):
            for p in group:
                self.assertIn(f"{p}/file.go:1.1,2.2 2 {i}", merged)

    def test_missing_or_unexpected_artifacts_fail(self):
        path = self.root / "unit-results-4"
        path.rename(self.root / "unexpected")
        with self.assertRaises(ValueError):
            self.merge()
        (self.root / "unexpected").rename(path)
        (self.root / "extra").mkdir()
        with self.assertRaises(ValueError):
            self.merge()

    def test_corrupt_or_incomplete_success_evidence_fails(self):
        root = self.root / "unit-results-1"
        mutations = {
            "packages.txt": ["", "\n".join(self.groups[0] * 2), "\n".join(self.groups[1])],
            "all-packages.txt": ["\n".join(self.packages[:-1])],
            "metadata.txt": [
                "revision=wrong\nscope=shard-1-of-4\nflags=" + shards.FLAGS + "\ntest_exit=0\n",
                "revision=abc\nscope=full\nflags=" + shards.FLAGS + "\ntest_exit=0\n",
                "revision=abc\nscope=shard-1-of-4\nflags=" + shards.FLAGS + "\ntest_exit=1\n",
                "revision=abc\nscope=shard-1-of-4\nflags=-short\ntest_exit=0\n",
            ],
            "coverage.txt": [
                "", "mode: set\n", "mode: atomic\ninvalid\n",
                f"mode: atomic\n{self.groups[1][0]}/file.go:1.1,2.2 2 1\n",
                "mode: atomic\n" + f"{self.groups[0][0]}/file.go:1.1,2.2 2 1\n" * 2,
            ],
        }
        for filename, contents in mutations.items():
            path = root / filename
            original = path.read_text()
            for content in contents:
                with self.subTest(filename=filename, content=content):
                    path.write_text(content)
                    with self.assertRaises(ValueError):
                        self.merge()
            path.unlink()
            with self.assertRaises(OSError):
                self.merge()
            path.write_text(original)

    def test_cli_selects_every_package_once(self):
        groups = []
        for i in range(1, 5):
            result = subprocess.run(
                [sys.executable, str(SCRIPT), "select", "--shard", str(i), "--count", "4"],
                input="\n".join(self.packages), text=True, capture_output=True, check=True,
            )
            groups.extend(result.stdout.splitlines())
        self.assertEqual(sorted(groups), self.packages)
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "select", "--shard", "5", "--count", "4"],
            input="\n".join(self.packages), text=True, capture_output=True, check=False,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")


if __name__ == "__main__":
    unittest.main()
