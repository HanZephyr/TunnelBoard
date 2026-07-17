#!/usr/bin/env python3
"""获取随安装包内置的固定版本 Caddy 二进制（不在首次使用时下载，见 CONTEXT.md:75）。

用法：uv run scripts/fetch-caddy.py
产物：build/caddy/caddy.exe（已 .gitignore），SHA-256 与代码内钉版值一致。
"""
import hashlib
import io
import sys
import urllib.request
import zipfile
from pathlib import Path

VERSION = "2.11.4"
EXPECTED_SHA256 = "5cb9ab71e5756ce72840b8234177a2f40c8b4ab47a806b8e841e2b784e9df62b"
URL = f"https://github.com/caddyserver/caddy/releases/download/v{VERSION}/caddy_{VERSION}_windows_amd64.zip"
TARGET = Path(__file__).resolve().parent.parent / "build" / "caddy" / "caddy.exe"


def main() -> int:
    if TARGET.exists():
        digest = hashlib.sha256(TARGET.read_bytes()).hexdigest()
        if digest == EXPECTED_SHA256:
            print(f"caddy v{VERSION} 已存在且哈希匹配：{TARGET}")
            return 0
        print(f"现有文件哈希不匹配（{digest}），重新下载", file=sys.stderr)

    print(f"下载 caddy v{VERSION}：{URL}")
    with urllib.request.urlopen(URL, timeout=120) as resp:
        payload = resp.read()

    with zipfile.ZipFile(io.BytesIO(payload)) as zf:
        binary = zf.read("caddy.exe")

    digest = hashlib.sha256(binary).hexdigest()
    if digest != EXPECTED_SHA256:
        print(f"完整性校验失败：got {digest}, want {EXPECTED_SHA256}", file=sys.stderr)
        return 1

    TARGET.parent.mkdir(parents=True, exist_ok=True)
    TARGET.write_bytes(binary)
    print(f"已写入 {TARGET}（sha256={digest}）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
