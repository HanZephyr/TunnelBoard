#!/usr/bin/env python3
"""兼容入口：从统一供应链清单下载并校验 Caddy。"""

import argparse
from pathlib import Path

from release import ReleaseError, fetch_caddy


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", default="windows-amd64")
    parser.add_argument("--output", type=Path, default=Path("build/caddy/caddy.exe"))
    args = parser.parse_args()
    try:
        entry = fetch_caddy(args.target, args.output)
    except ReleaseError as exc:
        print(f"CADDY-FAIL: {exc}")
        return 1
    print(f"CADDY OK v{entry['version']} {args.target}: {args.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
