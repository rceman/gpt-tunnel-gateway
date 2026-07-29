# Security

Bind MCP to loopback only and require an exact configured Host and Origin.
Expose typed object operations only: never generic shell, arbitrary Git, path,
or filesystem access. Secrets remain outside Git. Dispatch messages contain no
task bodies or credentials.
