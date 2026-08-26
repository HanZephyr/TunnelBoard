"""Build a PNG-backed macOS ICNS file from square PNG source images.

This keeps iconfile.icns reproducible from build/appicon.svg even when the
current host does not have macOS's iconutil installed.
"""

from __future__ import annotations

import argparse
import struct
from pathlib import Path


ICON_SPECS = (
    ("icp4", 16),
    ("icp5", 32),
    ("icp6", 64),
    ("ic07", 128),
    ("ic08", 256),
    ("ic09", 512),
    ("ic10", 1024),
)
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"


def read_square_png(path: Path, expected_size: int) -> bytes:
    data = path.read_bytes()
    if data[:8] != PNG_SIGNATURE or len(data) < 24:
        raise ValueError(f"{path} is not a PNG")
    width, height = struct.unpack(">II", data[16:24])
    if (width, height) != (expected_size, expected_size):
        raise ValueError(
            f"{path} must be {expected_size}x{expected_size}, got {width}x{height}"
        )
    return data


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()

    chunks = []
    for chunk_name, size in ICON_SPECS:
        png = read_square_png(args.input / f"{size}.png", size)
        chunks.append(chunk_name.encode("ascii") + struct.pack(">I", len(png) + 8) + png)

    payload = b"".join(chunks)
    args.output.write_bytes(b"icns" + struct.pack(">I", len(payload) + 8) + payload)


if __name__ == "__main__":
    main()
