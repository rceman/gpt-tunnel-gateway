import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock


MODULE_PATH = Path(__file__).with_name("integration_activate.py")
SPEC = importlib.util.spec_from_file_location("integration_activate", MODULE_PATH)
integration_activate = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(integration_activate)


class IntegrationActivateTests(unittest.TestCase):
    def test_post_uses_candidate_ctl_after_build_not_stale_installed_ctl(self):
        with tempfile.TemporaryDirectory(prefix="integration-activate-candidate-ctl-") as temp:
            root = Path(temp)
            old_path = root / "old-bin"
            scripts = root / "scripts"
            home = root / "home"
            installed = home / ".local" / "bin"
            old_path.mkdir(parents=True)
            scripts.mkdir()
            installed.mkdir(parents=True)
            (root / "VERSION").write_text("0.6.15\n")

            old_ctl_log = root.parent / f"{root.name}-old-ctl.log"
            old_ctl = old_path / "gpt-tunnelctl"
            old_ctl.write_text(
                "#!/bin/sh\n"
                f"printf '%s\\n' \"$*\" >> {old_ctl_log}\n"
                "printf '%s\\n' 'old ctl does not support runtime-status' >&2\n"
                "exit 42\n"
            )
            old_ctl.chmod(0o700)
            (installed / "gpt-tunnel").write_bytes(b"candidate-cli")
            (installed / "gpt-tunnel-gatewayd").write_bytes(b"candidate-gateway")

            status = root.parent / f"{root.name}-status.json"
            log = root.parent / f"{root.name}-candidate-ctl.log"
            stale_gateway = root.parent / f"{root.name}-stale-gateway"
            stale_gateway.write_bytes(b"stale-gateway")
            status.write_text(json.dumps({
                "gateway": {
                    "running": True,
                    "pid": 100,
                    "executable": str(stale_gateway),
                    "identity_valid": True,
                },
                "tunnel": {"running": True, "pid": 514205, "identity_valid": True},
                "gateway_ready": True,
                "tunnel_ready": True,
                "version_match": True,
            }))

            candidate_ctl = root / "candidate-ctl.sh"
            candidate_ctl.write_text(
                "#!/bin/sh\n"
                "set -eu\n"
                "printf '%s %s\\n' \"${1:-}\" \"${2:-}\" >> \"$FAKE_LOG\"\n"
                "case \"${1:-}\" in\n"
                "  runtime-status) cat \"$FAKE_STATUS\" ;;\n"
                "  install-and-restart-gateway)\n"
                "    printf '{\"gateway\":{\"running\":true,\"pid\":101,\"executable\":\"%s\",\"identity_valid\":true},\"tunnel\":{\"running\":true,\"pid\":514205,\"identity_valid\":true},\"gateway_ready\":true,\"tunnel_ready\":true,\"version_match\":true}\\n' \"$FAKE_BIN/gpt-tunnel-gatewayd\" > \"$FAKE_STATUS\"\n"
                "    ;;\n"
                "  state) printf '%s\\n' '{\"valid\":true}' ;;\n"
                "  doctor) printf '%s\\n' 'doctor: ok' ;;\n"
                "  *) exit 64 ;;\n"
                "esac\n"
            )
            candidate_ctl.chmod(0o700)
            (installed / "gpt-tunnelctl").write_bytes(candidate_ctl.read_bytes())

            build = scripts / "build-release.sh"
            build.write_text(
                "#!/bin/sh\n"
                "set -eu\n"
                "mkdir -p \"$1\"\n"
                "printf 'candidate-cli' > \"$1/gpt-tunnel\"\n"
                "printf 'candidate-gateway' > \"$1/gpt-tunnel-gatewayd\"\n"
                f"cp {candidate_ctl} \"$1/gpt-tunnelctl\"\n"
                "chmod 700 \"$1/gpt-tunnelctl\"\n"
                "(cd \"$1\" && sha256sum gpt-tunnel gpt-tunnel-gatewayd gpt-tunnelctl > SHA256SUMS)\n"
            )
            build.chmod(0o700)
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=root, check=True)
            subprocess.run(["git", "config", "user.name", "Integration Test"], cwd=root, check=True)
            subprocess.run(["git", "add", "."], cwd=root, check=True)
            subprocess.run(["git", "commit", "-qm", "fixture"], cwd=root, check=True)

            old_cwd = Path.cwd()
            os.chdir(root)
            try:
                with mock.patch.object(integration_activate.Path, "home", return_value=home), mock.patch.dict(
                    os.environ,
                    {
                        "PATH": str(old_path) + os.pathsep + os.environ["PATH"],
                        "FAKE_STATUS": str(status),
                        "FAKE_LOG": str(log),
                        "FAKE_BIN": str(installed),
                    },
                ):
                    integration_activate.activate("post")
            finally:
                os.chdir(old_cwd)

            self.assertFalse(old_ctl_log.exists())
            commands = log.read_text().splitlines()
            self.assertGreaterEqual(commands.count("runtime-status "), 2)
            self.assertIn("install-and-restart-gateway --gateway-bin", commands)
            self.assertIn("state check", commands)
            self.assertIn("doctor ", commands)
            self.assertEqual(json.loads(status.read_text())["tunnel"]["pid"], 514205)

    def test_failed_child_preserves_bounded_phase_streams_and_redacts(self):
        large = "x" * (integration_activate.DIAGNOSTIC_LIMIT + 100)
        failure = subprocess.CalledProcessError(7, ["gpt-tunnelctl"], output=large, stderr="token=secret-value\nAuthorization: Bearer abc123\n" + large)
        with mock.patch.object(integration_activate.subprocess, "run", side_effect=failure):
            with self.assertRaises(RuntimeError) as raised:
                integration_activate.run(["/tmp/gpt-tunnelctl", "install-and-restart-gateway"], Path("/tmp"), phase="install_and_restart_gateway")
        payload = json.loads(str(raised.exception).split(": ", 1)[1])
        self.assertEqual(payload["phase"], "install_and_restart_gateway")
        self.assertEqual(payload["exit_code"], 7)
        self.assertTrue(payload["stdout_truncated"])
        self.assertTrue(payload["stderr_truncated"])
        self.assertNotIn("secret-value", payload["stderr"])
        self.assertNotIn("abc123", payload["stderr"])
        self.assertIn("[redacted]", payload["stderr"])
        self.assertLessEqual(len(payload["stdout"].encode()), integration_activate.DIAGNOSTIC_LIMIT)

    def test_exact_active_runtime_is_independent_of_stale_state_check(self):
        with tempfile.TemporaryDirectory(prefix="integration-activate-test-") as temp:
            root = Path(temp)
            home = root / "home"
            bin_dir = root / "bin"
            scripts = root / "scripts"
            dist = home / ".local" / "bin"
            for directory in (bin_dir, scripts, dist):
                directory.mkdir(parents=True)
            (root / "VERSION").write_text("0.6.11\n")
            candidate_ctl = root / "candidate-ctl.sh"
            candidate_ctl.write_text(
                "#!/bin/sh\n"
                "set -eu\n"
                "case \"$1${2:+ $2}\" in\n"
                "  runtime-status) cat \"$FAKE_STATUS\" ;;\n"
                "  'state check') printf '{\"valid\":false}\\n' ;;\n"
                "  doctor) printf 'doctor: ok\\n' ;;\n"
                "  *) exit 1 ;;\n"
                "esac\n"
            )
            candidate_ctl.chmod(0o700)
            build = scripts / "build-release.sh"
            build.write_text(
                "#!/bin/sh\n"
                "set -eu\n"
                "mkdir -p \"$1\"\n"
                "printf 'cli' > \"$1/gpt-tunnel\"\n"
                "printf 'gateway' > \"$1/gpt-tunnel-gatewayd\"\n"
                f"cp {candidate_ctl} \"$1/gpt-tunnelctl\"\n"
                "chmod 700 \"$1/gpt-tunnelctl\"\n"
                "(cd \"$1\" && sha256sum gpt-tunnel gpt-tunnel-gatewayd gpt-tunnelctl > SHA256SUMS)\n"
            )
            build.chmod(0o700)
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=root, check=True)
            subprocess.run(["git", "config", "user.name", "Integration Test"], cwd=root, check=True)
            subprocess.run(["git", "add", "."], cwd=root, check=True)
            subprocess.run(["git", "commit", "-qm", "fixture"], cwd=root, check=True)

            (dist / "gpt-tunnel").write_bytes(b"cli")
            (dist / "gpt-tunnel-gatewayd").write_bytes(b"gateway")
            (dist / "gpt-tunnelctl").write_bytes(candidate_ctl.read_bytes())
            ctl = bin_dir / "gpt-tunnelctl"
            log = root / "ctl.log"
            status = root / "status.json"
            status.write_text(json.dumps({
                "gateway": {
                    "running": True,
                    "pid": 100,
                    "executable": str(dist / "gpt-tunnel-gatewayd"),
                    "identity_valid": True,
                },
                "tunnel": {"running": True, "pid": 514205, "identity_valid": True},
                "gateway_ready": True,
                "tunnel_ready": True,
                "version_match": True,
            }))
            ctl.write_text(
                "#!/bin/sh\n"
                "set -eu\n"
                "case \"$1${2:+ $2}\" in\n"
                f"  status) cat {status!s} ;;\n"
                "  'state check') printf '{\"valid\":false}\\n' ;;\n"
                "  doctor) printf 'doctor: ok\\n' ;;\n"
                f"  install|restart-gateway) printf '%s\\n' \"$1\" >> {log!s}; exit 1 ;;\n"
                "  *) exit 1 ;;\n"
                "esac\n"
            )
            ctl.chmod(0o700)
            subprocess.run(["git", "add", "."], cwd=root, check=True)
            subprocess.run(["git", "commit", "-qm", "complete fixture"], cwd=root, check=True)

            old_cwd = Path.cwd()
            os.chdir(root)
            try:
                with mock.patch.object(integration_activate.Path, "home", return_value=home), mock.patch.dict(
                    os.environ,
                    {"PATH": str(bin_dir) + os.pathsep + os.environ["PATH"], "FAKE_STATUS": str(status)},
                ):
                    with self.assertRaisesRegex(RuntimeError, "durable state check failed"):
                        integration_activate.activate("post")
            finally:
                os.chdir(old_cwd)

            self.assertFalse(log.exists())


if __name__ == "__main__":
    unittest.main()
