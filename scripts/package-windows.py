#!/usr/bin/env python3
"""兼容入口：调用唯一 release Module 构建 Windows 候选产物。"""

import argparse

from release import ReleaseError, build_target, sha256_file


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", default="0.0.0-local")
    parser.add_argument("--require-signing", action="store_true")
    parser.add_argument("--skip-installer", action="store_true")
    args = parser.parse_args()
    try:
        outputs = build_target("windows-amd64", args.version, args.require_signing, args.skip_installer)
    except ReleaseError as exc:
        print(f"RELEASE-FAIL: {exc}")
        return 1
    for output in outputs:
        print(f"ARTIFACT {output} sha256={sha256_file(output)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
