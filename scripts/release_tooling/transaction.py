from __future__ import annotations

import os
import stat
import tempfile
from pathlib import Path

from .foundation import ReleaseError


def atomic_apply(repo: Path, changes: dict[str, bytes]) -> None:
    originals: dict[Path, bytes] = {}
    modes: dict[Path, int] = {}
    temporary: list[Path] = []
    try:
        for relative in changes:
            path = repo / relative
            if path.is_symlink() or not path.is_file():
                raise ReleaseError(f"release target is not a regular file: {relative}")
            originals[path] = path.read_bytes()
            modes[path] = stat.S_IMODE(path.stat().st_mode)
        for relative, content in changes.items():
            path = repo / relative
            with tempfile.NamedTemporaryFile("wb", dir=path.parent, prefix=f".{path.name}.release-", delete=False) as handle:
                handle.write(content)
                handle.flush()
                os.fsync(handle.fileno())
                temp_path = Path(handle.name)
            temp_path.chmod(modes[path])
            temporary.append(temp_path)
            os.replace(temp_path, path)
            temporary.remove(temp_path)
    except Exception as exc:
        for path, original in originals.items():
            try:
                path.write_bytes(original)
                path.chmod(modes[path])
            except OSError:
                pass
        if isinstance(exc, ReleaseError):
            raise
        raise ReleaseError(f"release application failed; original bytes restored: {exc}") from exc
    finally:
        for path in temporary:
            try:
                path.unlink()
            except FileNotFoundError:
                pass
