#!/usr/bin/env python3
"""Compare completed scans of the same directory with ncdu and disk-explorer."""

import argparse
import random
import shutil
import statistics
import subprocess
import time
from pathlib import Path


def elapsed(command):
    start = time.perf_counter()
    subprocess.run(command, stdout=subprocess.DEVNULL, check=True)
    return time.perf_counter() - start


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("path", type=Path, help="directory scanned by both tools")
    parser.add_argument("--disk-explorer", default="./disk-explorer")
    parser.add_argument("--scan-workers", type=int, default=0,
                        help="disk-explorer worker limit (0 = automatic)")
    parser.add_argument("--rounds", type=int, default=8)
    args = parser.parse_args()
    if not args.path.is_dir():
        parser.error("path must be a directory")
    if args.rounds < 1:
        parser.error("--rounds must be positive")
    if not 0 <= args.scan_workers <= 64:
        parser.error("--scan-workers must be between 0 and 64")
    if not shutil.which("ncdu"):
        parser.error("ncdu is required")

    path = str(args.path.resolve())
    commands = {
        "disk-explorer": [args.disk_explorer, "--summary", "--scan-workers",
                          str(args.scan_workers), path],
        "ncdu": ["ncdu", "-0", "--ignore-config", "-o", "/dev/null", path],
    }
    for command in commands.values():
        elapsed(command)  # warm filesystem caches

    samples = {name: [] for name in commands}
    for round_number in range(args.rounds):
        names = list(commands)
        random.Random(round_number).shuffle(names)
        for name in names:
            samples[name].append(elapsed(commands[name]))

    for name, values in samples.items():
        print(f"{name}: {statistics.median(values) * 1000:.1f} ms median "
              f"({args.rounds} runs)")
    ratio = statistics.median(samples["ncdu"]) / statistics.median(samples["disk-explorer"])
    print(f"ncdu / disk-explorer: {ratio:.2f}x")


if __name__ == "__main__":
    main()
