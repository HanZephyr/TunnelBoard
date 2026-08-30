import hashlib
import importlib.util
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


def load_release_module():
    spec = importlib.util.spec_from_file_location("tunnelboard_release", RELEASE)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def fake_pe(payload: bytes, machine: int = 0x8664) -> bytes:
    data = bytearray(70 + len(payload))
    data[:2] = b"MZ"
    data[0x3C:0x40] = (64).to_bytes(4, "little")
    data[64:68] = b"PE\0\0"
    data[68:70] = machine.to_bytes(2, "little")
    data[70:] = payload
    return bytes(data)


def fake_elf(payload: bytes, machine: int = 0x003E) -> bytes:
    data = bytearray(20 + len(payload))
    data[:4] = b"\x7fELF"
    data[4] = 2
    data[5] = 1
    data[18:20] = machine.to_bytes(2, "little")
    data[20:] = payload
    return bytes(data)


class ReleaseVersionNormalizationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.release = load_release_module()

    def test_debian_version_removes_tag_prefix(self) -> None:
        self.assertEqual(self.release.debian_version("v1.0.6"), "1.0.6")

    def test_debian_version_preserves_ci_version(self) -> None:
        self.assertEqual(self.release.debian_version("0.0.0-ci.42"), "0.0.0-ci.42")

    def test_debian_version_requires_numeric_prefix(self) -> None:
        with self.assertRaises(self.release.ReleaseError):
            self.release.debian_version("vnext")


class LinuxReleaseAssetTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.release = load_release_module()

    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.source = Path(self.tempdir.name) / "linux"
        self.output = Path(self.tempdir.name) / "release-assets"
        self.targets = ("debian-amd64", "debian-arm64", "rhel-amd64", "rhel-arm64")
        for target in self.targets:
            directory = self.source / target
            directory.mkdir(parents=True)
            (directory / "SHA256SUMS").write_text(f"checksum {target}\n", encoding="utf-8")
            (directory / "SHA256SUMS.asc").write_text(f"signature {target}\n", encoding="utf-8")
            (directory / "SHA256SUMS.public-key.asc").write_text("shared release public key\n", encoding="utf-8")

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_prepares_unique_checksum_assets_and_one_shared_public_key(self) -> None:
        outputs = self.release.prepare_linux_release_assets(self.source, self.output)
        expected = {
            *(f"TunnelBoard-{target}.SHA256SUMS" for target in self.targets),
            *(f"TunnelBoard-{target}.SHA256SUMS.asc" for target in self.targets),
            "TunnelBoard-linux-gpg-public-key.asc",
        }
        self.assertEqual({path.name for path in outputs}, expected)
        self.assertEqual((self.output / "TunnelBoard-debian-amd64.SHA256SUMS").read_text(encoding="utf-8"), "checksum debian-amd64\n")
        self.assertEqual((self.output / "TunnelBoard-linux-gpg-public-key.asc").read_text(encoding="utf-8"), "shared release public key\n")

    def test_rejects_mismatched_linux_public_keys(self) -> None:
        (self.source / "rhel-arm64" / "SHA256SUMS.public-key.asc").write_text("unexpected key\n", encoding="utf-8")
        with self.assertRaises(self.release.ReleaseError):
            self.release.prepare_linux_release_assets(self.source, self.output)


