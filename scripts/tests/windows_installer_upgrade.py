#!/usr/bin/env python3
"""仅在 GitHub Windows runner 验证同版本、不同字节的安装覆盖。"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import release


def run(command: list[str], *, timeout: int = 300) -> None:
    result = subprocess.run(command, cwd=release.ROOT, timeout=timeout)
    if result.returncode != 0:
        raise release.ReleaseError(f"command failed ({result.returncode}): {command[0]}")


def related_products() -> set[str]:
    # WindowsInstaller COM 查询不会触发 Win32_Product 的自动修复。
    script = """
$ErrorActionPreference = 'Stop'
$installer = New-Object -ComObject WindowsInstaller.Installer
foreach ($code in $installer.RelatedProducts('{D5CB6F64-09DB-4D47-9B52-F91B547C81AB}')) {
    Write-Output $code
}
"""
    result = subprocess.run(
        [release.windows_powershell(), "-NoProfile", "-NonInteractive", "-Command", script],
        capture_output=True, text=True, check=True, timeout=30,
    )
    return {line.strip() for line in result.stdout.splitlines() if line.strip()}


def make_fixture(payload: Path, destination: Path) -> None:
    shutil.copytree(payload, destination)
    helper = destination / "tunnelboard-helper.exe"
    app = destination / "TunnelBoard.exe"
    old_pin = release.sha256_file(helper)
    helper.write_bytes(helper.read_bytes() + b"\nTunnelBoard CI previous helper fixture\n")
    new_pin = release.sha256_file(helper)
    print(f"Fixture helper pin: current={old_pin}; previous={new_pin}", flush=True)
    app_bytes = app.read_bytes()
    if old_pin.encode("ascii") not in app_bytes:
        raise release.ReleaseError("fixture source application has no matching helper pin")
    app.write_bytes(app_bytes.replace(old_pin.encode("ascii"), new_pin.encode("ascii"))
                    + b"\nTunnelBoard CI previous application fixture\n")
    manifest_path = destination / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    for record in manifest["files"]:
        path = destination.joinpath(*release.safe_relative(record["path"]).parts)
        record["size"] = path.stat().st_size
        record["sha256"] = release.sha256_file(path)
    manifest["helper"]["bundle_sha256"] = new_pin
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    release.verify_embedded_helper_digest(app, new_pin)
    for name in ("TunnelBoard.exe", "tunnelboard-helper.exe"):
        if release.sha256_file(destination / name) == release.sha256_file(payload / name):
            raise release.ReleaseError(f"fixture must differ from current payload: {name}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--installer", required=True, type=Path)
    parser.add_argument("--payload", required=True, type=Path)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    if os.name != "nt" or os.environ.get("CI") != "true" or os.environ.get("GITHUB_ACTIONS") != "true":
        raise release.ReleaseError("installer upgrade test is restricted to GitHub Actions Windows runners")
    program_files = Path(os.environ["ProgramFiles"]).resolve()
    if (program_files / "TunnelBoard").exists() or related_products():
        raise release.ReleaseError("refusing to test on a runner with an existing TunnelBoard installation")
    installer, payload = args.installer.resolve(strict=True), args.payload.resolve(strict=True)
    release.verify_bundle_root(payload)
    manifest = json.loads((payload / "manifest.json").read_text(encoding="utf-8"))
    if manifest["version"] != args.version:
        raise release.ReleaseError("requested version differs from payload")
    wix = release.find_wix()
    if not wix:
        raise release.ReleaseError("WiX is required")
    install_dir = (program_files / f"TunnelBoard-CI-Upgrade-{uuid.uuid4().hex}").resolve()
    if install_dir.parent != program_files or install_dir.exists():
        raise release.ReleaseError("unsafe or occupied test installation directory")
    logs = release.RELEASE_ROOT / "windows-amd64" / "installer-logs"
    logs.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="tunnelboard-upgrade-fixture-") as temporary:
        scratch = Path(temporary)
        fixture = scratch / "payload"
        make_fixture(payload, fixture)
        expected_version = release.windows_application_file_version(args.version)
        release.verify_windows_application_file_version(fixture / "TunnelBoard.exe", expected_version)
        release.verify_windows_application_file_version(payload / "TunnelBoard.exe", expected_version)
        product = (release.ROOT / "scripts/windows-installer/Product.wxs").read_text(encoding="utf-8")
        product = re.sub(r'\s+AllowSameVersionUpgrades="[^"]*"', "", product)
        product = re.sub(r'\s+Schedule="[^"]*"', "", product)
        product_path = scratch / "Product.wxs"
        product_path.write_text(product, encoding="utf-8")
        baseline = scratch / "baseline.msi"
        run([wix, "build", "-arch", "x64", "-d", f"SourceDir={fixture}", "-d",
             f"MsiVersion={release.windows_installer_version(args.version)}", "-ext",
             "WixToolset.Util.wixext", "-o", str(baseline), str(product_path)])
        baseline_setup = scratch / "baseline-setup.exe"
        installer_root = release.ROOT / "scripts/windows-installer"
        run([wix, "build", "-arch", "x64", "-ext", "WixToolset.Bal.wixext",
             "-ext", "WixToolset.Util.wixext", "-loc", str(installer_root / "1033/thm.wxl"),
             "-d", f"MsiPath={baseline}", "-d",
             f"BundleVersion={release.windows_installer_version(args.version)}", "-o",
             str(baseline_setup), str(installer_root / "Bundle.wxs")])
        try:
            run([str(baseline_setup), "/quiet", "/norestart", "/log",
                 str(logs / "baseline.log"), f"InstallFolder={install_dir}",
                 "CreateStartMenuShortcut=0", "CreateDesktopShortcut=0", "LaunchAfterInstall=0"])
            previous_products = related_products()
            if len(previous_products) != 1:
                raise release.ReleaseError(f"expected exactly one baseline product: {previous_products}")
            release.verify_bundle_root(install_dir)
            run([str(installer), "/quiet", "/norestart", "/log", str(logs / "upgrade.log"),
                 f"InstallFolder={install_dir}", "CreateStartMenuShortcut=0",
                 "CreateDesktopShortcut=0", "LaunchAfterInstall=0"])
            current_products = related_products()
            if len(current_products) != 1 or previous_products & current_products:
                raise release.ReleaseError(f"old MSI registration remains after upgrade: {current_products}")
            if (install_dir / "manifest.json").read_bytes() != (payload / "manifest.json").read_bytes():
                raise release.ReleaseError("installed manifest is not the current payload manifest")
            release.verify_bundle_root(install_dir)
            release.verify_embedded_helper_digest(install_dir / "TunnelBoard.exe",
                                                   release.sha256_file(install_dir / "tunnelboard-helper.exe"))
            print("PASS: equal-version changed application/helper replaced together; old MSI removed")
        finally:
            # 仅卸载本脚本创建的隔离产品；不递归删除 Program Files。
            cleanup = subprocess.run([str(installer), "/uninstall", "/quiet", "/norestart",
                                      "/log", str(logs / "uninstall.log")], timeout=300)
            if cleanup.returncode or related_products():
                subprocess.run([str(baseline_setup), "/uninstall", "/quiet", "/norestart",
                                "/log", str(logs / "baseline-uninstall.log")], timeout=300, check=False)
            if related_products():
                subprocess.run(["msiexec.exe", "/x", str(baseline), "/qn", "/norestart",
                                "/l*v", str(logs / "baseline-cleanup.log")], timeout=300, check=False)
            if related_products() or install_dir.exists():
                raise release.ReleaseError("installer cleanup left registered products or installation files; see installer-logs")


if __name__ == "__main__":
    main()
