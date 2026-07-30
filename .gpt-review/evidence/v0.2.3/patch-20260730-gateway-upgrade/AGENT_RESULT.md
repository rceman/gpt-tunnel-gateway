# Gateway upgrade evidence

Implementation commit: `85078ec634bf79f8f29f3725a097899bb839197a`.

The source-only upgrade command validates clean synchronized `main`, strict
version increase, exact release artifacts/checksums, owner-only backups, a
kernel-backed concurrent lock, atomic replacement of all three binaries,
gateway-only restart, readiness, doctor, and native MCP checks. Any post-install
failure restores the prior binaries and gateway without restarting the tunnel.

All required Go, race, static, release, version, and diff gates passed. The
active installed runtime, config, tunnel env, gateway, tunnel-client, and
ChatGPT connector were not touched during implementation or testing.
