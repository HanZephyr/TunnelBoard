import hashlib
import json
import subprocess
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
RELEASE = ROOT / "scripts" / "release.py"
LOCK = ROOT / "scripts" / "caddy-lock.json"


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class ReleaseVerifierCLITest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.root = Path(self.tempdir.name) / "TunnelBoard"
        (self.root / "caddy").mkdir(parents=True)
        (self.root / "LICENSES").mkdir()
        files = {
            "TunnelBoard.exe": (b"app", "application", True),
            "tunnelboard-helper.exe": (b"helper", "privileged_helper", True),
            "caddy/caddy.exe": (b"caddy", "caddy", True),
            "LICENSES/TunnelBoard.txt": (b"license", "license", False),
        }
        manifest_files = []
        for rel, (payload, role, executable) in files.items():
            path = self.root / rel
            path.write_bytes(payload)
            manifest_files.append(
                {
                    "path": rel,
                    "role": role,
                    "size": len(payload),
                    "sha256": digest(payload),
                    "executable": executable,
                }
            )
        manifest = {
            "schema_version": 1,
            "product": "TunnelBoard",
            "version": "1.2.3",
            "git_commit": "a" * 40,
            "build": {"workflow": "test", "run_id": "1"},
            "target": {"name": "windows-amd64", "os": "windows", "arch": "amd64"},
            "files": manifest_files,
            "embedded_assets": [],
            "caddy": {
                "version": "2.11.4",
                "result_binary_sha256": digest(b"caddy"),
                "inputs": [{"target": "windows-amd64", "binary_sha256": digest(b"caddy")}],
            },
            "tools": {},
            "signing": {"required": False, "status": "unsigned-ci"},
            "support": {"real_machine_smoke": "pending"},
        }
        (self.root / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
        self.lock = Path(self.tempdir.name) / "test-caddy-lock.json"
        self.lock.write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "version": "2.11.4",
                    "targets": {
                        "windows-amd64": {
                            "os": "windows",
                            "arch": "amd64",
                            "binary_sha256": digest(b"caddy"),
                        }
                    },
                }
            ),
            encoding="utf-8",
        )

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def archive(self) -> Path:
        archive = Path(self.tempdir.name) / "artifact.zip"
        with zipfile.ZipFile(archive, "w") as zf:
            for path in self.root.rglob("*"):
                if path.is_file():
                    zf.write(path, Path("TunnelBoard") / path.relative_to(self.root))
        return archive

    def run_verify(self, archive: Path) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                sys.executable,
                str(RELEASE),
                "verify",
                "--artifact",
                str(archive),
                "--supply-chain-lock",
                str(self.lock),
            ],
            cwd=ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )

    def test_verified_bundle_passes(self) -> None:
        result = self.run_verify(self.archive())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("VERIFY PASS", result.stdout)

    def test_missing_required_file_fails(self) -> None:
        (self.root / "tunnelboard-helper.exe").unlink()
        result = self.run_verify(self.archive())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("missing", result.stderr.lower())

    def test_tampered_file_fails(self) -> None:
        (self.root / "caddy" / "caddy.exe").write_bytes(b"xxxxx")
        result = self.run_verify(self.archive())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sha256", result.stderr.lower())

    def test_undeclared_executable_fails(self) -> None:
        (self.root / "debug.exe").write_bytes(b"debug")
        result = self.run_verify(self.archive())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("undeclared", result.stderr.lower())

    def test_manifest_path_traversal_fails(self) -> None:
        manifest_path = self.root / "manifest.json"
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        manifest["files"][0]["path"] = "../outside.exe"
        manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        result = self.run_verify(self.archive())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unsafe", result.stderr.lower())


class CaddySupplyChainLockTest(unittest.TestCase):
    def test_supported_targets_have_pinned_archive_and_binary_hashes(self) -> None:
        lock = json.loads(LOCK.read_text(encoding="utf-8"))
        self.assertEqual(lock["version"], "2.11.4")
        for target in ("windows-amd64", "darwin-amd64", "darwin-arm64", "linux-amd64"):
            entry = lock["targets"][target]
            self.assertTrue(entry["url"].startswith("https://github.com/caddyserver/caddy/releases/download/v2.11.4/"))
            self.assertRegex(entry["archive_sha256"], r"^[0-9a-f]{64}$")
            self.assertRegex(entry["binary_sha256"], r"^[0-9a-f]{64}$")
            self.assertGreater(entry["archive_size"], 1_000_000)
            self.assertLess(entry["max_download_bytes"], 25_000_000)


class GitHubActionsReleaseContractTest(unittest.TestCase):
    def test_workflow_delegates_packaging_and_creates_draft_only(self) -> None:
        workflow = (ROOT / ".github" / "workflows" / "build.yml").read_text(encoding="utf-8")
        self.assertIn("quality-gate:", workflow)
        self.assertIn("verify-artifacts:", workflow)
        self.assertIn("draft-release:", workflow)
        self.assertIn("uv run scripts/release.py build --target windows-amd64", workflow)
        self.assertIn("uv run scripts/release.py build --target darwin-universal", workflow)
        windows_job = workflow.split("  build-windows:", 1)[1].split("  build-macos:", 1)[0]
        macos_job = workflow.split("  build-macos:", 1)[1].split("  verify-artifacts:", 1)[0]
        self.assertNotIn("run: wails build", windows_job)
        self.assertNotIn("run: wails build", macos_job)
        self.assertIn("gh release create", workflow)
        self.assertIn("--draft", workflow)
        self.assertNotIn("draft: false", workflow)
        self.assertNotIn("build/bin/*.exe", workflow)
        self.assertNotIn("artifacts/**/*", workflow)

    def test_linux_is_explicitly_not_published(self) -> None:
        workflow = (ROOT / ".github" / "workflows" / "build.yml").read_text(encoding="utf-8")
        self.assertIn("Linux: not delivered", workflow)
        self.assertNotIn("build-linux:", workflow)


if __name__ == "__main__":
    unittest.main()
