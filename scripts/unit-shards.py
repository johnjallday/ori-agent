#!/usr/bin/env python3
"""Partition the canonical unit package list; order a run heaviest-first;
validate/merge successful shards; refresh the weights from a full run.

Weights only affect placement and run order, never selection. Refresh them
from a fresh (-count=1) full run's go test -json stream:

    python3 scripts/unit-shards.py weights < events.json > scripts/unit-shard-weights.json

No external packages required.
"""

import argparse
import json
import math
from pathlib import Path
import re
import sys

MODULE = "github.com/johnjallday/ori-agent/"
FLAGS = "-json -short -race -timeout 30m -count=1 -covermode=atomic"
WEIGHTS = Path(__file__).with_name("unit-shard-weights.json")
DEFAULT_WEIGHT = 10
BLOCK = re.compile(r"^(\S+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$")


def packages(text):
    result = text.splitlines()
    if not result or any(
        not re.fullmatch(re.escape(MODULE) + r"[A-Za-z0-9_./-]+", p)
        or "/../" in p
        for p in result
    ):
        raise ValueError("empty or invalid unit package list")
    if len(result) != len(set(result)):
        raise ValueError("duplicate unit package")
    return sorted(result)


def load_weights():
    weights = json.loads(WEIGHTS.read_text())
    if not isinstance(weights, dict) or any(
        not isinstance(value, (int, float))
        or isinstance(value, bool)
        or not math.isfinite(value)
        or value <= 0
        for value in weights.values()
    ):
        raise ValueError("invalid shard weights")
    return weights


def order(selected, weights):
    """Heaviest package first, ties alphabetical.

    `go test -p N` starts its package list in order, so the package that decides
    a run's wall clock must not queue behind a hundred quick ones.
    """
    return sorted(selected, key=lambda p: (-weights.get(p, DEFAULT_WEIGHT), p))


def weights_from_events(text):
    """Whole seconds per package from a go test -json stream.

    Only packages above the default weight are kept, so the file stays short.
    A cached package reports no time and drops out, so refresh from a fresh run.
    """
    durations = {}
    for line in text.splitlines():
        if not line.strip():
            continue
        event = json.loads(line)
        if event.get("Action") in ("pass", "fail") and "Test" not in event and event.get("Package"):
            durations[event["Package"]] = event.get("Elapsed", 0)
    if not durations:
        raise ValueError("no package results in the event stream")
    packages("\n".join(durations))
    return {
        package: math.ceil(seconds)
        for package, seconds in sorted(durations.items())
        if math.ceil(seconds) > DEFAULT_WEIGHT
    }


def partition(selected, count, weights):
    if not 1 <= count <= min(32, len(selected)):
        raise ValueError("shard count must be 1..32 with no empty shard")
    # LPT with stable ties: independent runners must compute the same partition.
    groups, totals = [[] for _ in range(count)], [0] * count
    for package in order(selected, weights):
        shard = min(range(count), key=lambda i: (totals[i], len(groups[i]), i))
        groups[shard].append(package)
        totals[shard] += weights.get(package, DEFAULT_WEIGHT)
    return [sorted(group) for group in groups]


def merge(directory, selected, count, weights, revision):
    expected = partition(selected, count, weights)
    names = {f"unit-results-{i}" for i in range(1, count + 1)}
    if {path.name for path in directory.iterdir()} != names:
        raise ValueError("missing or unexpected unit result artifacts")
    blocks = {}
    for index, group in enumerate(expected, 1):
        root = directory / f"unit-results-{index}"
        if packages((root / "packages.txt").read_text()) != group:
            raise ValueError(f"shard {index}: missing, duplicate or misplaced packages")
        if packages((root / "all-packages.txt").read_text()) != selected:
            raise ValueError(f"shard {index}: canonical package list differs")
        metadata = (root / "metadata.txt").read_text().splitlines()
        for field in (
            f"revision={revision}",
            f"scope=shard-{index}-of-{count}",
            f"flags={FLAGS}",
            "test_exit=0",
        ):
            if metadata.count(field) != 1:
                raise ValueError(f"shard {index}: missing successful-run identity {field}")
        lines = (root / "coverage.txt").read_text().splitlines()
        if not lines or lines[0] != "mode: atomic":
            raise ValueError(f"shard {index}: missing atomic coverage header")
        for line in lines[1:]:
            match = BLOCK.fullmatch(line)
            if not match:
                raise ValueError(f"shard {index}: malformed coverage block")
            filename, *numbers = match.groups()
            if filename.rsplit("/", 1)[0] not in group:
                raise ValueError(f"shard {index}: coverage outside selected packages")
            key = line.rsplit(" ", 2)[0]
            statements, hits = map(int, numbers[-2:])
            if key in blocks:
                # With package-only coverage, every source block belongs to
                # exactly one package/shard. Never silently double-count it.
                raise ValueError(f"shard {index}: duplicate coverage block")
            blocks[key] = (statements, hits)
    if not blocks:
        raise ValueError("empty combined coverage")
    return "mode: atomic\n" + "".join(
        f"{key} {statements} {hits}\n"
        for key, (statements, hits) in sorted(blocks.items())
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    select = commands.add_parser("select", help="read canonical packages from stdin")
    select.add_argument("--shard", type=int, required=True)
    select.add_argument("--count", type=int, required=True)
    commands.add_parser("order", help="print the packages read from stdin heaviest first")
    commands.add_parser("weights", help="print package weights from a go test -json stream on stdin")
    combine = commands.add_parser("merge", help="verify all shards before emitting coverage")
    combine.add_argument("--directory", type=Path, required=True)
    combine.add_argument("--packages", type=Path, required=True)
    combine.add_argument("--count", type=int, required=True)
    combine.add_argument("--revision", required=True)
    args = parser.parse_args()
    try:
        if args.command == "weights":
            print(json.dumps(weights_from_events(sys.stdin.read()), indent=2))
            return 0
        weights = load_weights()
        if args.command == "select":
            if not 1 <= args.shard <= args.count:
                raise ValueError("shard index must be 1..count")
            groups = partition(packages(sys.stdin.read()), args.count, weights)
            print("\n".join(groups[args.shard - 1]))
        elif args.command == "order":
            print("\n".join(order(packages(sys.stdin.read()), weights)))
        else:
            selected = packages(args.packages.read_text())
            print(merge(args.directory, selected, args.count, weights, args.revision), end="")
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(f"unit shards: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