class ReleaseVerifierCLITest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.root = Path(self.tempdir.name) / "TunnelBoard"
        (self.root / "caddy").mkdir(parents=True)
        (self.root / "LICENSES").mkdir()
        files = {
            "TunnelBoard.exe": (fake_pe(b"app"), "application", True),
            "tunnelboard-helper.exe": (fake_pe(b"helper"), "privileged_helper", True),
            "caddy/caddy.exe": (fake_pe(b"caddy"), "caddy", True),
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
            "target": {"name": "windows-amd64", "os": "windows", "arch": "amd64", "minimum_system": "Windows 10 1809"},
            "files": manifest_files,
            "embedded_assets": [],
            "caddy": {
                "version": "2.11.4",
                "result_binary_sha256": digest(fake_pe(b"caddy")),
                "inputs": [{"target": "windows-amd64", "binary_sha256": digest(fake_pe(b"caddy"))}],
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
                            "binary_sha256": digest(fake_pe(b"caddy")),
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
        caddy = self.root / "caddy" / "caddy.exe"
        tampered = bytearray(caddy.read_bytes())
        tampered[-1] ^= 0xFF
        caddy.write_bytes(tampered)
        result = self.run_verify(self.archive())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sha256", result.stderr.lower())

    def test_wrong_application_architecture_fails(self) -> None:
        app = self.root / "TunnelBoard.exe"
        app.write_bytes(fake_pe(b"app", machine=0x014C))
        manifest_path = self.root / "manifest.json"
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        for record in manifest["files"]:
            if record["path"] == "TunnelBoard.exe":
                record["size"] = app.stat().st_size
                record["sha256"] = digest(app.read_bytes())
        manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        result = self.run_verify(self.archive())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("architecture mismatch", result.stderr.lower())

    def test_undeclared_executable_fails(self) -> None:
        (self.root / "debug.exe").write_bytes(b"debug")
        result = self.run_verify(self.archive())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("undeclared", result.stderr.lower())

    def test_executable_outside_manifest_root_fails(self) -> None:
        archive = self.archive()
        with zipfile.ZipFile(archive, "a") as zf:
            zf.writestr("sibling-debug.exe", b"debug")
        result = self.run_verify(archive)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("single top-level", result.stderr.lower())

    def test_manifest_path_traversal_fails(self) -> None:
        manifest_path = self.root / "manifest.json"
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        manifest["files"][0]["path"] = "../outside.exe"
        manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        result = self.run_verify(self.archive())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unsafe", result.stderr.lower())


class LinuxPayloadVerifierCLITest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.root = Path(self.tempdir.name) / "TunnelBoard"
        files = {
            "opt/tunnelboard/tunnelboard": (fake_elf(b"app"), "application", True),
            "opt/tunnelboard/caddy/caddy": (fake_elf(b"caddy"), "caddy", True),
            "opt/tunnelboard/LICENSES/TunnelBoard.txt": (b"license", "license", False),
            "usr/libexec/tunnelboard/tunnelboard-linux-helper": (fake_elf(b"helper"), "privileged_helper", True),
            "usr/share/polkit-1/actions/io.github.hanzephyr.TunnelBoard.policy": (b"policy", "polkit_policy", False),
            "usr/share/applications/io.github.hanzephyr.TunnelBoard.desktop": (b"desktop", "desktop_entry", False),
        }
        manifest_files = []
        for rel, (payload, role, executable) in files.items():
            path = self.root / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(payload)
            manifest_files.append({"path": rel, "role": role, "size": len(payload), "sha256": digest(payload), "executable": executable})
        manifest = {
            "schema_version": 1,
            "product": "TunnelBoard",
            "version": "1.2.3",
            "git_commit": "a" * 40,
            "build": {"workflow": "test", "run_id": "1"},
            "target": {
                "name": "linux-debian-amd64",
                "os": "linux",
                "arch": "amd64",
                "minimum_system": "Debian 12 / Ubuntu 24.04 LTS",
            },
            "files": manifest_files,
            "embedded_assets": [],
            "caddy": {
                "version": "2.11.4",
                "result_binary_sha256": digest(fake_elf(b"caddy")),
                "inputs": [{"target": "linux-amd64", "binary_sha256": digest(fake_elf(b"caddy"))}],
            },
            "tools": {},
            "signing": {"required": False, "status": "unsigned-ci"},
            "support": {"real_machine_smoke": "pending"},
        }
        manifest_path = self.root / "opt" / "tunnelboard" / "manifest.json"
        manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
        self.lock = Path(self.tempdir.name) / "test-caddy-lock.json"
        self.lock.write_text(
            json.dumps(
                {"schema_version": 1, "version": "2.11.4", "targets": {"linux-amd64": {"binary_sha256": digest(fake_elf(b"caddy"))}}}
            ),
            encoding="utf-8",
        )

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_linux_payload_manifest_covers_files_outside_opt(self) -> None:
        archive = Path(self.tempdir.name) / "linux-payload.zip"
        with zipfile.ZipFile(archive, "w") as zf:
            for path in self.root.rglob("*"):
                if path.is_file():
                    zf.write(path, Path("TunnelBoard") / path.relative_to(self.root))
        result = subprocess.run(
            [sys.executable, str(RELEASE), "verify", "--artifact", str(archive), "--supply-chain-lock", str(self.lock)],
            cwd=ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("VERIFY PASS linux-debian-amd64", result.stdout)


class CaddySupplyChainLockTest(unittest.TestCase):
    def test_supported_targets_have_pinned_archive_and_binary_hashes(self) -> None:
        lock = json.loads(LOCK.read_text(encoding="utf-8"))
        self.assertEqual(lock["version"], "2.11.4")
        for target in ("windows-amd64", "darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64"):
            entry = lock["targets"][target]
            self.assertTrue(entry["url"].startswith("https://github.com/caddyserver/caddy/releases/download/v2.11.4/"))
            self.assertRegex(entry["archive_sha256"], r"^[0-9a-f]{64}$")
            self.assertRegex(entry["binary_sha256"], r"^[0-9a-f]{64}$")
            self.assertGreater(entry["archive_size"], 1_000_000)
            self.assertLess(entry["max_download_bytes"], 25_000_000)


class LinuxReleaseTargetCLITest(unittest.TestCase):
    def test_linux_native_package_targets_are_recognized(self) -> None:
        for target in (
            "linux-debian-amd64",
            "linux-debian-arm64",
            "linux-rhel-amd64",
            "linux-rhel-arm64",
        ):
            result = subprocess.run(
                [sys.executable, str(RELEASE), "build", "--target", target, "--version", "0.0.0-ci.1"],
                cwd=ROOT,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
            self.assertNotIn("invalid choice", result.stderr, target)
            self.assertIn("must be built on a Linux runner", result.stderr, target)


class LinuxNativePackagingContractTest(unittest.TestCase):
    def test_linux_package_metadata_only_installs_managed_payload_and_cleanup_hook(self) -> None:
        packaging = ROOT / "scripts" / "linux-packaging"
        debian_control = (packaging / "debian" / "control.in").read_text(encoding="utf-8")
        debian_prerm = (packaging / "debian" / "prerm").read_text(encoding="utf-8")
        rpm_spec = (packaging / "rpm" / "tunnelboard.spec.in").read_text(encoding="utf-8")
        policy = (packaging / "io.github.hanzephyr.TunnelBoard.policy").read_text(encoding="utf-8")
        desktop = (packaging / "io.github.hanzephyr.TunnelBoard.desktop").read_text(encoding="utf-8")

        self.assertIn("Package: tunnelboard", debian_control)
        self.assertIn("libwebkit2gtk-4.1-0", debian_control)
        self.assertIn("/usr/libexec/tunnelboard/tunnelboard-linux-helper package-uninstall", debian_prerm)
        self.assertIn("/usr/libexec/tunnelboard/tunnelboard-linux-helper package-uninstall", rpm_spec)
        self.assertIn("Requires:       webkit2gtk3", rpm_spec)
        self.assertIn("%global __os_install_post %{nil}", rpm_spec)
        self.assertIn("%global _build_id_links none", rpm_spec)
        self.assertIn("remove|deconfigure", debian_prerm)
        self.assertNotIn("upgrade)", debian_prerm)
        self.assertIn("%preun", rpm_spec)
        self.assertNotIn("%postun", rpm_spec)
        self.assertIn('[ "$1" -eq 0 ]', rpm_spec)
        self.assertIn("io.github.hanzephyr.TunnelBoard.manage-system", policy)
        self.assertIn("auth_admin_keep", policy)
        self.assertIn("Exec=/opt/tunnelboard/tunnelboard", desktop)
        self.assertIn(
            'webkit_tag = "webkit2_40"',
            RELEASE.read_text(encoding="utf-8"),
        )


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
        self.assertIn('if: ${{ !startsWith(github.ref, \'refs/tags/v\') }}', macos_job)
        self.assertIn('if: ${{ startsWith(github.ref, \'refs/tags/v\') }}', macos_job)
        self.assertIn('--version "${{ github.ref_name }}"', macos_job)
        self.assertIn("gh release create", workflow)
        self.assertIn("--draft", workflow)
        self.assertNotIn("draft: false", workflow)
        self.assertNotIn("build/bin/*.exe", workflow)
        self.assertNotIn("artifacts/**/*", workflow)

    def test_linux_native_packages_are_built_and_verified_before_draft_release(self) -> None:
        workflow = (ROOT / ".github" / "workflows" / "build.yml").read_text(encoding="utf-8")
        release_script = (ROOT / "scripts" / "release.py").read_text(encoding="utf-8")
        self.assertIn("build-linux:", workflow)
        self.assertIn("linux-debian-amd64", workflow)
        self.assertIn("linux-debian-arm64", workflow)
        self.assertIn("linux-rhel-amd64", workflow)
        self.assertIn("linux-rhel-arm64", workflow)
        self.assertIn("verify-linux-package", workflow)
        self.assertIn("TUNNELBOARD_GPG_SIGNING_KEY_BASE64", workflow)
        self.assertIn("TUNNELBOARD_GPG_KEY_FINGERPRINT", workflow)
        linux_verification = workflow.split("  verify-linux-artifacts:", 1)[1].split("  draft-release:", 1)[0]
        self.assertIn("-name 'SHA256SUMS.public-key.asc'", linux_verification)
        self.assertIn("-name 'SHA256SUMS.asc'", linux_verification)
        self.assertNotIn("-name '*.SHA256SUMS", linux_verification)
        windows_checksum_lookup = "find artifacts/windows -maxdepth 1 -type f -name '*.SHA256SUMS'"
        self.assertEqual(workflow.count(windows_checksum_lookup), 2)
        self.assertNotIn("artifacts/linux/*/*.SHA256SUMS", workflow)
        self.assertIn("checksums = output / f\"{base}.SHA256SUMS\"", release_script)
        self.assertIn("checksums = output / \"SHA256SUMS\"", release_script)
        self.assertIn('cat "$windows_checksums" artifacts/macos/SHA256SUMS artifacts/linux/*/SHA256SUMS', workflow)
        self.assertNotIn("artifacts/windows/SHA256SUMS", workflow)
        draft_release = workflow.split("  draft-release:", 1)[1]
        self.assertIn("Checkout release assembler from the same commit", draft_release)
        self.assertIn("prepare-linux-release-assets", draft_release)
        self.assertIn("artifacts/release-assets/*.SHA256SUMS", draft_release)
        self.assertIn("artifacts/release-assets/*.SHA256SUMS.asc", draft_release)
        self.assertIn("artifacts/release-assets/TunnelBoard-linux-gpg-public-key.asc", draft_release)
        self.assertIn("--draft", workflow)
        self.assertIn("Linux native packages remain draft", workflow)

    def test_draft_release_includes_unsigned_macos_dmg_with_gatekeeper_notice(self) -> None:
        workflow = (ROOT / ".github" / "workflows" / "build.yml").read_text(encoding="utf-8")
        draft_release = workflow.split("  draft-release:", 1)[1]
        self.assertIn("Download the verified macOS candidate", draft_release)
        self.assertIn("artifacts/macos/*.dmg", draft_release)
        self.assertIn("macOS: ad-hoc signed and not notarized", draft_release)
        self.assertIn("Gatekeeper", draft_release)

    def test_windows_installer_removes_legacy_service_before_copying_files(self) -> None:
        installer = (ROOT / "scripts" / "windows-installer" / "Product.wxs").read_text(encoding="utf-8")
        cleanup = installer.find('Id="CleanupLegacyService"')
        copy_payload = installer.find('Before="InstallFiles"')
        self.assertGreaterEqual(cleanup, 0, "installer must invoke the fixed legacy-service cleanup command")
        self.assertGreater(copy_payload, cleanup, "legacy SYSTEM service must be removed before replacing payload files")
        self.assertIn("--cleanup-legacy-service", installer[cleanup:copy_payload])
        self.assertIn('Return="check"', installer[cleanup:copy_payload], "cleanup failure must stop the upgrade")

    def test_windows_uninstaller_removes_current_user_ca_before_deleting_helper(self) -> None:
        installer = (ROOT / "scripts" / "windows-installer" / "Product.wxs").read_text(encoding="utf-8")
        cleanup = installer.find('Id="CleanupCurrentUserCA"')
        delete_helper = installer.find('Before="RemoveFiles"')
        self.assertGreaterEqual(cleanup, 0, "uninstaller must offer the fixed current-user CA cleanup")
        self.assertGreater(delete_helper, cleanup, "CA and its private key must be removed before deleting the cleanup binary")
        self.assertIn("--cleanup-current-user-ca", installer[cleanup:delete_helper])
        self.assertIn('Return="check"', installer[cleanup:delete_helper], "failed trust cleanup must not silently orphan a root CA")

    def test_windows_installer_uses_wix_burn_instead_of_nsis(self) -> None:
        workflow = (ROOT / ".github" / "workflows" / "build.yml").read_text(encoding="utf-8")
        release = RELEASE.read_text(encoding="utf-8")
        bundle = (ROOT / "scripts" / "windows-installer" / "Bundle.wxs").read_text(encoding="utf-8")
        self.assertFalse((ROOT / "scripts" / "windows-installer.nsi").exists())
        self.assertIn("dotnet tool install", workflow)
        self.assertNotIn("choco install nsis", workflow)
        self.assertIn('WixStandardBootstrapperApplication', bundle)
        self.assertIn('Theme="hyperlinkLargeLicense"', bundle)
        self.assertIn('bal:Overridable="yes"', bundle)
        self.assertIn("def find_wix", release)
        self.assertIn('"/quiet", "/norestart"', release)

    def test_windows_installer_expands_default_path_and_allows_folder_selection(self) -> None:
        bundle = (ROOT / "scripts" / "windows-installer" / "Bundle.wxs").read_text(encoding="utf-8")
        self.assertIn(
            'Name="InstallFolder" Type="formatted" Value="[ProgramFiles64Folder]\\TunnelBoard"',
            bundle,
            "WiX v4 must expand the Program Files variable before passing it to MSI",
        )
        self.assertIn(
            'SuppressOptionsUI="no"',
            bundle,
            "the interactive installer must expose the Options page for selecting an install folder",
        )
        self.assertIn('<MsiProperty Name="INSTALLFOLDER" Value="[InstallFolder]" />', bundle)

    def test_windows_installer_exposes_directory_and_shortcut_choices_with_one_icon(self) -> None:
        bundle = (ROOT / "scripts" / "windows-installer" / "Bundle.wxs").read_text(encoding="utf-8")
        product = (ROOT / "scripts" / "windows-installer" / "Product.wxs").read_text(encoding="utf-8")
        theme_path = ROOT / "scripts" / "windows-installer" / "installer-theme.xml"
        self.assertTrue(theme_path.is_file(), "installer must ship an explicit installation-options page")
        theme = theme_path.read_text(encoding="utf-8")

        self.assertIn('ThemeFile="scripts\\windows-installer\\installer-theme.xml"', bundle)
        self.assertIn('<MsiProperty Name="CREATE_START_MENU_SHORTCUT" Value="[CreateStartMenuShortcut]" />', bundle)
        self.assertIn('<MsiProperty Name="CREATE_DESKTOP_SHORTCUT" Value="[CreateDesktopShortcut]" />', bundle)
        self.assertIn('<Editbox Name="InstallFolder"', theme)
        self.assertIn('<BrowseDirectoryAction VariableName="InstallFolder" />', theme)
        self.assertIn('<Checkbox Name="CreateStartMenuShortcut"', theme)
        self.assertIn('<Checkbox Name="CreateDesktopShortcut"', theme)
        self.assertIn('<Property Id="CREATE_START_MENU_SHORTCUT" Value="1" />', product)
        self.assertIn('<Property Id="CREATE_DESKTOP_SHORTCUT" Value="0" />', product)
        self.assertIn('Condition="CREATE_START_MENU_SHORTCUT = &quot;1&quot;"', product)
        self.assertIn('Condition="CREATE_DESKTOP_SHORTCUT = &quot;1&quot;"', product)
        self.assertIn('Id="StartMenuShortcut"', product)
        self.assertIn('Id="DesktopShortcut"', product)
        self.assertIn('Target="[INSTALLFOLDER]TunnelBoard.exe"', product)
        self.assertIn('Icon="ApplicationIcon"', product)
        self.assertIn('<Icon Id="ApplicationIcon" SourceFile="build\\windows\\icon.ico" />', product)
        self.assertIn('IconSourceFile="build\\windows\\icon.ico"', bundle)


if __name__ == "__main__":
    unittest.main()
