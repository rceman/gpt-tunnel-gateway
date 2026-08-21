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
            build = scripts / "build-release.sh"
            build.write_text(
                "#!/bin/sh\n"
                "set -eu\n"
                "mkdir -p \"$1\"\n"
                "printf 'cli' > \"$1/gpt-tunnel\"\n"
                "printf 'gateway' > \"$1/gpt-tunnel-gatewayd\"\n"
                "printf 'ctl' > \"$1/gpt-tunnelctl\"\n"
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
            (dist / "gpt-tunnelctl").write_bytes(b"ctl")
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
                    os.environ, {"PATH": str(bin_dir) + os.pathsep + os.environ["PATH"]}
                ):
                    with self.assertRaisesRegex(RuntimeError, "durable state check failed"):
                        integration_activate.activate("post")
            finally:
                os.chdir(old_cwd)

            self.assertFalse(log.exists())


if __name__ == "__main__":
    unittest.main()
