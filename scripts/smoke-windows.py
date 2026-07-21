#!/usr/bin/env python3
"""冒烟即验收：对打包产物在真实 Windows 权限模型下跑最小闭环。

流程：产物预检 → 会话 Helper 首次 UAC → 同一 selfcheck 生命周期内复用管道 →
受托管 hosts 写入与回读 → 本地 HTTP 上游 + Caddy 启动 → CurrentUser CA 信任 →
curl 经系统信任库访问 https://<smoke 域名> → 全量清理（撤 hosts / 停 Caddy / 撤 CA）。

用法：uv run scripts/smoke-windows.py
前置：先执行 uv run scripts/package-windows.py 产出 build/bin。
"""
import http.server
import os
import shutil
import subprocess
import sys
import tempfile
import threading
from functools import partial
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BIN = ROOT / "build" / "bin"
GO = r"C:\Program Files\Go\bin\go.exe"
SELFCHECK = BIN / "selfcheck.exe"
DOMAIN = "tunnelboard-smoke.test"
UPSTREAM_PORT = 18099
HOSTS = Path(os.environ.get("SystemRoot", r"C:\Windows")) / "System32" / "drivers" / "etc" / "hosts"
SMOKE_TEXT = "TUNNELBOARD-SMOKE-OK"


def run(cmd: list[str], check: bool = True, capture: bool = False, cwd: Path | None = None) -> subprocess.CompletedProcess:
    print("+", " ".join(str(c) for c in cmd))
    result = subprocess.run(
        [str(c) for c in cmd], capture_output=capture, text=True, encoding="utf-8", errors="replace", cwd=cwd
    )
    if check and result.returncode != 0:
        if capture:
            print(result.stdout)
            print(result.stderr, file=sys.stderr)
        sys.exit(f"命令失败（{result.returncode}）：{cmd}")
    return result


def selfcheck(*args: str) -> subprocess.CompletedProcess:
    return run([SELFCHECK, *args], capture=True)


def start_upstream(docroot: Path) -> http.server.ThreadingHTTPServer:
    handler = partial(http.server.SimpleHTTPRequestHandler, directory=str(docroot))
    server = http.server.ThreadingHTTPServer(("127.0.0.1", UPSTREAM_PORT), handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def main() -> int:
    if not (BIN / "tunnelboard.exe").exists() or not (BIN / "tunnelboard-helper.exe").exists():
        sys.exit("缺少打包产物，请先运行 uv run scripts/package-windows.py")

    run([GO, "build", "-o", str(SELFCHECK), "./cmd/selfcheck"], cwd=ROOT)
    docroot = Path(tempfile.mkdtemp(prefix="tb-smoke-"))
    (docroot / "index.html").write_text(SMOKE_TEXT, encoding="utf-8")
    datadir = Path(tempfile.mkdtemp(prefix="tb-smoke-caddy-"))
    os.environ["TUNNELBOARD_CADDY_PATH"] = str(ROOT / "build" / "caddy" / "caddy.exe")

    steps = []
    server = None
    try:
        steps.append(("会话 Helper 启动、复用与 hosts 回读", lambda: selfcheck("helper-session-apply", "-domain", DOMAIN)))

        server = start_upstream(docroot)
        steps.append(("Caddy 启动", lambda: selfcheck("caddy-start", "-datadir", str(datadir), "-domain", DOMAIN, "-port", str(UPSTREAM_PORT))))

        steps.append(("当前用户 CA 信任", lambda: selfcheck("trust-ca", "-datadir", str(datadir))))

        def https_check():
            # --ssl-no-revoke：本地 CA 无 CRL/OCSP，curl 的 schannel 默认强制吊销检查会失败；
            # 浏览器对非 EV 证书不做硬性吊销检查，不受影响。
            out = run(["curl.exe", "-fsS", "--ssl-no-revoke", "--max-time", "15", f"https://{DOMAIN}/"], capture=True)
            if SMOKE_TEXT not in out.stdout:
                sys.exit(f"HTTPS 内容不符：{out.stdout!r}")
            return out
        steps.append(("HTTPS 访问验证", https_check))

        for name, fn in steps:
            print(f"== {name}")
            result = fn()
            if result.stdout:
                print(result.stdout.strip())
        print("\nSMOKE PASS：全部冒烟阶段通过")
        return 0
    finally:
        print("== 清理")
        for args in [("untrust-ca", "-datadir", str(datadir)),
                     ("caddy-stop", "-datadir", str(datadir)),
                     ("hosts-remove", "-domain", DOMAIN)]:
            subprocess.run([str(SELFCHECK), *args], capture_output=True)
        shutil.rmtree(docroot, ignore_errors=True)
        shutil.rmtree(datadir, ignore_errors=True)
        if server is not None:
            server.shutdown()


if __name__ == "__main__":
    sys.exit(main())
