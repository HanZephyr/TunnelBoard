#!/usr/bin/env python3
"""打包 Windows 发行目录（build/bin/）：主程序 + 受限 helper + 钉版 Caddy。

用法：uv run scripts/package-windows.py
产物：build/bin/tunnelboard.exe、build/bin/tunnelboard-helper.exe、build/bin/caddy/caddy.exe
"""
import hashlib
import os
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BIN = ROOT / "build" / "bin"
GO = r"C:\Program Files\Go\bin\go.exe"
EXPECTED_CADDY_SHA256 = "5cb9ab71e5756ce72840b8234177a2f40c8b4ab47a806b8e841e2b784e9df62b"


def run(cmd: list[str], cwd: Path | None = None) -> None:
    print("+", " ".join(str(c) for c in cmd))
    # shell=True 以兼容 corepack 等 .cmd shim；list2cmdline 处理含空格路径的引号
    result = subprocess.run(subprocess.list2cmdline([str(c) for c in cmd]), cwd=cwd or ROOT, shell=True)
    if result.returncode != 0:
        sys.exit(f"命令失败（{result.returncode}）：{cmd}")


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main() -> int:
    # 1. 前端构建（pnpm shim 在本环境不可靠，走 corepack；wails build -s 跳过前端）
    run(["corepack", "pnpm", "build"], cwd=ROOT / "frontend")
    # 2. 钉版 Caddy（不存在或哈希不符时下载校验）
    run(["uv", "run", "scripts/fetch-caddy.py"])
    # 3. 编译主程序（跳过前端构建；-clean 会清空输出目录，helper 须在其后编译）
    run(["wails", "build", "-platform", "windows/amd64", "-clean", "-s"])
    # 4. 编译 helper
    BIN.mkdir(parents=True, exist_ok=True)
    run([GO, "build", "-o", str(BIN / "tunnelboard-helper.exe"), "./cmd/helper"])
    # 5. 复制 Caddy 到 exe 同级并校验
    caddy_dst = BIN / "caddy"
    caddy_dst.mkdir(exist_ok=True)
    shutil.copy2(ROOT / "build" / "caddy" / "caddy.exe", caddy_dst / "caddy.exe")

    for name in ["tunnelboard.exe", "tunnelboard-helper.exe", "caddy/caddy.exe"]:
        artifact = BIN / name
        if not artifact.exists():
            sys.exit(f"缺少产物：{artifact}")
        print(f"  {name}: {artifact.stat().st_size} bytes sha256={sha256(artifact)}")
    if sha256(caddy_dst / "caddy.exe") != EXPECTED_CADDY_SHA256:
        sys.exit("caddy.exe 完整性校验失败")
    print("打包完成：", BIN)
    return 0


if __name__ == "__main__":
    sys.exit(main())
