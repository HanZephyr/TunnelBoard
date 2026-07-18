#!/usr/bin/env python3
"""冒烟即验收：对打包产物在真实 Windows 权限模型下跑最小闭环。

流程：产物预检 → 提权安装/重启 helper 服务（一次 UAC）→ 管道 ping →
受托管 hosts 写入与回读 → 本地 HTTP 上游 + Caddy 启动 → 本地 CA 信任 →
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
import time
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


def current_user_sid() -> str:
    out = subprocess.run(["whoami", "/user"], capture_output=True, text=True).stdout
    for line in out.splitlines():
        if "S-1-5-" in line:
            return line.split()[-1].strip()
    sys.exit("无法解析当前用户 SID")


def service_running() -> bool:
    out = subprocess.run(["sc.exe", "query", "TunnelBoardHelper"], capture_output=True, text=True).stdout
    return "RUNNING" in out


def ensure_service(sid: str) -> None:
    if service_running():
        print("helper 服务已在运行")
        return
    print("安装/重启 helper 服务（将弹一次 UAC）…")
    helper_exe = str(BIN / "tunnelboard-helper.exe")
    ps = f"Start-Process -Verb RunAs -Wait -FilePath '{helper_exe}' -ArgumentList '-install','-owner','{sid}'"
    run(["powershell", "-NoProfile", "-Command", ps])
    for _ in range(60):
        if service_running():
            print("helper 服务已运行")
            return
        time.sleep(1)
    sys.exit("helper 服务 60 秒内未上线")


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
    sid = current_user_sid()
    ensure_service(sid)

    docroot = Path(tempfile.mkdtemp(prefix="tb-smoke-"))
    (docroot / "index.html").write_text(SMOKE_TEXT, encoding="utf-8")
    datadir = Path(tempfile.mkdtemp(prefix="tb-smoke-caddy-"))
    os.environ["TUNNELBOARD_CADDY_PATH"] = str(ROOT / "build" / "caddy" / "caddy.exe")

    steps = []
    server = None
    try:
        steps.append(("管道 ping", lambda: selfcheck("ping")))
        steps.append(("hosts 写入与回读", lambda: selfcheck("hosts-apply", "-domain", DOMAIN)))

        server = start_upstream(docroot)
        steps.append(("Caddy 启动", lambda: selfcheck("caddy-start", "-datadir", str(datadir), "-domain", DOMAIN, "-port", str(UPSTREAM_PORT))))

        cafile = datadir / "caddy" / "pki" / "authorities" / "local" / "root.crt"
        steps.append(("CA 信任", lambda: selfcheck("trust-ca", "-cafile", str(cafile))))

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
        for args in [("untrust-ca", "-cafile", str(datadir / "caddy" / "pki" / "authorities" / "local" / "root.crt")),
                     ("caddy-stop", "-datadir", str(datadir)),
                     ("hosts-remove", "-domain", DOMAIN)]:
            subprocess.run([str(SELFCHECK), *args], capture_output=True)
        shutil.rmtree(docroot, ignore_errors=True)
        shutil.rmtree(datadir, ignore_errors=True)
        if server is not None:
            server.shutdown()


if __name__ == "__main__":
    sys.exit(main())
