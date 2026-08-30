#!/usr/bin/env python3
"""TunnelBoard 唯一发行打包 Module。

GitHub Actions 与本地复现必须调用本文件，workflow 不得复制下载、组装或
artifact 校验规则。正式 Release 仍只能由 GitHub Actions 生成。
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import io
import json
import os
import platform
import re
import shutil
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
import zipfile
from datetime import datetime
from pathlib import Path, PurePosixPath
from typing import Any


ROOT = Path(__file__).resolve().parent.parent
LOCK_PATH = ROOT / "scripts" / "caddy-lock.json"
RELEASE_ROOT = ROOT / "build" / "release"
LINUX_PACKAGING_ROOT = ROOT / "scripts" / "linux-packaging"
MAX_ARTIFACT_BYTES = 1_000_000_000
RELEASE_TARGETS = {
    "windows-amd64": {"os": "windows", "arch": "amd64", "minimum_system": "Windows 10 1809"},
    "darwin-universal": {"os": "darwin", "arch": "universal", "minimum_system": "macOS 10.15"},
    "linux-debian-amd64": {
        "os": "linux", "arch": "amd64", "package_format": "deb", "distribution": "debian",
        "minimum_system": "Debian 12 / Ubuntu 24.04 LTS",
    },
    "linux-debian-arm64": {
        "os": "linux", "arch": "arm64", "package_format": "deb", "distribution": "debian",
        "minimum_system": "Debian 12 / Ubuntu 24.04 LTS",
    },
    "linux-rhel-amd64": {
        "os": "linux", "arch": "amd64", "package_format": "rpm", "distribution": "rhel",
        "minimum_system": "RHEL-compatible 9+",
    },
    "linux-rhel-arm64": {
        "os": "linux", "arch": "arm64", "package_format": "rpm", "distribution": "rhel",
        "minimum_system": "RHEL-compatible 9+",
    },
}


def configure_stdout() -> None:
    """确保转发外部构建工具的 UTF-8 日志不会被 Windows 代码页中断。"""
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8", errors="backslashreplace")


configure_stdout()


def windows_powershell() -> str:
    candidate = Path(os.environ.get("SystemRoot", r"C:\\Windows")) / "System32" / "WindowsPowerShell" / "v1.0" / "powershell.exe"
    return str(candidate) if candidate.is_file() else "powershell"


class ReleaseError(RuntimeError):
    pass


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_file(path: Path) -> str:
    hasher = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            hasher.update(chunk)
    return hasher.hexdigest()


def load_lock(path: Path = LOCK_PATH) -> dict[str, Any]:
    lock = json.loads(path.read_text(encoding="utf-8"))
    if lock.get("schema_version") != 1 or not lock.get("version"):
        raise ReleaseError("unsupported Caddy lock schema")
    return lock


def safe_relative(name: str) -> PurePosixPath:
    if "\\" in name or "\x00" in name:
        raise ReleaseError(f"unsafe path uses backslash: {name}")
    path = PurePosixPath(name)
    if (
        not name
        or path.as_posix() == "."
        or path.is_absolute()
        or ".." in path.parts
        or "." in path.parts
        or (path.parts and ":" in path.parts[0])
    ):
        raise ReleaseError(f"unsafe relative path: {name}")
    return path


def download_limited(url: str, max_bytes: int) -> bytes:
    request = urllib.request.Request(url, headers={"User-Agent": "TunnelBoard-release-builder/1"})
    with urllib.request.urlopen(request, timeout=120) as response:
        declared = response.headers.get("Content-Length")
        if declared and int(declared) > max_bytes:
            raise ReleaseError(f"Caddy archive exceeds download budget: {declared} > {max_bytes}")
        payload = response.read(max_bytes + 1)
    if len(payload) > max_bytes:
        raise ReleaseError(f"Caddy archive exceeds download budget: > {max_bytes}")
    return payload


def extract_exact_member(payload: bytes, entry: dict[str, Any]) -> bytes:
    wanted = safe_relative(entry["archive_member"])
    if entry["archive_format"] == "zip":
        with zipfile.ZipFile(io.BytesIO(payload)) as archive:
            for info in archive.infolist():
                path = safe_relative(info.filename.rstrip("/")) if info.filename.rstrip("/") else None
                if path and (info.external_attr >> 16) & 0o170000 == 0o120000:
                    raise ReleaseError(f"Caddy archive contains symlink: {info.filename}")
            try:
                return archive.read(wanted.as_posix())
            except KeyError as exc:
                raise ReleaseError(f"Caddy archive missing exact member {wanted}") from exc
    if entry["archive_format"] == "tar.gz":
        with tarfile.open(fileobj=io.BytesIO(payload), mode="r:gz") as archive:
            for member in archive.getmembers():
                safe_relative(member.name.rstrip("/"))
                if member.issym() or member.islnk():
                    raise ReleaseError(f"Caddy archive contains link: {member.name}")
            try:
                member = archive.getmember(wanted.as_posix())
            except KeyError as exc:
                raise ReleaseError(f"Caddy archive missing exact member {wanted}") from exc
            stream = archive.extractfile(member)
            if stream is None:
                raise ReleaseError(f"Caddy member is not a file: {wanted}")
            return stream.read()
    raise ReleaseError(f"unsupported Caddy archive format: {entry['archive_format']}")


def fetch_caddy(target: str, output: Path) -> dict[str, Any]:
    lock = load_lock()
    try:
        entry = lock["targets"][target]
    except KeyError as exc:
        raise ReleaseError(f"unsupported Caddy target: {target}") from exc
    payload = download_limited(entry["url"], int(entry["max_download_bytes"]))
    if len(payload) != int(entry["archive_size"]):
        raise ReleaseError(f"Caddy archive size mismatch: got {len(payload)}, want {entry['archive_size']}")
    archive_digest = sha256_bytes(payload)
    if archive_digest != entry["archive_sha256"]:
        raise ReleaseError(f"Caddy archive sha256 mismatch: got {archive_digest}")
    binary = extract_exact_member(payload, entry)
    binary_digest = sha256_bytes(binary)
    if binary_digest != entry["binary_sha256"]:
        raise ReleaseError(f"Caddy binary sha256 mismatch: got {binary_digest}")
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_bytes(binary)
    if entry["os"] != "windows":
        output.chmod(0o755)
    return {"version": lock["version"], "target": target, **entry}


def extract_artifact(artifact: Path, destination: Path) -> Path:
    if artifact.is_dir():
        return artifact
    if artifact.stat().st_size > MAX_ARTIFACT_BYTES:
        raise ReleaseError("artifact exceeds verification budget")
    if artifact.suffix.lower() != ".zip":
        raise ReleaseError("verify accepts an unpacked directory or verification .zip bundle")
    total = 0
    with zipfile.ZipFile(artifact) as archive:
        for info in archive.infolist():
            name = info.filename.rstrip("/")
            if not name:
                continue
            rel = safe_relative(name)
            total += info.file_size
            if total > MAX_ARTIFACT_BYTES:
                raise ReleaseError("expanded artifact exceeds verification budget")
            mode = (info.external_attr >> 16) & 0o170000
            if mode == 0o120000:
                raise ReleaseError(f"artifact contains symlink: {name}")
            target = destination.joinpath(*rel.parts)
            if info.is_dir():
                target.mkdir(parents=True, exist_ok=True)
                continue
            target.parent.mkdir(parents=True, exist_ok=True)
            with archive.open(info) as source, target.open("wb") as output:
                shutil.copyfileobj(source, output)
    top_level = list(destination.iterdir())
    if len(top_level) != 1 or not top_level[0].is_dir():
        raise ReleaseError("artifact must contain a single top-level bundle directory")
    return top_level[0]


def locate_manifest(root: Path) -> Path:
    for manifest in (root / "manifest.json", root / "opt" / "tunnelboard" / "manifest.json"):
        if manifest.is_file():
            return manifest
    raise ReleaseError("artifact bundle root must contain manifest.json")


def validate_manifest_shape(manifest: dict[str, Any], lock: dict[str, Any]) -> None:
    if manifest.get("schema_version") != 1 or manifest.get("product") != "TunnelBoard":
        raise ReleaseError("unsupported artifact manifest")
    target = manifest.get("target", {})
    if target.get("name") not in RELEASE_TARGETS:
        raise ReleaseError(f"unsupported manifest target: {target.get('name')}")
    expected_target = RELEASE_TARGETS[target["name"]]
    for field in ("os", "arch", "minimum_system"):
        if target.get(field) != expected_target[field]:
            raise ReleaseError(f"manifest target {field} mismatch for {target['name']}")
    files = manifest.get("files")
    if not isinstance(files, list) or not files:
        raise ReleaseError("manifest files must be a non-empty list")


def verify_bundle_root(
    root: Path,
    execute_caddy: bool = False,
    supply_chain_lock: Path = LOCK_PATH,
    allowed_runtime_files: set[str] | None = None,
) -> dict[str, Any]:
    manifest_path = locate_manifest(root)
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    lock = load_lock(supply_chain_lock)
    validate_manifest_shape(manifest, lock)
    bundle_root = root
    declared: set[str] = set()
    roles: set[str] = set()
    caddy_path: Path | None = None
    helper_path: Path | None = None
    app_path: Path | None = None
    for record in manifest["files"]:
        rel = safe_relative(str(record.get("path", ""))).as_posix()
        if rel in declared:
            raise ReleaseError(f"duplicate manifest path: {rel}")
        declared.add(rel)
        roles.add(str(record.get("role", "")))
        path = bundle_root.joinpath(*PurePosixPath(rel).parts)
        if not path.is_file():
            raise ReleaseError(f"missing declared file: {rel}")
        size = path.stat().st_size
        if size != int(record.get("size", -1)):
            raise ReleaseError(f"size mismatch for {rel}: got {size}")
        digest = sha256_file(path)
        if digest != record.get("sha256"):
            raise ReleaseError(f"sha256 mismatch for {rel}: got {digest}")
        if record.get("role") == "caddy":
            if caddy_path is not None:
                raise ReleaseError("manifest declares multiple Caddy binaries")
            caddy_path = path
        if record.get("role") == "privileged_helper":
            if helper_path is not None:
                raise ReleaseError("manifest declares multiple privileged helpers")
            helper_path = path
        if record.get("role") == "application":
            app_path = path

    actual = {
        p.relative_to(bundle_root).as_posix()
        for p in bundle_root.rglob("*")
        if p.is_file() and p != manifest_path
    }
    undeclared = sorted(actual - declared - (allowed_runtime_files or set()))
    if undeclared:
        raise ReleaseError(f"undeclared artifact file: {undeclared[0]}")
    missing_records = sorted(declared - actual)
    if missing_records:
        raise ReleaseError(f"missing declared file: {missing_records[0]}")

    os_name = manifest["target"]["os"]
    required = {"application", "caddy", "license"}
    if os_name in {"windows", "linux"}:
        required.add("privileged_helper")
    if os_name == "linux":
        required.update({"polkit_policy", "desktop_entry"})
        required_paths = {
            "opt/tunnelboard/tunnelboard": "application",
            "opt/tunnelboard/caddy/caddy": "caddy",
            "usr/libexec/tunnelboard/tunnelboard-linux-helper": "privileged_helper",
            "usr/share/polkit-1/actions/io.github.hanzephyr.TunnelBoard.policy": "polkit_policy",
            "usr/share/applications/io.github.hanzephyr.TunnelBoard.desktop": "desktop_entry",
        }
        for path, role in required_paths.items():
            matching = [record for record in manifest["files"] if record.get("path") == path and record.get("role") == role]
            if len(matching) != 1:
                raise ReleaseError(f"Linux artifact must declare exactly one {role} at {path}")
    absent_roles = sorted(required - roles)
    if absent_roles:
        raise ReleaseError(f"manifest missing required roles: {', '.join(absent_roles)}")
    if caddy_path is None:
        raise ReleaseError("manifest missing Caddy")
    if os_name == "windows":
        for executable in (app_path, helper_path, caddy_path):
            if executable is None:
                raise ReleaseError("Windows artifact is missing a required executable")
            verify_windows_pe_machine(executable, 0x8664)
    if os_name == "linux":
        expected_machine = {"amd64": 0x3E, "arm64": 0xB7}.get(manifest["target"]["arch"])
        if expected_machine is None:
            raise ReleaseError("unsupported Linux artifact architecture")
        for executable in (app_path, helper_path, caddy_path):
            if executable is None:
                raise ReleaseError("Linux artifact is missing a required executable")
            verify_linux_elf_machine(executable, expected_machine)
    caddy_record = manifest.get("caddy", {})
    result_digest = sha256_file(caddy_path)
    if result_digest != caddy_record.get("result_binary_sha256"):
        raise ReleaseError("manifest Caddy result sha256 does not match the artifact")
    inputs = caddy_record.get("inputs")
    if not isinstance(inputs, list) or not inputs:
        raise ReleaseError("manifest Caddy inputs are missing")
    for source in inputs:
        target_name = source.get("target")
        if target_name not in lock["targets"]:
            raise ReleaseError(f"manifest Caddy input target is not pinned: {target_name}")
        if source.get("binary_sha256") != lock["targets"][target_name]["binary_sha256"]:
            raise ReleaseError(f"manifest Caddy input sha256 disagrees with supply-chain lock: {target_name}")
    if len(inputs) == 1 and result_digest != inputs[0]["binary_sha256"]:
        raise ReleaseError("single-architecture Caddy does not match the pinned binary sha256")
    if execute_caddy:
        validate_caddy_runtime(caddy_path)
        if os_name == "windows":
            if helper_path is None:
                raise ReleaseError("Windows artifact is missing its privileged helper")
            observed_helper = validate_windows_helper(helper_path)
            if observed_helper != manifest.get("helper"):
                raise ReleaseError("Helper self-check metadata disagrees with artifact manifest")
            signing = manifest.get("signing", {})
            if signing.get("required") is True:
                observed_app_sign = signing_status(app_path, True)
                observed_helper_sign = signing_status(helper_path, True)
                expected_app_sign = signing.get("application", {})
                expected_helper_sign = signing.get("helper", {})
                if observed_app_sign.get("publisher") != expected_app_sign.get("publisher"):
                    raise ReleaseError("application Authenticode publisher disagrees with manifest")
                if observed_helper_sign.get("publisher") != expected_helper_sign.get("publisher"):
                    raise ReleaseError("Helper Authenticode publisher disagrees with manifest")
                if observed_app_sign.get("publisher") != observed_helper_sign.get("publisher"):
                    raise ReleaseError("application and Helper Authenticode publishers differ")
    return manifest


def verify_windows_pe_machine(path: Path, expected: int) -> None:
    with path.open("rb") as stream:
        header = stream.read(64)
        if len(header) < 64 or header[:2] != b"MZ":
            raise ReleaseError(f"Windows executable is not PE: {path.name}")
        pe_offset = int.from_bytes(header[0x3C:0x40], "little")
        if pe_offset < 64 or pe_offset > 16 * 1024 * 1024:
            raise ReleaseError(f"Windows executable has invalid PE offset: {path.name}")
        stream.seek(pe_offset)
        signature = stream.read(6)
    if len(signature) != 6 or signature[:4] != b"PE\0\0":
        raise ReleaseError(f"Windows executable has invalid PE signature: {path.name}")
    machine = int.from_bytes(signature[4:6], "little")
    if machine != expected:
        raise ReleaseError(f"Windows executable architecture mismatch for {path.name}: 0x{machine:04x}")


def verify_linux_elf_machine(path: Path, expected: int) -> None:
    with path.open("rb") as stream:
        header = stream.read(20)
    if len(header) < 20 or header[:4] != b"\x7fELF" or header[4] != 2:
        raise ReleaseError(f"invalid 64-bit ELF executable: {path.name}")
    endian = {1: "little", 2: "big"}.get(header[5])
    if endian is None:
        raise ReleaseError(f"invalid ELF byte order: {path.name}")
    machine = int.from_bytes(header[18:20], endian)
    if machine != expected:
        raise ReleaseError(f"Linux executable architecture mismatch for {path.name}: 0x{machine:04x}")


def service_exists() -> bool:
    if os.name != "nt":
        return False
    result = subprocess.run(
        ["sc.exe", "query", "TunnelBoardHelper"], capture_output=True, text=True, encoding="utf-8", errors="replace"
    )
    return result.returncode == 0


def validate_windows_helper(helper_path: Path) -> dict[str, Any]:
    service_before = service_exists()
    metadata = helper_metadata(helper_path)
    if service_exists() != service_before:
        raise ReleaseError("Helper self-check mutated TunnelBoardHelper SCM service state")
    return metadata


def helper_metadata(helper_path: Path) -> dict[str, Any]:
    result = subprocess.run(
        [str(helper_path), "--self-check", "--json"],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=15,
    )
    if result.returncode != 0:
        raise ReleaseError(f"Helper --self-check failed: {result.stdout} {result.stderr}")
    try:
        metadata = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise ReleaseError("Helper --self-check --json did not return JSON") from exc
    protocol = str(metadata.get("protocol_version", "")).strip()
    if not protocol:
        raise ReleaseError("Helper self-check omitted protocol_version")
    if metadata.get("persistent_service") is not False:
        raise ReleaseError("Helper self-check does not guarantee persistent_service=false")
    return {
        "protocol_version": protocol,
        "persistent_service": False,
        "bundle_sha256": sha256_file(helper_path),
    }


def verify_windows_acl(install_dir: Path) -> None:
    result = subprocess.run(
        ["icacls", str(install_dir)],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    if result.returncode != 0:
        raise ReleaseError(f"cannot inspect installed directory ACL: {result.stdout} {result.stderr}")
    standard_users = ("everyone", "authenticated users", "builtin\\users", "s-1-1-0", "s-1-5-11", "s-1-5-32-545")
    dangerous_rights = ("(f)", "(m)", "(w)", "(wd)", "(wo)", "(dc)")
    unsafe = []
    for line in result.stdout.splitlines():
        normalized_line = line.lower()
        if any(identity in normalized_line for identity in standard_users) and any(
            right in normalized_line for right in dangerous_rights
        ):
            unsafe.append(line)
    if unsafe:
        raise ReleaseError(f"installed directory grants standard-user write rights: {' | '.join(unsafe)}")


def verify_windows_installer(installer: Path, require_signing: bool) -> None:
    if os.name != "nt":
        raise ReleaseError("Windows installer verification requires a Windows runner")
    signing_status(installer, require_signing)
    program_files = Path(os.environ.get("ProgramFiles", r"C:\Program Files"))
    install_dir = program_files / f"TunnelBoard-CI-{os.getpid()}"
    if install_dir.exists():
        shutil.rmtree(install_dir)
    result = subprocess.run([str(installer), "/quiet", "/norestart", f"InstallFolder={install_dir}"], timeout=120)
    if result.returncode != 0:
        raise ReleaseError(f"silent installer failed with exit code {result.returncode}")
    if not install_dir.is_dir():
        default_install_dir = program_files / "TunnelBoard"
        if default_install_dir.is_dir():
            install_dir = default_install_dir
        else:
            raise ReleaseError(f"installer did not create {install_dir} or {default_install_dir}")
    try:
        verify_windows_acl(install_dir)
        verify_bundle_root(
            install_dir,
            execute_caddy=True,
        )
        if service_exists():
            raise ReleaseError("installer created a forbidden persistent TunnelBoardHelper service")
    finally:
        subprocess.run([str(installer), "/uninstall", "/quiet", "/norestart"], timeout=120, check=False)
        deadline = time.monotonic() + 10
        while install_dir.exists() and time.monotonic() < deadline:
            time.sleep(0.1)
        if install_dir.exists():
            raise ReleaseError(f"uninstaller did not remove the isolated install directory: {install_dir}")
    if service_exists():
        raise ReleaseError("TunnelBoardHelper service exists after uninstall")


def tcp_open(port: int) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as client:
        client.settimeout(0.2)
        return client.connect_ex(("127.0.0.1", port)) == 0


def windows_tcp_listener_pids(port: int) -> set[int]:
    if os.name != "nt":
        return set()
    result = subprocess.run(
        ["netstat", "-ano", "-p", "TCP"], capture_output=True, text=True, encoding="utf-8", errors="replace"
    )
    pids: set[int] = set()
    for line in result.stdout.splitlines():
        fields = line.split()
        if len(fields) < 5 or fields[0].upper() != "TCP" or fields[3].upper() != "LISTENING":
            continue
        if fields[1].rsplit(":", 1)[-1] == str(port):
            try:
                pids.add(int(fields[4]))
            except ValueError:
                pass
    return pids


def validate_caddy_runtime(caddy_path: Path) -> None:
    if os.name != "nt":
        caddy_path.chmod(caddy_path.stat().st_mode | 0o111)
    version = subprocess.run(
        [str(caddy_path), "version"],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=10,
    )
    if version.returncode != 0 or load_lock()["version"] not in version.stdout:
        raise ReleaseError(f"Caddy version self-check failed: {version.stdout} {version.stderr}")
    baseline_2019_pids = windows_tcp_listener_pids(2019)
    with tempfile.TemporaryDirectory(prefix="tunnelboard-caddy-poc-") as raw:
        temp = Path(raw)
        socket_path = temp / "admin-a.sock"
        next_socket_path = temp / "admin-b.sock"
        # Caddy 地址是 network/address：Windows 的盘符路径没有前导 /，Unix 有。
        # 因此 Windows 必须是 unix/C:/...；unix//C:/... 会被解释为 /C:/...，
        # 即使静态 validate 通过，运行时也无法创建 socket。
        def endpoint(path: Path, permissions: bool = True) -> str:
            prefix = f"unix/{path.as_posix()}" if os.name == "nt" else f"unix//{path.as_posix().lstrip('/')}"
            return f"{prefix}|0600" if permissions else prefix

        config = {
            "admin": {"listen": endpoint(socket_path)},
            "apps": {
                "http": {
                    "servers": {
                        "poc": {
                            "listen": ["127.0.0.1:0"],
                            "routes": [],
                        }
                    }
                }
            },
        }
        config_path = temp / "caddy.json"
        config_path.write_text(json.dumps(config), encoding="utf-8")
        admin_client = temp / ("caddy-admin-client.exe" if os.name == "nt" else "caddy-admin-client")
        client_build = subprocess.run(
            ["go", "build", "-trimpath", "-o", str(admin_client), "./scripts/caddy-admin-client"],
            cwd=ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=60,
        )
        if client_build.returncode != 0:
            raise ReleaseError(f"cannot build AF_UNIX verification client: {client_build.stdout} {client_build.stderr}")
        validate = subprocess.run(
            [str(caddy_path), "validate", "--config", str(config_path)],
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=15,
        )
        if validate.returncode != 0:
            raise ReleaseError(f"Caddy validate failed: {validate.stdout} {validate.stderr}")
        env = os.environ.copy()
        env["XDG_DATA_HOME"] = str(temp / "data")
        env["XDG_CONFIG_HOME"] = str(temp / "config")
        log_path = temp / "caddy-poc.log"
        log_stream = log_path.open("wb")
        process = subprocess.Popen(
            [str(caddy_path), "run", "--config", str(config_path)],
            cwd=temp,
            env=env,
            stdout=log_stream,
            stderr=subprocess.STDOUT,
        )
        try:
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline and not socket_path.exists() and process.poll() is None:
                time.sleep(0.05)
            if not socket_path.exists():
                process.wait(timeout=2)
                log_stream.flush()
                raise ReleaseError(
                    f"Caddy did not create AF_UNIX admin socket: {log_path.read_text(encoding='utf-8', errors='replace')}"
                )
            if process.pid in windows_tcp_listener_pids(2019):
                raise ReleaseError("Caddy opened forbidden TCP admin port 2019")
            if os.name != "nt" and tcp_open(2019):
                raise ReleaseError("TCP 127.0.0.1:2019 is listening during AF_UNIX POC")
            if os.name == "nt" and windows_tcp_listener_pids(2019) - baseline_2019_pids:
                raise ReleaseError("a new TCP 2019 listener appeared during AF_UNIX POC")
            # Caddy /load 会替换 admin listener；复用同一路径可能让旧 listener
            # 的清理 unlink 掉新 socket，因此应用 generation 内必须 a/b 双槽换代。
            next_config = json.loads(json.dumps(config))
            next_config["admin"]["listen"] = endpoint(next_socket_path)
            next_config["apps"]["http"]["servers"]["poc"]["logs"] = {}
            next_config_path = temp / "caddy-next.json"
            next_config_path.write_text(json.dumps(next_config), encoding="utf-8")
            reload_result = subprocess.run(
                [
                    str(admin_client),
                    "--socket",
                    str(socket_path),
                    "--method",
                    "POST",
                    "--path",
                    "/load",
                    "--body-file",
                    str(next_config_path),
                ],
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=15,
            )
            if reload_result.returncode != 0:
                raise ReleaseError(f"Caddy AF_UNIX /load failed: {reload_result.stdout} {reload_result.stderr}")
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline and not next_socket_path.exists() and process.poll() is None:
                time.sleep(0.05)
            if not next_socket_path.exists():
                raise ReleaseError("Caddy /load did not rotate to the second AF_UNIX admin socket")
            stop_result = subprocess.run(
                [
                    str(admin_client),
                    "--socket",
                    str(next_socket_path),
                    "--method",
                    "POST",
                    "--path",
                    "/stop",
                ],
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=15,
            )
            if stop_result.returncode != 0:
                # /stop 可以先关闭 admin listener/进程再来得及写 HTTP 响应；
                # 此时客户端超时但受管进程已退出，仍是成功的可观察终态。
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired as exc:
                    raise ReleaseError(
                        f"Caddy AF_UNIX /stop failed: {stop_result.stdout} {stop_result.stderr}"
                    ) from exc
            process.wait(timeout=10)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            log_stream.close()


def command_version(command: list[str]) -> str:
    try:
        result = subprocess.run(
            platform_command(command),
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=15,
        )
    except (OSError, subprocess.TimeoutExpired):
        return "unavailable"
    text = (result.stdout or result.stderr).strip().splitlines()
    return text[0] if result.returncode == 0 and text else "unavailable"


def run(command: list[str], cwd: Path = ROOT, env: dict[str, str] | None = None) -> None:
    display = subprocess.list2cmdline(command)
    started = time.monotonic()
    print(f"[{datetime.now():%H:%M:%S}] START {display}", flush=True)
    process = subprocess.Popen(
        platform_command(command),
        cwd=cwd,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        encoding="utf-8",
        errors="replace",
        bufsize=1,
        env=env,
    )
    assert process.stdout is not None
    try:
        for line in process.stdout:
            print(f"[{datetime.now():%H:%M:%S}]   {line.rstrip()}", flush=True)
        returncode = process.wait()
    except KeyboardInterrupt:
        process.terminate()
        process.wait(timeout=5)
        raise
    finally:
        process.stdout.close()
    elapsed = time.monotonic() - started
    if returncode != 0:
        print(f"[{datetime.now():%H:%M:%S}] FAIL exit={returncode} elapsed={elapsed:.1f}s", flush=True)
        raise ReleaseError(f"command failed ({returncode}): {command}")
    print(f"[{datetime.now():%H:%M:%S}] DONE elapsed={elapsed:.1f}s", flush=True)


def platform_command(command: list[str]) -> list[str]:
    if not command:
        raise ReleaseError("empty command")
    resolved = shutil.which(command[0])
    if not resolved:
        return command
    prepared = [resolved, *command[1:]]
    if os.name == "nt" and Path(resolved).suffix.lower() in {".cmd", ".bat"}:
        return [os.environ.get("COMSPEC", "cmd.exe"), "/d", "/s", "/c", subprocess.list2cmdline(prepared)]
    return prepared


def git_commit() -> str:
    value = os.environ.get("GITHUB_SHA", "").strip()
    if value:
        return value
    result = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=ROOT,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    return result.stdout.strip() if result.returncode == 0 else "unknown"


def file_role(relative: str, target_os: str) -> str:
    lowered = relative.lower()
    if "licenses/" in lowered:
        return "license"
    if lowered.endswith("tunnelboard-helper.exe"):
        return "privileged_helper"
    if lowered.endswith("usr/libexec/tunnelboard/tunnelboard-linux-helper"):
        return "privileged_helper"
    if lowered.endswith("usr/share/polkit-1/actions/io.github.hanzephyr.tunnelboard.policy"):
        return "polkit_policy"
    if lowered.endswith("usr/share/applications/io.github.hanzephyr.tunnelboard.desktop"):
        return "desktop_entry"
    if lowered.endswith("caddy.exe") or lowered.endswith("/caddy"):
        return "caddy"
    if (target_os == "windows" and lowered.endswith("tunnelboard.exe")) or (
        target_os == "darwin" and "/macos/tunnelboard" in lowered
    ) or (
        target_os == "linux" and lowered.endswith("opt/tunnelboard/tunnelboard")
    ):
        return "application"
    return "runtime"


def frontend_inventory() -> list[dict[str, Any]]:
    dist = ROOT / "frontend" / "dist"
    records = []
    if not dist.is_dir():
        raise ReleaseError("frontend/dist is missing after frontend build")
    for path in sorted(p for p in dist.rglob("*") if p.is_file()):
        rel = path.relative_to(dist).as_posix()
        records.append({"path": rel, "size": path.stat().st_size, "sha256": sha256_file(path)})
    if not records:
        raise ReleaseError("frontend build produced no embedded assets")
    return records


def signing_status(path: Path, required: bool) -> dict[str, Any]:
    if os.name != "nt":
        return {"required": required, "status": "not-applicable"}
    escaped_path = str(path).replace("'", "''")
    script = (
        f"$s=Get-AuthenticodeSignature -LiteralPath '{escaped_path}'; "
        "$publisher=if($s.SignerCertificate){$s.SignerCertificate.Subject}else{''}; "
        "[pscustomobject]@{Status=[string]$s.Status;Publisher=$publisher}|ConvertTo-Json -Compress"
    )
    result = subprocess.run(
        [windows_powershell(), "-NoProfile", "-Command", script],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    status = "unknown"
    publisher = ""
    if result.returncode == 0 and result.stdout.strip():
        data = json.loads(result.stdout)
        status = str(data.get("Status", "unknown"))
        publisher = str(data.get("Publisher", ""))
    if required and status.lower() != "valid":
        raise ReleaseError(f"required Authenticode signature is not valid for {path.name}: {status}")
    return {"required": required, "status": status, "publisher": publisher}


def sign_windows(path: Path, required: bool) -> dict[str, Any]:
    cert = os.environ.get("TUNNELBOARD_SIGN_CERT_BASE64", "")
    password = os.environ.get("TUNNELBOARD_SIGN_CERT_PASSWORD", "")
    if not cert:
        if required:
            raise ReleaseError("Tag build requires TUNNELBOARD_SIGN_CERT_BASE64")
        return signing_status(path, False)
    signtool = find_signtool()
    if not signtool:
        raise ReleaseError("signtool is required when a signing certificate is provided")
    with tempfile.TemporaryDirectory(prefix="tunnelboard-sign-") as raw:
        pfx = Path(raw) / "release.pfx"
        try:
            pfx.write_bytes(base64.b64decode(cert, validate=True))
        except ValueError as exc:
            raise ReleaseError("invalid base64 signing certificate") from exc
        run([
            str(signtool), "sign", "/fd", "SHA256", "/td", "SHA256",
            "/tr", "http://timestamp.digicert.com", "/f", str(pfx), "/p", password, str(path),
        ])
    return signing_status(path, True)


def find_signtool() -> str | None:
    configured = os.environ.get("SIGNTOOL_PATH") or shutil.which("signtool")
    if configured:
        return str(configured)
    kits = Path(os.environ.get("ProgramFiles(x86)", r"C:\Program Files (x86)")) / "Windows Kits" / "10" / "bin"
    matches = sorted(kits.glob("*/x64/signtool.exe"), reverse=True)
    return str(matches[0]) if matches else None


def write_manifest(
    bundle_root: Path,
    manifest_path: Path,
    target_name: str,
    version: str,
    caddy_entry: dict[str, Any],
    embedded_assets: list[dict[str, Any]],
    signing: dict[str, Any],
    helper: dict[str, Any] | None = None,
) -> dict[str, Any]:
    target = RELEASE_TARGETS[target_name]
    files = []
    for path in sorted(p for p in bundle_root.rglob("*") if p.is_file() and p != manifest_path):
        rel = path.relative_to(bundle_root).as_posix()
        role = file_role(rel, target["os"])
        files.append(
            {
                "path": rel,
                "role": role,
                "size": path.stat().st_size,
                "sha256": sha256_file(path),
                "executable": role in {"application", "privileged_helper", "caddy"},
            }
        )
    manifest = {
        "schema_version": 1,
        "product": "TunnelBoard",
        "version": version,
        "git_commit": git_commit(),
        "build": {
            "workflow": os.environ.get("GITHUB_WORKFLOW", "local-reproduction"),
            "run_id": os.environ.get("GITHUB_RUN_ID", "local"),
        },
        "target": {"name": target_name, "os": target["os"], "arch": target["arch"], "minimum_system": target["minimum_system"]},
        "files": files,
        "embedded_assets": embedded_assets,
        "caddy": {
            "version": caddy_entry["version"],
            "result_binary_sha256": caddy_entry["binary_sha256"],
            "inputs": caddy_entry.get("inputs")
            or [
                {
                    "target": caddy_entry["target"],
                    "archive_sha256": caddy_entry["archive_sha256"],
                    "binary_sha256": caddy_entry["binary_sha256"],
                }
            ],
        },
        "tools": {
            "go": command_version(["go", "version"]),
            "wails": command_version(["wails", "version"]),
            "node": command_version(["node", "--version"]),
            "pnpm": command_version(["pnpm", "--version"]),
            "uv": command_version(["uv", "--version"]),
        },
        "signing": signing,
        "helper": helper or {"protocol_version": "not-applicable", "persistent_service": False},
        "support": {
            "real_machine_smoke": "pending",
            "linux": "not-applicable" if target["os"] != "linux" else "candidate-pending-desktop-acceptance",
            "macos_privileged_smoke": "pending" if target["os"] == "darwin" else "not-applicable",
        },
    }
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return manifest


def zip_directory(root: Path, output: Path) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for path in sorted(p for p in root.rglob("*") if p.is_file()):
            archive.write(path, Path(root.name) / path.relative_to(root))


def write_checksums(paths: list[Path], output: Path) -> None:
    lines = [f"{sha256_file(path)}  {path.name}" for path in sorted(paths, key=lambda item: item.name)]
    write_utf8_lf(output, "\n".join(lines) + "\n")


def write_utf8_lf(destination: Path, text: str) -> None:
    with destination.open("w", encoding="utf-8", newline="\n") as stream:
        stream.write(text)


def write_licenses(destination: Path, caddy_version: str) -> None:
    destination.mkdir()
    apache_license = (ROOT / "LICENSE").read_text(encoding="utf-8")
    (destination / "TunnelBoard.txt").write_text(apache_license, encoding="utf-8")
    (destination / "Caddy.txt").write_text(
        f"Caddy v{caddy_version}\nUpstream license: {load_lock()['license_url']}\n\n{apache_license}",
        encoding="utf-8",
    )


def prepare_release_root(target: str) -> tuple[Path, Path]:
    target_root = RELEASE_ROOT / target
    if target_root.exists():
        shutil.rmtree(target_root)
    stage = target_root / "stage" / "TunnelBoard"
    output = target_root / "out"
    stage.mkdir(parents=True)
    output.mkdir(parents=True)
    return stage, output


def prepare_linux_release_root(target: str) -> tuple[Path, Path, Path]:
    target_root = RELEASE_ROOT / target
    if target_root.exists():
        shutil.rmtree(target_root)
    payload = target_root / "payload"
    output = target_root / "out"
    scratch = target_root / "scratch"
    payload.mkdir(parents=True)
    output.mkdir()
    scratch.mkdir()
    return payload, output, scratch


def render_template(template: Path, destination: Path, replacements: dict[str, str]) -> None:
    text = template.read_text(encoding="utf-8")
    for key, value in replacements.items():
        text = text.replace(f"@{key}@", value)
    if re.search(r"@[A-Z_]+@", text):
        raise ReleaseError(f"unresolved placeholder in {template.name}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    write_utf8_lf(destination, text)


def linux_arches(arch: str) -> tuple[str, str]:
    mapping = {"amd64": ("amd64", "x86_64"), "arm64": ("arm64", "aarch64")}
    try:
        return mapping[arch]
    except KeyError as exc:
        raise ReleaseError(f"unsupported Linux architecture: {arch}") from exc


def linux_package_basename(target: str, version: str) -> str:
    target_info = RELEASE_TARGETS[target]
    return f"TunnelBoard-{version}-linux-{target_info['distribution']}-{target_info['arch']}"


def rpm_version(version: str) -> str:
    value = version.removeprefix("v").replace("-", ".").replace("_", ".")
    if not value or any(ch not in "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz.+" for ch in value):
        raise ReleaseError("version cannot be represented in an RPM")
    return value


def debian_version(version: str) -> str:
    value = version.removeprefix("v")
    allowed = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz.+:~-"
    if not value or not value[0].isdigit() or any(ch not in allowed for ch in value):
        raise ReleaseError("version cannot be represented in a Debian package")
    return value


def stage_linux_payload(
    payload: Path,
    app: Path,
    helper: Path,
    caddy: Path,
    caddy_version: str,
) -> None:
    app_dir = payload / "opt" / "tunnelboard"
    caddy_dir = app_dir / "caddy"
    helper_dir = payload / "usr" / "libexec" / "tunnelboard"
    policy_dir = payload / "usr" / "share" / "polkit-1" / "actions"
    desktop_dir = payload / "usr" / "share" / "applications"
    icon_dir = payload / "usr" / "share" / "icons" / "hicolor" / "256x256" / "apps"
    for directory in (app_dir, caddy_dir, helper_dir, policy_dir, desktop_dir, icon_dir):
        directory.mkdir(parents=True, exist_ok=True)
    shutil.copy2(app, app_dir / "tunnelboard")
    shutil.copy2(helper, helper_dir / "tunnelboard-linux-helper")
    shutil.copy2(caddy, caddy_dir / "caddy")
    for executable in (app_dir / "tunnelboard", helper_dir / "tunnelboard-linux-helper", caddy_dir / "caddy"):
        executable.chmod(0o755)
    shutil.copy2(
        LINUX_PACKAGING_ROOT / "io.github.hanzephyr.TunnelBoard.policy",
        policy_dir / "io.github.hanzephyr.TunnelBoard.policy",
    )
    shutil.copy2(
        LINUX_PACKAGING_ROOT / "io.github.hanzephyr.TunnelBoard.desktop",
        desktop_dir / "io.github.hanzephyr.TunnelBoard.desktop",
    )
    icon = ROOT / "build" / "appicon.png"
    if not icon.is_file():
        raise ReleaseError("build/appicon.png is missing")
    shutil.copy2(icon, icon_dir / "io.github.hanzephyr.TunnelBoard.png")
    write_licenses(app_dir / "LICENSES", caddy_version)


def build_debian_package(payload: Path, output: Path, version: str, arch: str, base: str) -> Path:
    dpkg_deb = shutil.which("dpkg-deb")
    if not dpkg_deb:
        raise ReleaseError("dpkg-deb is required to build Debian packages")
    package_root = payload.parent / "debian-root"
    shutil.copytree(payload, package_root)
    debian = package_root / "DEBIAN"
    debian.mkdir()
    render_template(
        LINUX_PACKAGING_ROOT / "debian" / "control.in",
        debian / "control",
        {"VERSION": debian_version(version), "ARCH": arch},
    )
    prerm = debian / "prerm"
    shutil.copy2(LINUX_PACKAGING_ROOT / "debian" / "prerm", prerm)
    prerm.chmod(0o755)
    package = output / f"{base}.deb"
    run([dpkg_deb, "--root-owner-group", "--build", str(package_root), str(package)])
    return package


def write_payload_tarball(payload: Path, output: Path) -> None:
    with tarfile.open(output, "w:gz", format=tarfile.PAX_FORMAT) as archive:
        for path in sorted(payload.rglob("*")):
            archive.add(path, arcname=path.relative_to(payload).as_posix(), recursive=False)


def build_rpm_package(payload: Path, output: Path, version: str, rpm_arch: str, base: str) -> Path:
    rpmbuild = shutil.which("rpmbuild")
    if not rpmbuild:
        raise ReleaseError("rpmbuild is required to build RHEL-compatible packages")
    topdir = payload.parent / "rpmbuild"
    for name in ("BUILD", "BUILDROOT", "RPMS", "SOURCES", "SPECS", "SRPMS"):
        (topdir / name).mkdir(parents=True)
    write_payload_tarball(payload, topdir / "SOURCES" / "tunnelboard-payload.tar.gz")
    spec = topdir / "SPECS" / "tunnelboard.spec"
    render_template(
        LINUX_PACKAGING_ROOT / "rpm" / "tunnelboard.spec.in",
        spec,
        {"VERSION": rpm_version(version), "RPM_ARCH": rpm_arch},
    )
    run([rpmbuild, "--define", f"_topdir {topdir}", "--target", rpm_arch, "-bb", str(spec)])
    candidates = sorted((topdir / "RPMS" / rpm_arch).glob("tunnelboard-*.rpm"))
    if len(candidates) != 1:
        raise ReleaseError(f"expected one RPM, found {len(candidates)}")
    package = output / f"{base}.rpm"
    shutil.copy2(candidates[0], package)
    return package


def gpg_environment(gpg_home: Path) -> dict[str, str]:
    environment = os.environ.copy()
    environment["GNUPGHOME"] = str(gpg_home)
    return environment


def prepare_linux_gpg_signer(required: bool, scratch: Path) -> tuple[Path, str] | None:
    encoded_key = os.environ.get("TUNNELBOARD_GPG_SIGNING_KEY_BASE64", "")
    expected_fingerprint = os.environ.get("TUNNELBOARD_GPG_KEY_FINGERPRINT", "").replace(" ", "").upper()
    if not required:
        return None
    if not encoded_key or not expected_fingerprint:
        raise ReleaseError(
            "Linux tag build requires TUNNELBOARD_GPG_SIGNING_KEY_BASE64 and TUNNELBOARD_GPG_KEY_FINGERPRINT"
        )
    gpg = shutil.which("gpg")
    if not gpg:
        raise ReleaseError("gpg is required for a signed Linux release")
    gpg_home = scratch / "gnupg"
    gpg_home.mkdir(mode=0o700)
    private_key = scratch / "release-signing-key.asc"
    try:
        private_key.write_bytes(base64.b64decode(encoded_key, validate=True))
    except ValueError as exc:
        raise ReleaseError("invalid base64 Linux GPG signing key") from exc
    run([gpg, "--batch", "--homedir", str(gpg_home), "--import", str(private_key)])
    result = subprocess.run(
        [gpg, "--batch", "--homedir", str(gpg_home), "--with-colons", "--list-secret-keys"],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    fingerprints = {line.split(":")[9].upper() for line in result.stdout.splitlines() if line.startswith("fpr:")}
    if result.returncode != 0 or expected_fingerprint not in fingerprints:
        raise ReleaseError("configured Linux GPG fingerprint is not available in the imported signing key")
    return gpg_home, expected_fingerprint


def sign_linux_checksums(checksums: Path, output: Path, signer: tuple[Path, str]) -> tuple[Path, Path]:
    gpg_home, fingerprint = signer
    gpg = shutil.which("gpg")
    assert gpg is not None
    signature = output / f"{checksums.name}.asc"
    public_key = output / f"{checksums.name}.public-key.asc"
    environment = gpg_environment(gpg_home)
    run(
        [gpg, "--batch", "--yes", "--armor", "--local-user", fingerprint, "--detach-sign", "--output", str(signature), str(checksums)],
        env=environment,
    )
    exported = subprocess.run(
        [gpg, "--batch", "--armor", "--export", fingerprint],
        capture_output=True,
        env=environment,
    )
    if exported.returncode != 0 or not exported.stdout:
        raise ReleaseError("failed to export TunnelBoard GPG public key")
    public_key.write_bytes(exported.stdout)
    return signature, public_key


def sign_rpm(package: Path, signer: tuple[Path, str]) -> None:
    rpmsign = shutil.which("rpmsign")
    if not rpmsign:
        raise ReleaseError("rpmsign is required for a signed RHEL-compatible release")
    gpg_home, fingerprint = signer
    run([rpmsign, "--addsign", "--define", f"_gpg_name {fingerprint}", str(package)], env=gpg_environment(gpg_home))


def prepare_linux_release_assets(source: Path, output: Path) -> list[Path]:
    """为 GitHub Release 生成名称唯一且可验签的 Linux checksum 资产。"""
    expected_targets = {
        f"{target['distribution']}-{target['arch']}"
        for target in RELEASE_TARGETS.values()
        if target["os"] == "linux"
    }
    if not source.is_dir():
        raise ReleaseError(f"Linux release asset source directory does not exist: {source}")
    actual_targets = {path.name for path in source.iterdir() if path.is_dir()}
    if actual_targets != expected_targets:
        raise ReleaseError("Linux release asset directories do not match the supported targets")
    if output.exists() and any(output.iterdir()):
        raise ReleaseError(f"Linux release asset output directory must be empty: {output}")
    output.mkdir(parents=True, exist_ok=True)

    outputs: list[Path] = []
    shared_public_key: bytes | None = None
    for target in sorted(expected_targets):
        directory = source / target
        checksums = directory / "SHA256SUMS"
        signature = directory / "SHA256SUMS.asc"
        public_key = directory / "SHA256SUMS.public-key.asc"
        for path in (checksums, signature, public_key):
            if not path.is_file() or path.is_symlink():
                raise ReleaseError(f"required Linux release asset is missing or unsafe: {path}")
        for path in (checksums, signature):
            destination = output / f"TunnelBoard-{target}.{path.name}"
            shutil.copy2(path, destination)
            outputs.append(destination)
        content = public_key.read_bytes()
        if shared_public_key is None:
            shared_public_key = content
        elif content != shared_public_key:
            raise ReleaseError("Linux release packages do not share one GPG public key")
    assert shared_public_key is not None
    public_key_destination = output / "TunnelBoard-linux-gpg-public-key.asc"
    public_key_destination.write_bytes(shared_public_key)
    outputs.append(public_key_destination)
    return outputs


def capture(command: list[str], cwd: Path = ROOT, env: dict[str, str] | None = None) -> str:
    result = subprocess.run(
        platform_command(command),
        cwd=cwd,
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    if result.returncode != 0:
        detail = (result.stderr or result.stdout).strip()
        raise ReleaseError(f"command failed ({result.returncode}): {command}: {detail}")
    return result.stdout


def verify_linux_package(
    package: Path,
    require_signing: bool = False,
    rpm_public_key: Path | None = None,
) -> dict[str, Any]:
    if platform.system() != "Linux":
        raise ReleaseError("Linux packages must be verified on a Linux runner")
    if not package.is_file():
        raise ReleaseError(f"Linux package does not exist: {package}")
    with tempfile.TemporaryDirectory(prefix="tunnelboard-linux-package-verify-") as raw:
        destination = Path(raw) / "payload"
        destination.mkdir()
        if package.suffix == ".deb":
            dpkg_deb = shutil.which("dpkg-deb")
            if not dpkg_deb:
                raise ReleaseError("dpkg-deb is required to verify Debian packages")
            package_name = capture([dpkg_deb, "--field", str(package), "Package"]).strip()
            architecture = capture([dpkg_deb, "--field", str(package), "Architecture"]).strip()
            if (package_name, architecture) not in {("tunnelboard", "amd64"), ("tunnelboard", "arm64")}:
                raise ReleaseError("Debian package metadata is not a supported TunnelBoard architecture")
            run([dpkg_deb, "--extract", str(package), str(destination)])
        elif package.suffix == ".rpm":
            rpm = shutil.which("rpm")
            rpm2cpio = shutil.which("rpm2cpio")
            cpio = shutil.which("cpio")
            if not rpm or not rpm2cpio or not cpio:
                raise ReleaseError("rpm, rpm2cpio and cpio are required to verify RHEL-compatible packages")
            fields = capture([rpm, "--queryformat", "%{NAME}\\n%{ARCH}\\n", "-qp", str(package)]).splitlines()
            if fields != ["tunnelboard", "x86_64"] and fields != ["tunnelboard", "aarch64"]:
                raise ReleaseError("RPM package metadata is not a supported TunnelBoard architecture")
            if require_signing:
                if rpm_public_key is None or not rpm_public_key.is_file():
                    raise ReleaseError("RPM native signature verification requires the released GPG public key")
                rpm_database = Path(raw) / "rpmdb"
                capture([rpm, "--dbpath", str(rpm_database), "--initdb"])
                capture([rpm, "--dbpath", str(rpm_database), "--import", str(rpm_public_key)])
                signature_status = capture([rpm, "--dbpath", str(rpm_database), "--checksig", str(package)])
                status = signature_status.lower()
                if "signature" not in status or "ok" not in status or "not ok" in status:
                    raise ReleaseError("RPM native GPG signature is missing or invalid")
            cpio_payload = subprocess.run([rpm2cpio, str(package)], capture_output=True)
            if cpio_payload.returncode != 0:
                raise ReleaseError("rpm2cpio failed while reading package")
            listing = subprocess.run([cpio, "-it"], input=cpio_payload.stdout, capture_output=True)
            if listing.returncode != 0:
                raise ReleaseError("cpio failed while listing package payload")
            for name in listing.stdout.decode("utf-8", errors="replace").splitlines():
                safe_relative(name.removeprefix("./").lstrip("/"))
            extracted = subprocess.run(
                [cpio, "--extract", "--make-directories", "--no-absolute-filenames"],
                cwd=destination,
                input=cpio_payload.stdout,
                capture_output=True,
            )
            if extracted.returncode != 0:
                raise ReleaseError("cpio failed while extracting package payload")
        else:
            raise ReleaseError("Linux package must be a .deb or .rpm")
        manifest = verify_bundle_root(destination)
        package_format = RELEASE_TARGETS[manifest["target"]["name"]].get("package_format")
        expected_suffix = ".deb" if package_format == "deb" else ".rpm"
        if package.suffix != expected_suffix:
            raise ReleaseError("package extension disagrees with artifact manifest target")
        return manifest


def verify_linux_release_checksums(checksums: Path, signature: Path, public_key: Path) -> None:
    if platform.system() != "Linux":
        raise ReleaseError("Linux release signatures must be verified on a Linux runner")
    for path in (checksums, signature, public_key):
        if not path.is_file():
            raise ReleaseError(f"required release signature asset is missing: {path}")
    gpg = shutil.which("gpg")
    if not gpg:
        raise ReleaseError("gpg is required to verify Linux release signatures")
    with tempfile.TemporaryDirectory(prefix="tunnelboard-linux-gpg-verify-") as raw:
        gpg_home = Path(raw) / "gnupg"
        gpg_home.mkdir(mode=0o700)
        environment = gpg_environment(gpg_home)
        run([gpg, "--batch", "--import", str(public_key)], env=environment)
        run([gpg, "--batch", "--verify", str(signature), str(checksums)], env=environment)
    for line in checksums.read_text(encoding="utf-8").splitlines():
        digest, separator, filename = line.partition("  ")
        if separator != "  " or len(digest) != 64 or not filename:
            raise ReleaseError("invalid Linux SHA256SUMS format")
        artifact = checksums.parent / filename
        if not artifact.is_file() or sha256_file(artifact) != digest:
            raise ReleaseError(f"Linux release checksum mismatch: {filename}")


def find_wix() -> str | None:
    configured = os.environ.get("TUNNELBOARD_WIX", "")
    if configured and Path(configured).is_file():
        return configured
    return shutil.which("wix") or shutil.which("wix.exe")


def windows_installer_version(version: str) -> str:
    """将发布版本映射为 Windows Installer 支持的三段数字版本。"""
    parts = version.removeprefix("v").split(".")
    if 3 <= len(parts) <= 4 and all(part.isdecimal() for part in parts):
        major, minor, patch = (int(part) for part in parts[:3])
        revision = int(parts[3]) if len(parts) == 4 else 0
        build = patch * 1000 + revision
        if major <= 255 and minor <= 255 and build <= 65535:
            return f"{major}.{minor}.{build}"
    # 分支候选只用于 CI 生命周期验证；避免把 prerelease 标签塞进 MSI 的数字版本字段。
    return "0.0.1"


def build_windows(version: str, require_signing: bool, skip_installer: bool) -> list[Path]:
    if os.name != "nt":
        raise ReleaseError("windows-amd64 must be built on a Windows runner")
    stage, output = prepare_release_root("windows-amd64")
    scratch = RELEASE_ROOT / "windows-amd64" / "scratch"
    scratch.mkdir()
    app = scratch / "TunnelBoard.exe"
    helper = scratch / "tunnelboard-helper.exe"
    caddy = stage / "caddy" / "caddy.exe"
    run(["go", "build", "-v", "-trimpath", "-o", str(helper), "./cmd/helper"])
    # 签名会改变 PE 字节，必须先签 Helper，再把最终 SHA 注入主程序。
    helper_sign = sign_windows(helper, require_signing)
    helper_digest = sha256_file(helper)
    caddy_entry = fetch_caddy("windows-amd64", caddy)
    ldflags = (
        f"-X main.helperBundleSHA256={helper_digest} "
        f"-X main.caddyBundleVersion={caddy_entry['version']} "
        f"-X main.caddyBundleSHA256={caddy_entry['binary_sha256']}"
    )
    run(
        [
            "wails",
            "build",
            "-clean",
            "-platform",
            "windows/amd64",
            "-o",
            app.name,
            "-ldflags",
            ldflags,
        ]
    )
    embedded = frontend_inventory()
    built_app = ROOT / "build" / "bin" / app.name
    if not built_app.is_file():
        raise ReleaseError(f"Wails did not produce {built_app}")
    shutil.copy2(built_app, app)
    shutil.copy2(app, stage / app.name)
    shutil.copy2(helper, stage / helper.name)
    write_licenses(stage / "LICENSES", caddy_entry["version"])
    app_sign = sign_windows(stage / app.name, require_signing)
    if require_signing and app_sign.get("publisher") != helper_sign.get("publisher"):
        raise ReleaseError("application and Helper Authenticode publishers differ")
    signing = {
        "required": require_signing,
        "status": "valid" if require_signing else "unsigned-ci",
        "application": app_sign,
        "helper": helper_sign,
    }
    helper_info = validate_windows_helper(stage / helper.name)
    manifest = stage / "manifest.json"
    write_manifest(stage, manifest, "windows-amd64", version, caddy_entry, embedded, signing, helper_info)
    base = f"TunnelBoard-{version}-windows-x64"
    bundle = output / f"{base}.bundle.zip"
    zip_directory(stage, bundle)
    sidecar = output / f"{base}.manifest.json"
    shutil.copy2(manifest, sidecar)
    outputs = [bundle, sidecar]
    if not skip_installer:
        wix = find_wix()
        if not wix:
            raise ReleaseError("WiX v4 is required for the Windows installer")
        installer_root = ROOT / "scripts" / "windows-installer"
        installer_version = windows_installer_version(version)
        msi = scratch / f"{base}.msi"
        setup = output / f"{base}-setup.exe"
        run([
            wix,
            "build",
            "-arch",
            "x64",
            "-d",
            f"SourceDir={stage}",
            "-d",
            f"MsiVersion={installer_version}",
            "-o",
            str(msi),
            str(installer_root / "Product.wxs"),
        ])
        msi_sign = sign_windows(msi, require_signing)
        if require_signing and msi_sign.get("publisher") != app_sign.get("publisher"):
            raise ReleaseError("MSI and application Authenticode publishers differ")
        run([
            wix,
            "build",
            "-arch",
            "x64",
            "-ext",
            "WixToolset.Bal.wixext",
            "-d",
            f"MsiPath={msi}",
            "-d",
            f"BundleVersion={installer_version}",
            "-o",
            str(setup),
            str(installer_root / "Bundle.wxs"),
        ])
        setup_sign = sign_windows(setup, require_signing)
        if require_signing and setup_sign.get("publisher") != app_sign.get("publisher"):
            raise ReleaseError("installer and application Authenticode publishers differ")
        outputs.append(setup)
    checksums = output / f"{base}.SHA256SUMS"
    write_checksums([path for path in outputs if not path.name.endswith(".bundle.zip")], checksums)
    outputs.append(checksums)
    verify_bundle_root(stage, execute_caddy=True)
    return outputs


def build_darwin(version: str) -> list[Path]:
    if platform.system() != "Darwin":
        raise ReleaseError("darwin-universal must be built on a macOS runner")
    stage, output = prepare_release_root("darwin-universal")
    scratch = RELEASE_ROOT / "darwin-universal" / "scratch"
    scratch.mkdir()
    amd64 = scratch / "caddy-amd64"
    arm64 = scratch / "caddy-arm64"
    entry_amd64 = fetch_caddy("darwin-amd64", amd64)
    entry_arm64 = fetch_caddy("darwin-arm64", arm64)
    universal = scratch / "caddy"
    run(["lipo", "-create", "-output", str(universal), str(amd64), str(arm64)])
    universal.chmod(0o755)
    run(["codesign", "--force", "--sign", "-", str(universal)])
    caddy_digest = sha256_file(universal)
    ldflags = (
        f"-X main.caddyBundleVersion={entry_amd64['version']} "
        f"-X main.caddyBundleSHA256={caddy_digest}"
    )
    run(["wails", "build", "-clean", "-platform", "darwin/universal", "-ldflags", ldflags])
    embedded = frontend_inventory()
    built_app = ROOT / "build" / "bin" / "tunnelboard.app"
    if not built_app.is_dir():
        raise ReleaseError(f"Wails did not produce {built_app}")
    app = stage / "TunnelBoard.app"
    shutil.copytree(built_app, app)
    caddy_dir = app / "Contents" / "MacOS" / "caddy"
    caddy_dir.mkdir()
    shutil.copy2(universal, caddy_dir / "caddy")
    write_licenses(stage / "LICENSES", entry_amd64["version"])
    # 嵌套 Caddy 已先签名；签整个 App 时禁止 --deep 再次改写其字节，
    # 否则主程序中注入的 Caddy SHA 会在打包后立即失效。
    run(["codesign", "--force", "--sign", "-", str(app)])
    # universal Caddy is a packaging transform; record both pinned upstream inputs.
    combined = {
        "version": entry_amd64["version"],
        "binary_sha256": sha256_file(caddy_dir / "caddy"),
        "inputs": [
            {
                "target": entry_amd64["target"],
                "archive_sha256": entry_amd64["archive_sha256"],
                "binary_sha256": entry_amd64["binary_sha256"],
            },
            {
                "target": entry_arm64["target"],
                "archive_sha256": entry_arm64["archive_sha256"],
                "binary_sha256": entry_arm64["binary_sha256"],
            },
        ],
    }
    manifest = stage / "manifest.json"
    write_manifest(
        stage,
        manifest,
        "darwin-universal",
        version,
        combined,
        embedded,
        {"required": True, "status": "ad-hoc", "notarized": False},
    )
    base = f"TunnelBoard-{version}-macos-universal"
    bundle = output / f"{base}.bundle.zip"
    zip_directory(stage, bundle)
    sidecar = output / f"{base}.manifest.json"
    shutil.copy2(manifest, sidecar)
    dmg_root = RELEASE_ROOT / "darwin-universal" / "dmg"
    shutil.copytree(stage, dmg_root)
    dmg = output / f"{base}.dmg"
    run(["hdiutil", "create", "-volname", "TunnelBoard", "-srcfolder", str(dmg_root), "-ov", "-format", "UDZO", str(dmg)])
    verify_bundle_root(stage)
    checksums = output / "SHA256SUMS"
    write_checksums([sidecar, dmg], checksums)
    return [bundle, sidecar, dmg, checksums]


def build_linux(target: str, version: str, require_signing: bool) -> list[Path]:
    if platform.system() != "Linux":
        raise ReleaseError(f"{target} must be built on a Linux runner")
    target_info = RELEASE_TARGETS[target]
    if target_info["os"] != "linux":
        raise ReleaseError(f"not a Linux release target: {target}")
    deb_arch, rpm_arch = linux_arches(target_info["arch"])
    payload, output, scratch = prepare_linux_release_root(target)
    app = scratch / "tunnelboard"
    helper = scratch / "tunnelboard-linux-helper"
    caddy = scratch / "caddy"
    helper_source = ROOT / "cmd" / "linux-helper"
    if not helper_source.is_dir():
        raise ReleaseError("cmd/linux-helper is required for a Linux package")
    run(["go", "build", "-v", "-trimpath", "-o", str(helper), "./cmd/linux-helper"])
    helper_digest = sha256_file(helper)
    caddy_entry = fetch_caddy(f"linux-{target_info['arch']}", caddy)
    ldflags = (
        f"-X main.helperBundleSHA256={helper_digest} "
        f"-X main.caddyBundleVersion={caddy_entry['version']} "
        f"-X main.caddyBundleSHA256={caddy_entry['binary_sha256']}"
    )
    # Debian 12 and RHEL 9 provide the WebKitGTK 4.0 ABI. Keep the Wails
    # build tag at that common floor even when a newer Debian derivative also
    # offers WebKitGTK 4.1.
    webkit_tag = "webkit2_40"
    run(
        [
            "wails", "build", "-clean", "-platform", f"linux/{target_info['arch']}", "-tags", webkit_tag,
            "-o", app.name, "-ldflags", ldflags,
        ]
    )
    built_app = ROOT / "build" / "bin" / app.name
    if not built_app.is_file():
        raise ReleaseError(f"Wails did not produce {built_app}")
    shutil.copy2(built_app, app)
    stage_linux_payload(payload, app, helper, caddy, caddy_entry["version"])
    signing: dict[str, Any] = {
        "required": require_signing,
        "status": "gpg-detached-checksums" if require_signing else "unsigned-ci",
        "package_signature": "rpm-native-and-gpg-checksums" if target_info["package_format"] == "rpm" else "gpg-checksums",
    }
    signer = prepare_linux_gpg_signer(require_signing, scratch)
    if signer is not None:
        signing["fingerprint"] = signer[1]
    manifest = payload / "opt" / "tunnelboard" / "manifest.json"
    write_manifest(
        payload,
        manifest,
        target,
        version,
        caddy_entry,
        frontend_inventory(),
        signing,
        {"protocol_version": "linux-polkit-v1", "persistent_service": False},
    )
    base = linux_package_basename(target, version)
    if target_info["package_format"] == "deb":
        package = build_debian_package(payload, output, version, deb_arch, base)
    else:
        package = build_rpm_package(payload, output, version, rpm_arch, base)
        if signer is not None:
            sign_rpm(package, signer)
    sidecar = output / f"{base}.manifest.json"
    shutil.copy2(manifest, sidecar)
    checksums = output / "SHA256SUMS"
    write_checksums([package, sidecar], checksums)
    outputs = [package, sidecar, checksums]
    if signer is not None:
        signature, public_key = sign_linux_checksums(checksums, output, signer)
        outputs.extend([signature, public_key])
    verify_bundle_root(payload)
    return outputs


def build_target(target: str, version: str, require_signing: bool, skip_installer: bool) -> list[Path]:
    if not version or any(ch not in "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz.-_" for ch in version):
        raise ReleaseError("version contains unsafe characters")
    if target == "windows-amd64":
        return build_windows(version, require_signing, skip_installer)
    if target == "darwin-universal":
        return build_darwin(version)
    if target in RELEASE_TARGETS and RELEASE_TARGETS[target]["os"] == "linux":
        return build_linux(target, version, require_signing)
    raise ReleaseError(f"unsupported release target: {target}")


def cli() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    fetch = subparsers.add_parser("fetch-caddy", help="下载并校验指定平台的钉版 Caddy")
    fetch.add_argument("--target", required=True)
    fetch.add_argument("--output", type=Path, required=True)

    verify = subparsers.add_parser("verify", help="解包并验证最终 verification bundle")
    verify.add_argument("--artifact", type=Path, required=True)
    verify.add_argument("--execute-caddy", action="store_true")
    verify.add_argument("--supply-chain-lock", type=Path, default=LOCK_PATH, help=argparse.SUPPRESS)

    poc = subparsers.add_parser("caddy-poc", help="验证钉版 Caddy AF_UNIX admin 且无 TCP 回退")
    poc.add_argument("--binary", type=Path, required=True)

    installer = subparsers.add_parser("verify-windows-installer", help="安装、验证并卸载最终 Windows setup")
    installer.add_argument("--installer", type=Path, required=True)
    installer.add_argument("--require-signing", action="store_true")

    linux_package = subparsers.add_parser("verify-linux-package", help="解包并验证最终 Linux 原生包")
    linux_package.add_argument("--artifact", type=Path, required=True)
    linux_package.add_argument("--require-signing", action="store_true")
    linux_package.add_argument("--rpm-public-key", type=Path)

    linux_checksums = subparsers.add_parser("verify-linux-checksums", help="验证 Linux 发布校验清单及 GPG 签名")
    linux_checksums.add_argument("--checksums", type=Path, required=True)
    linux_checksums.add_argument("--signature", type=Path, required=True)
    linux_checksums.add_argument("--public-key", type=Path, required=True)

    release_assets = subparsers.add_parser("prepare-linux-release-assets", help="生成名称唯一的 Linux Release checksum 与签名资产")
    release_assets.add_argument("--source", type=Path, required=True)
    release_assets.add_argument("--output", type=Path, required=True)

    build = subparsers.add_parser("build", help="从空 staging 构建指定平台正式候选产物")
    build.add_argument("--target", required=True, choices=sorted(RELEASE_TARGETS))
    build.add_argument("--version", required=True)
    build.add_argument("--require-signing", action="store_true")
    build.add_argument("--skip-installer", action="store_true", help=argparse.SUPPRESS)

    args = parser.parse_args()
    try:
        if args.command == "fetch-caddy":
            entry = fetch_caddy(args.target, args.output)
            print(f"CADDY OK v{entry['version']} {args.target} {args.output}")
        elif args.command == "verify":
            with tempfile.TemporaryDirectory(prefix="tunnelboard-artifact-verify-") as raw:
                root = extract_artifact(args.artifact.resolve(), Path(raw))
                manifest = verify_bundle_root(
                    root,
                    execute_caddy=args.execute_caddy,
                    supply_chain_lock=args.supply_chain_lock.resolve(),
                )
            print(f"VERIFY PASS {manifest['target']['name']} {manifest['version']}")
        elif args.command == "caddy-poc":
            validate_caddy_runtime(args.binary.resolve())
            print("CADDY AF_UNIX POC PASS")
        elif args.command == "verify-windows-installer":
            verify_windows_installer(args.installer.resolve(), args.require_signing)
            print("WINDOWS INSTALLER VERIFY PASS")
        elif args.command == "verify-linux-package":
            manifest = verify_linux_package(
                args.artifact.resolve(),
                args.require_signing,
                args.rpm_public_key.resolve() if args.rpm_public_key else None,
            )
            print(f"LINUX PACKAGE VERIFY PASS {manifest['target']['name']} {manifest['version']}")
        elif args.command == "verify-linux-checksums":
            verify_linux_release_checksums(args.checksums.resolve(), args.signature.resolve(), args.public_key.resolve())
            print("LINUX CHECKSUM SIGNATURE VERIFY PASS")
        elif args.command == "prepare-linux-release-assets":
            outputs = prepare_linux_release_assets(args.source.resolve(), args.output.resolve())
            for output in outputs:
                print(f"RELEASE ASSET {output} sha256={sha256_file(output)}")
        elif args.command == "build":
            outputs = build_target(args.target, args.version, args.require_signing, args.skip_installer)
            for output in outputs:
                print(f"ARTIFACT {output} sha256={sha256_file(output)}")
        return 0
    except (
        ReleaseError,
        OSError,
        json.JSONDecodeError,
        zipfile.BadZipFile,
        tarfile.TarError,
        subprocess.TimeoutExpired,
    ) as exc:
        print(f"RELEASE-FAIL: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(cli())
